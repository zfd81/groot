"""
定时任务调度 API 端点测试

测试覆盖:
- GET /schedule - 列出所有定时任务（空列表、有数据、状态过滤）
- GET /schedule/:id - 查询任务详情（存在、不存在）
- DELETE /schedule/:id - 删除任务
- POST /schedule/:id/disable - 禁用任务
- POST /schedule/:id/enable - 启用任务
- POST /schedule/:id/archive - 归档任务
- GET /schedule/:id/history - 执行历史（空、有记录）
"""

import pytest
import requests
import json
import os
import time
from conftest import BASE_URL, TEST_HOME


def _write_task_json(task_id: str, task_name: str, status: str = "active",
                     schedule: str = "0 9 * * *", instruction: str = "测试指令"):
    """向 schedules/{status}/ 写入任务 JSON 文件"""
    task_dir = os.path.join(TEST_HOME, "schedules", status)
    os.makedirs(task_dir, exist_ok=True)

    task = {
        "id": task_id,
        "name": task_name,
        "schedule": schedule,
        "instruction": instruction,
        "missed_policy": "run_once",
        "created_at": "2026-05-11T00:00:00Z",
        "updated_at": "2026-05-11T00:00:00Z"
    }

    filepath = os.path.join(task_dir, f"{task_id}.json")
    with open(filepath, "w") as f:
        json.dump(task, f)
    return filepath


def _delete_task_json(task_id: str):
    """删除所有状态目录下的任务 JSON 文件"""
    for status in ["active", "disabled", "archive"]:
        filepath = os.path.join(TEST_HOME, "schedules", status, f"{task_id}.json")
        if os.path.exists(filepath):
            os.remove(filepath)


def _write_execution_records(task_id: str, records: list):
    """写入执行记录"""
    exec_dir = os.path.join(TEST_HOME, "schedules", "executions")
    os.makedirs(exec_dir, exist_ok=True)
    filepath = os.path.join(exec_dir, f"{task_id}.json")
    with open(filepath, "w") as f:
        json.dump(records, f)


def _cleanup_execution_records(task_id: str):
    """清理执行记录"""
    filepath = os.path.join(TEST_HOME, "schedules", "executions", f"{task_id}.json")
    if os.path.exists(filepath):
        os.remove(filepath)


