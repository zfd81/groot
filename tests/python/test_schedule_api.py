"""
定时任务调度 API 端点测试

测试覆盖:
- GET /schedule - 列出所有定时任务（空列表、有数据、状态过滤 active/disabled/archive）
- GET /schedule/:id - 查询任务详情（存在、不存在）
- DELETE /schedule/:id - 删除任务
- POST /schedule/:id/disable - 禁用任务
- POST /schedule/:id/enable - 启用任务
- POST /schedule/:id/archive - 归档任务
- GET /schedule/:id/history - 执行历史（空、有记录）
- /web/tools 中调度工具可见性（Leader + schedule.enabled=true）

任务与执行记录已全部入库（{GROOT_HOME}/groot.db）：
- schedule_tasks(task_id, name, schedule_expr, status, payload, ...) —— payload
  为 schedule.Task 的 JSON 序列化（internal/repo/scheduledb/schedule.go）
- schedule_executions(execution_id, task_id, started_at, finished_at, status,
  detail) —— detail 为 schedule.ExecutionRecord 的 JSON 序列化
预置任务通过 Python sqlite3 直插数据库；List/Get/History API 直接读数据库
（Manager → Storage → ScheduleRepo），无需等待 sync_interval 同步周期。

说明：非 Leader 或 schedule.enabled=false 时端点返回 503 schedule_unavailable
（internal/api/handler/schedule.go）；共享测试服务 schedule.enabled=true 且单实例
即 Leader，此分支在 test_cli_commands.py 的独立实例（schedule 默认关闭）上覆盖。
"""

import pytest
import requests
import json
import os
import sqlite3
import time
from datetime import datetime
from conftest import BASE_URL, TEST_HOME


# SQLite 数据库文件（conftest server fixture 的 GROOT_HOME 下）
DB_PATH = os.path.join(TEST_HOME, "groot.db")


def _db():
    """打开测试库连接（服务端使用 WAL + busy_timeout，Python 侧设 timeout 即可）"""
    conn = sqlite3.connect(DB_PATH, timeout=5)
    return conn


def _now_ms() -> int:
    return int(time.time() * 1000)


def _insert_schedule(task_id: str, task_name: str, status: str = "active",
                     schedule: str = "0 9 * * *", instruction: str = "测试指令"):
    """向 schedule_tasks 表直插一条任务记录。

    payload 结构与 internal/schedule/types.go 的 Task JSON 序列化一致；
    status 存独立列（payload 中的 status 由 repo 读取时覆盖，不必写入）。
    created_at/updated_at 列为毫秒时间戳（UnixMilli）。
    """
    payload = {
        "id": task_id,
        "name": task_name,
        "schedule": schedule,
        "missed_policy": "run_once",
        "task": {
            "instruction": instruction,
            "model": "",
            "system_prompt": ""
        },
        "notification": {
            "on_success": [],
            "on_failure": []
        },
        "created_at": "2026-05-11T00:00:00Z",
        "updated_at": "2026-05-11T00:00:00Z"
    }

    now = _now_ms()
    conn = _db()
    try:
        conn.execute(
            """INSERT INTO schedule_tasks
                 (task_id, name, schedule_expr, status, payload,
                  next_run_at, last_run_at, version, created_at, updated_at)
               VALUES (?, ?, ?, ?, ?, NULL, NULL, 0, ?, ?)""",
            (task_id, task_name, schedule, status,
             json.dumps(payload, ensure_ascii=False), now, now),
        )
        conn.commit()
    finally:
        conn.close()


def _delete_schedule(task_id: str):
    """删除任务及其执行记录（清理预置数据，避免污染共享数据库）"""
    if not os.path.exists(DB_PATH):
        return
    conn = _db()
    try:
        conn.execute("DELETE FROM schedule_tasks WHERE task_id = ?", (task_id,))
        conn.execute("DELETE FROM schedule_executions WHERE task_id = ?", (task_id,))
        conn.commit()
    finally:
        conn.close()


def _insert_execution_records(task_id: str, records: list):
    """向 schedule_executions 表直插执行记录。

    detail 列为 ExecutionRecord 的 JSON（字段见 internal/schedule/types.go）；
    started_at 列为毫秒时间戳，History API 按 started_at DESC 排序。
    """
    conn = _db()
    try:
        for record in records:
            started_at = record["started_at"]  # RFC3339 字符串
            started_ms = int(datetime.fromisoformat(
                started_at.replace("Z", "+00:00")).timestamp() * 1000)
            finished_ms = None
            if record.get("finished_at"):
                finished_ms = int(datetime.fromisoformat(
                    record["finished_at"].replace("Z", "+00:00")).timestamp() * 1000)
            conn.execute(
                """INSERT INTO schedule_executions
                     (execution_id, task_id, started_at, finished_at, status, detail)
                   VALUES (?, ?, ?, ?, ?, ?)""",
                (record["execution_id"], task_id, started_ms, finished_ms,
                 record["status"], json.dumps(record, ensure_ascii=False)),
            )
        conn.commit()
    finally:
        conn.close()