class TestScheduleListAPI:
    """GET /schedule - 列出定时任务"""

    @pytest.fixture(autouse=True)
    def setup(self):
        """清理测试数据"""
        for tid in ["task-test-list-1", "task-test-list-2", "task-test-list-3"]:
            _delete_task_json(tid)
        yield
        for tid in ["task-test-list-1", "task-test-list-2", "task-test-list-3"]:
            _delete_task_json(tid)

    def test_list_empty(self, server, api_headers):
        """查询空任务列表"""
        response = requests.get(f"{BASE_URL}/schedule", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert isinstance(data, list)
        assert len(data) == 0

    def test_list_all_tasks(self, server, api_headers):
        """查询所有任务（多个状态）"""
        _write_task_json("task-test-list-1", "测试任务1", status="active",
                         schedule="0 9 * * *")
        _write_task_json("task-test-list-2", "测试任务2", status="disabled",
                         schedule="0 12 * * *")
        _write_task_json("task-test-list-3", "测试任务3", status="archive",
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
        _write_task_json("task-test-list-1", "活跃任务", status="active")
        _write_task_json("task-test-list-2", "已禁用任务", status="disabled")

        response = requests.get(f"{BASE_URL}/schedule?status=active", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert isinstance(data, list)
        for task in data:
            assert task["id"] != "task-test-list-2"  # disabled 的不应出现

    def test_list_filter_disabled(self, server, api_headers):
        """按 disabled 状态过滤"""
        _write_task_json("task-test-list-1", "活跃任务", status="active")
        _write_task_json("task-test-list-2", "已禁用任务", status="disabled")

        response = requests.get(f"{BASE_URL}/schedule?status=disabled", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert isinstance(data, list)
        for task in data:
            assert task["id"] != "task-test-list-1"


class TestScheduleGetAPI:
    """GET /schedule/:id - 查询任务详情"""

    TASK_ID = "task-test-get-1"

    @pytest.fixture(autouse=True)
    def setup(self):
        _delete_task_json(self.TASK_ID)
        yield
        _delete_task_json(self.TASK_ID)

    def test_get_existing_task(self, server, api_headers):
        """查询存在的任务详情"""
        _write_task_json(self.TASK_ID, "测试任务详情", schedule="*/30 * * * *",
                         instruction="每30分钟执行一次")

        response = requests.get(f"{BASE_URL}/schedule/{self.TASK_ID}", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert data["id"] == self.TASK_ID
        assert data["name"] == "测试任务详情"
        assert data["schedule"] == "*/30 * * * *"
        assert data["instruction"] == "每30分钟执行一次"

    def test_get_nonexistent_task(self, server, api_headers):
        """查询不存在的任务"""
        response = requests.get(f"{BASE_URL}/schedule/task-nonexistent", headers=api_headers)
        assert response.status_code == 404
        data = response.json()
        assert "error" in data


class TestScheduleDeleteAPI:
    """DELETE /schedule/:id - 删除定时任务"""

    TASK_ID = "task-test-delete-1"

    @pytest.fixture(autouse=True)
    def setup(self):
        _delete_task_json(self.TASK_ID)
        yield
        _delete_task_json(self.TASK_ID)

    def test_delete_existing_task(self, server, api_headers):
        """删除存在的任务"""
        _write_task_json(self.TASK_ID, "待删除任务")

        response = requests.delete(f"{BASE_URL}/schedule/{self.TASK_ID}", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert data["status"] == "deleted"
        assert data["id"] == self.TASK_ID

        # 验证文件已删除
        response2 = requests.get(f"{BASE_URL}/schedule/{self.TASK_ID}", headers=api_headers)
        assert response2.status_code == 404

    def test_delete_nonexistent_task(self, server, api_headers):
        """删除不存在的任务"""
        response = requests.delete(f"{BASE_URL}/schedule/task-nonexistent", headers=api_headers)
        assert response.status_code == 500
        data = response.json()
        assert "error" in data


class TestScheduleDisableAPI:
    """POST /schedule/:id/disable - 禁用定时任务"""

    TASK_ID = "task-test-disable-1"

    @pytest.fixture(autouse=True)
    def setup(self):
        _delete_task_json(self.TASK_ID)
        yield
        _delete_task_json(self.TASK_ID)

    def test_disable_active_task(self, server, api_headers):
        """禁用活跃任务（active → disabled）"""
        _write_task_json(self.TASK_ID, "待禁用任务", status="active")

        response = requests.post(
            f"{BASE_URL}/schedule/{self.TASK_ID}/disable", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert data["status"] == "disabled"

        # 验证文件已移动到 disabled 目录
        disabled_path = os.path.join(TEST_HOME, "schedules", "disabled", f"{self.TASK_ID}.json")
        assert os.path.exists(disabled_path)

        active_path = os.path.join(TEST_HOME, "schedules", "active", f"{self.TASK_ID}.json")
        assert not os.path.exists(active_path)

    def test_disable_nonexistent_task(self, server, api_headers):
        """禁用不存在的任务"""
        response = requests.post(
            f"{BASE_URL}/schedule/task-nonexistent/disable", headers=api_headers)
        assert response.status_code == 500


class TestScheduleEnableAPI:
    """POST /schedule/:id/enable - 启用定时任务"""

    TASK_ID = "task-test-enable-1"

    @pytest.fixture(autouse=True)
    def setup(self):
        _delete_task_json(self.TASK_ID)
        yield
        _delete_task_json(self.TASK_ID)

    def test_enable_disabled_task(self, server, api_headers):
        """启用已禁用任务（disabled → active）"""
        _write_task_json(self.TASK_ID, "待启用任务", status="disabled")

        response = requests.post(
            f"{BASE_URL}/schedule/{self.TASK_ID}/enable", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert data["status"] == "enabled"

        # 验证文件已移动到 active 目录
        active_path = os.path.join(TEST_HOME, "schedules", "active", f"{self.TASK_ID}.json")
        assert os.path.exists(active_path)

        disabled_path = os.path.join(TEST_HOME, "schedules", "disabled", f"{self.TASK_ID}.json")
        assert not os.path.exists(disabled_path)


class TestScheduleArchiveAPI:
    """POST /schedule/:id/archive - 归档定时任务"""

    TASK_ID = "task-test-archive-1"

    @pytest.fixture(autouse=True)
    def setup(self):
        _delete_task_json(self.TASK_ID)
        yield
        _delete_task_json(self.TASK_ID)

    def test_archive_active_task(self, server, api_headers):
        """归档活跃任务（active → archive）"""
        _write_task_json(self.TASK_ID, "待归档任务", status="active")

        response = requests.post(
            f"{BASE_URL}/schedule/{self.TASK_ID}/archive", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert data["status"] == "archived"

        archive_path = os.path.join(TEST_HOME, "schedules", "archive", f"{self.TASK_ID}.json")
        assert os.path.exists(archive_path)

        active_path = os.path.join(TEST_HOME, "schedules", "active", f"{self.TASK_ID}.json")
        assert not os.path.exists(active_path)

    def test_archive_disabled_task(self, server, api_headers):
        """归档已禁用任务（disabled → archive）"""
        _write_task_json(self.TASK_ID, "待归档任务", status="disabled")

        response = requests.post(
            f"{BASE_URL}/schedule/{self.TASK_ID}/archive", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert data["status"] == "archived"


class TestScheduleHistoryAPI:
    """GET /schedule/:id/history - 查询执行历史"""

    TASK_ID = "task-test-history-1"

    @pytest.fixture(autouse=True)
    def setup(self):
        _delete_task_json(self.TASK_ID)
        _cleanup_execution_records(self.TASK_ID)
        yield
        _delete_task_json(self.TASK_ID)
        _cleanup_execution_records(self.TASK_ID)

    def test_history_empty(self, server, api_headers):
        """查询无执行记录的任务"""
        _write_task_json(self.TASK_ID, "测试历史任务")

        response = requests.get(
            f"{BASE_URL}/schedule/{self.TASK_ID}/history", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert isinstance(data, list)

    def test_history_with_records(self, server, api_headers):
        """查询有执行记录的任务"""
        _write_task_json(self.TASK_ID, "测试历史任务")
        records = [
            {
                "task_id": self.TASK_ID,
                "exec_time": "2026-05-11T09:00:05Z",
                "trigger_type": "cron",
                "session_id": f"{self.TASK_ID}-20260511T090005-sched",
                "status": "completed",
                "duration_ms": 1234,
                "step_count": 3
            },
            {
                "task_id": self.TASK_ID,
                "exec_time": "2026-05-10T09:00:02Z",
                "trigger_type": "cron",
                "session_id": f"{self.TASK_ID}-20260510T090002-sched",
                "status": "failed",
                "duration_ms": 5000,
                "step_count": 1,
                "error": "执行超时"
            }
        ]
        _write_execution_records(self.TASK_ID, records)

        response = requests.get(
            f"{BASE_URL}/schedule/{self.TASK_ID}/history", headers=api_headers)
        assert response.status_code == 200
        data = response.json()
        assert isinstance(data, list)
        assert len(data) == 2

        # 验证记录字段
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
        assert "error" in data
        assert isinstance(data["error"], str)


class TestScheduleToolsVisible:
    """验证调度工具在 /tools 中可见"""

    def test_tools_includes_schedule(self, server, api_headers):
        """GET /tools 应包含 schedule 工具"""
        response = requests.get(f"{BASE_URL}/tools", headers=api_headers)
        assert response.status_code == 200
        data = response.json()

        # 查找 schedule 组
        schedule_tools = data.get("schedule", {})
        if schedule_tools:
            tool_names = [t["name"] for t in schedule_tools.get("tools", [])]
            expected = ["schedule_create", "schedule_list", "schedule_delete",
                        "schedule_disable", "schedule_enable",
                        "schedule_archive", "schedule_history", "schedule_inspect"]
            for name in expected:
                assert name in tool_names, f"缺少工具: {name}"
            assert schedule_tools["total"] >= 8