class TestScheduleListAPI:
    """GET /schedule - 列出定时任务"""

    TASK_IDS = ["task-test-list-1", "task-test-list-2", "task-test-list-3"]

    @pytest.fixture(autouse=True)
    def setup(self, server):
        """清理测试数据（唯一 id，前后各清一次避免残留）"""
        for tid in self.TASK_IDS:
            _delete_schedule(tid)
        yield
        for tid in self.TASK_IDS:
            _delete_schedule(tid)

    def test_list_empty(self, server, api_headers):
        """查询任务列表：本文件预置的任务清理后不应存在"""
        response = requests.get(f"{BASE_URL}/schedule", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert isinstance(data, list)
        # 共享数据库可能存在其他来源的任务，只断言本文件的预置任务已清理
        ids = [t["id"] for t in data]
        for tid in self.TASK_IDS:
            assert tid not in ids

    def test_list_all_tasks(self, server, api_headers):
        """查询所有任务（多个状态）"""
        _insert_schedule("task-test-list-1", "测试任务1", status="active",
                         schedule="0 9 * * *")
        _insert_schedule("task-test-list-2", "测试任务2", status="disabled",
                         schedule="0 12 * * *")
        _insert_schedule("task-test-list-3", "测试任务3", status="archive",
                         schedule="2026-06-01T00:00:00Z")

        response = requests.get(f"{BASE_URL}/schedule", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert isinstance(data, list)
        assert len(data) >= 3

        # 验证字段
        task = data[0]
        assert "id" in task
        assert "name" in task
        assert "schedule" in task
        assert "created_at" in task

    def test_list_filter_active(self, server, api_headers):
        """按 active 状态过滤"""
        _insert_schedule("task-test-list-1", "活跃任务", status="active")
        _insert_schedule("task-test-list-2", "已禁用任务", status="disabled")

        response = requests.get(f"{BASE_URL}/schedule?status=active", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert isinstance(data, list)
        ids = [t["id"] for t in data]
        assert "task-test-list-1" in ids
        assert "task-test-list-2" not in ids  # disabled 的不应出现

    def test_list_filter_disabled(self, server, api_headers):
        """按 disabled 状态过滤"""
        _insert_schedule("task-test-list-1", "活跃任务", status="active")
        _insert_schedule("task-test-list-2", "已禁用任务", status="disabled")

        response = requests.get(f"{BASE_URL}/schedule?status=disabled", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert isinstance(data, list)
        ids = [t["id"] for t in data]
        assert "task-test-list-2" in ids
        assert "task-test-list-1" not in ids

    def test_list_filter_archive(self, server, api_headers):
        """按 archive 状态过滤（归档任务不出现在 active/disabled 过滤中）"""
        _insert_schedule("task-test-list-1", "活跃任务", status="active")
        _insert_schedule("task-test-list-3", "已归档任务", status="archive")

        response = requests.get(f"{BASE_URL}/schedule?status=archive", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert isinstance(data, list)
        ids = [t["id"] for t in data]
        assert "task-test-list-3" in ids
        assert "task-test-list-1" not in ids  # active 的不应出现

        # 反向验证：active 过滤不含归档任务
        response = requests.get(f"{BASE_URL}/schedule?status=active", headers=api_headers)
        assert response.status_code == 200
        assert "task-test-list-3" not in [t["id"] for t in response.json()]


class TestScheduleGetAPI:
    """GET /schedule/:id - 查询任务详情"""

    TASK_ID = "task-test-get-1"

    @pytest.fixture(autouse=True)
    def setup(self, server):
        _delete_schedule(self.TASK_ID)
        yield
        _delete_schedule(self.TASK_ID)

    def test_get_existing_task(self, server, api_headers):
        """查询存在的任务详情"""
        _insert_schedule(self.TASK_ID, "测试任务详情", schedule="*/30 * * * *",
                         instruction="每30分钟执行一次")

        response = requests.get(f"{BASE_URL}/schedule/{self.TASK_ID}", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert data["id"] == self.TASK_ID
        assert data["name"] == "测试任务详情"
        assert data["schedule"] == "*/30 * * * *"
        assert data["task"]["instruction"] == "每30分钟执行一次"

    def test_get_nonexistent_task(self, server, api_headers):
        """查询不存在的任务"""
        response = requests.get(f"{BASE_URL}/schedule/task-nonexistent", headers=api_headers)
        assert response.status_code == 404
        data = response.json()
        assert "status" in data
        assert "message" in data


class TestScheduleDeleteAPI:
    """DELETE /schedule/:id - 删除定时任务"""

    TASK_ID = "task-test-delete-1"

    @pytest.fixture(autouse=True)
    def setup(self, server):
        _delete_schedule(self.TASK_ID)
        yield
        _delete_schedule(self.TASK_ID)

    def test_delete_existing_task(self, server, api_headers):
        """删除存在的任务"""
        _insert_schedule(self.TASK_ID, "待删除任务")

        response = requests.delete(f"{BASE_URL}/schedule/{self.TASK_ID}", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert data["status"] == "deleted"
        assert data["id"] == self.TASK_ID

        # 再次查询应为 404（数据库记录已删除）
        response2 = requests.get(f"{BASE_URL}/schedule/{self.TASK_ID}", headers=api_headers)
        assert response2.status_code == 404

    def test_delete_nonexistent_task(self, server, api_headers):
        """删除不存在的任务"""
        response = requests.delete(f"{BASE_URL}/schedule/task-nonexistent", headers=api_headers)
        assert response.status_code == 500
        data = response.json()
        assert "status" in data
        assert "message" in data


class TestScheduleDisableAPI:
    """POST /schedule/:id/disable - 禁用定时任务"""

    TASK_ID = "task-test-disable-1"

    @pytest.fixture(autouse=True)
    def setup(self, server):
        _delete_schedule(self.TASK_ID)
        yield
        _delete_schedule(self.TASK_ID)

    def test_disable_active_task(self, server, api_headers):
        """禁用活跃任务（active → disabled）"""
        _insert_schedule(self.TASK_ID, "待禁用任务", status="active")

        response = requests.post(
            f"{BASE_URL}/schedule/{self.TASK_ID}/disable", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert data["status"] == "disabled"

        # 再 GET 验证任务状态已变更为 disabled（状态即数据库 status 列）
        response2 = requests.get(f"{BASE_URL}/schedule/{self.TASK_ID}", headers=api_headers)
        assert response2.status_code == 200
        assert response2.json()["status"] == "disabled"

    def test_disable_nonexistent_task(self, server, api_headers):
        """禁用不存在的任务"""
        response = requests.post(
            f"{BASE_URL}/schedule/task-nonexistent/disable", headers=api_headers)
        assert response.status_code == 500


class TestScheduleEnableAPI:
    """POST /schedule/:id/enable - 启用定时任务"""

    TASK_ID = "task-test-enable-1"

    @pytest.fixture(autouse=True)
    def setup(self, server):
        _delete_schedule(self.TASK_ID)
        yield
        _delete_schedule(self.TASK_ID)

    def test_enable_disabled_task(self, server, api_headers):
        """启用已禁用任务（disabled → active）"""
        _insert_schedule(self.TASK_ID, "待启用任务", status="disabled")

        response = requests.post(
            f"{BASE_URL}/schedule/{self.TASK_ID}/enable", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert data["status"] == "enabled"

        # 再 GET 验证任务状态已变更为 active
        response2 = requests.get(f"{BASE_URL}/schedule/{self.TASK_ID}", headers=api_headers)
        assert response2.status_code == 200
        assert response2.json()["status"] == "active"


class TestScheduleArchiveAPI:
    """POST /schedule/:id/archive - 归档定时任务"""

    TASK_ID = "task-test-archive-1"

    @pytest.fixture(autouse=True)
    def setup(self, server):
        _delete_schedule(self.TASK_ID)
        yield
        _delete_schedule(self.TASK_ID)

    def test_archive_active_task(self, server, api_headers):
        """归档活跃任务（active → archive）"""
        _insert_schedule(self.TASK_ID, "待归档任务", status="active")

        response = requests.post(
            f"{BASE_URL}/schedule/{self.TASK_ID}/archive", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert data["status"] == "archived"

        # 再 GET 验证任务状态已变更为 archive
        response2 = requests.get(f"{BASE_URL}/schedule/{self.TASK_ID}", headers=api_headers)
        assert response2.status_code == 200
        assert response2.json()["status"] == "archive"

    def test_archive_disabled_task(self, server, api_headers):
        """归档已禁用任务（disabled → archive）"""
        _insert_schedule(self.TASK_ID, "待归档任务", status="disabled")

        response = requests.post(
            f"{BASE_URL}/schedule/{self.TASK_ID}/archive", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert data["status"] == "archived"


class TestScheduleHistoryAPI:
    """GET /schedule/:id/history - 查询执行历史"""

    TASK_ID = "task-test-history-1"

    @pytest.fixture(autouse=True)
    def setup(self, server):
        _delete_schedule(self.TASK_ID)
        yield
        _delete_schedule(self.TASK_ID)

    def test_history_empty(self, server, api_headers):
        """查询无执行记录的任务"""
        _insert_schedule(self.TASK_ID, "测试历史任务")

        response = requests.get(
            f"{BASE_URL}/schedule/{self.TASK_ID}/history", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert isinstance(data, list)
        assert len(data) == 0

    def test_history_with_records(self, server, api_headers):
        """查询有执行记录的任务"""
        _insert_schedule(self.TASK_ID, "测试历史任务")
        # ExecutionRecord 字段见 internal/schedule/types.go（exec_time 已更名 started_at）
        records = [
            {
                "execution_id": f"{self.TASK_ID}-20260511T090005",
                "task_id": self.TASK_ID,
                "started_at": "2026-05-11T09:00:05Z",
                "finished_at": "2026-05-11T09:00:06Z",
                "trigger_type": "cron",
                "session_id": f"{self.TASK_ID}-20260511T090005-sched",
                "chat_id": "",
                "status": "completed",
                "duration_ms": 1234,
                "step_count": 3,
                "error": "",
                "notifications": []
            },
            {
                "execution_id": f"{self.TASK_ID}-20260510T090002",
                "task_id": self.TASK_ID,
                "started_at": "2026-05-10T09:00:02Z",
                "finished_at": "2026-05-10T09:00:07Z",
                "trigger_type": "cron",
                "session_id": f"{self.TASK_ID}-20260510T090002-sched",
                "chat_id": "",
                "status": "failed",
                "duration_ms": 5000,
                "step_count": 1,
                "error": "执行超时",
                "notifications": []
            }
        ]
        _insert_execution_records(self.TASK_ID, records)

        response = requests.get(
            f"{BASE_URL}/schedule/{self.TASK_ID}/history", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert isinstance(data, list)
        assert len(data) == 2

        # 验证记录字段（按 started_at DESC 排序，第一条应为最新的 completed）
        first = data[0]
        assert first["task_id"] == self.TASK_ID
        assert first["trigger_type"] == "cron"
        assert first["status"] == "completed"
        assert first["duration_ms"] == 1234
        assert first["step_count"] == 3
        assert "session_id" in first
        assert first["session_id"].endswith("-sched")


class TestScheduleAPIAuth:
    """定时任务 API 认证测试"""

    def test_list_without_auth(self, server, no_auth_headers):
        """无认证访问应返回 401"""
        response = requests.get(f"{BASE_URL}/schedule", headers=no_auth_headers)
        assert response.status_code == 401

    def test_get_without_auth(self, server, no_auth_headers):
        """无认证访问应返回 401"""
        response = requests.get(f"{BASE_URL}/schedule/task-xxx", headers=no_auth_headers)
        assert response.status_code == 401


class TestScheduleAPIResponseFormat:
    """定时任务 API 响应格式验证"""

    def test_list_response_format(self, server, api_headers):
        """验证列表响应格式"""
        response = requests.get(f"{BASE_URL}/schedule", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert isinstance(data, list)

    def test_error_response_format(self, server, api_headers):
        """验证错误响应格式"""
        response = requests.get(f"{BASE_URL}/schedule/task-nonexistent-xxxxx", headers=api_headers)
        assert response.status_code == 404
        data = response.json()
        assert "status" in data
        assert "message" in data
        assert isinstance(data["status"], str)
        assert isinstance(data["message"], str)


class TestScheduleToolsVisible:
    """验证调度工具在 /web/tools 中可见

    调度工具仅在实例为 Leader 且 schedule.enabled=true 时注册（cmd/groot/main.go
    startLeaderTasks）；conftest 配置 schedule.enabled=true，单实例即 Leader，
    内置工具以 group "schedule" 出现（internal/mcp/manager.go GetToolInfos）。
    """

    def test_tools_includes_schedule(self, server):
        """GET /web/tools 应包含全部 8 个 schedule 工具"""
        from conftest import _web_login
        web = _web_login(BASE_URL)

        response = web.get(f"{BASE_URL}/web/tools")
        assert response.status_code == 200
        data = response.json()

        # schedule 组必须存在（Leader + enabled）
        schedule_tools = data.get("schedule", {})
        assert schedule_tools, f"/web/tools 应含 schedule 组，实际 groups: {list(data.keys())}"

        # 工具名与 internal/schedule/tools.go 常量一致
        tool_names = [t["name"] for t in schedule_tools.get("tools", [])]
        expected = ["schedule_create", "schedule_list", "schedule_delete",
                    "schedule_disable", "schedule_enable",
                    "schedule_archive", "schedule_history", "schedule_inspect"]
        for name in expected:
            assert name in tool_names, f"缺少工具: {name}"
        assert schedule_tools["total"] >= 8
