"""
定时任务调度 CLI 命令测试

测试覆盖:
- groot schedule list - 列出所有任务（空、有数据）
- groot schedule inspect - 查看任务详情
- groot schedule history - 查看执行历史
- groot schedule delete - 删除任务
- groot schedule disable - 禁用任务
- groot schedule enable - 启用任务
- groot schedule archive - 归档任务
- groot schedule --help - 帮助信息
"""

import pytest
import json
import os
import subprocess
from conftest import GROOT_BIN, TEST_HOME


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
        json.dump(task, f, ensure_ascii=False)
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
        json.dump(records, f, ensure_ascii=False)


def _cleanup_execution_records(task_id: str):
    """清理执行记录"""
    filepath = os.path.join(TEST_HOME, "schedules", "executions", f"{task_id}.json")
    if os.path.exists(filepath):
        os.remove(filepath)


def _run_schedule_cmd(args: list) -> subprocess.CompletedProcess:
    """运行 groot schedule 命令"""
    env = os.environ.copy()
    env["GROOT_HOME"] = TEST_HOME
    return subprocess.run(
        [GROOT_BIN, "schedule"] + args,
        env=env,
        capture_output=True,
        text=True
    )


class TestScheduleCLIList:
    """groot schedule list 测试"""

    @pytest.fixture(autouse=True)
    def setup(self):
        for tid in ["task-cli-list-1", "task-cli-list-2", "task-cli-list-3"]:
            _delete_task_json(tid)
        yield
        for tid in ["task-cli-list-1", "task-cli-list-2", "task-cli-list-3"]:
            _delete_task_json(tid)

    def test_list_empty(self):
        """空任务列表"""
        result = _run_schedule_cmd(["list"])
        assert result.returncode == 0
        assert "没有定时任务" in result.stdout

    def test_list_with_tasks(self):
        """有任务时的列表"""
        _write_task_json("task-cli-list-1", "测试任务1", status="active",
                         schedule="0 9 * * *")
        _write_task_json("task-cli-list-2", "测试任务2", status="disabled",
                         schedule="0 12 * * *")
        _write_task_json("task-cli-list-3", "测试任务3", status="archive",
                         schedule="2026-06-01T00:00:00Z")

        result = _run_schedule_cmd(["list"])
        assert result.returncode == 0
        assert "task-cli-list-1" in result.stdout
        assert "task-cli-list-2" in result.stdout
        assert "task-cli-list-3" in result.stdout
        assert "active" in result.stdout
        assert "disabled" in result.stdout
        assert "archive" in result.stdout
        # 统计信息
        assert "共 3 个任务" in result.stdout or "个任务" in result.stdout

    def test_list_output_has_header(self):
        """列表输出应有表头"""
        _write_task_json("task-cli-list-1", "测试任务1")

        result = _run_schedule_cmd(["list"])
        assert result.returncode == 0
        assert "ID" in result.stdout
        assert "NAME" in result.stdout
        assert "SCHEDULE" in result.stdout
        assert "STATUS" in result.stdout


class TestScheduleCLIInspect:
    """groot schedule inspect 测试"""

    TASK_ID = "task-cli-inspect-1"

    @pytest.fixture(autouse=True)
    def setup(self):
        _delete_task_json(self.TASK_ID)
        yield
        _delete_task_json(self.TASK_ID)

    def test_inspect_existing_task(self):
        """查看存在的任务详情"""
        _write_task_json(self.TASK_ID, "详情任务", schedule="*/30 * * * *",
                         instruction="每30分钟生成报表")

        result = _run_schedule_cmd(["inspect", self.TASK_ID])
        assert result.returncode == 0
        assert self.TASK_ID in result.stdout
        assert "详情任务" in result.stdout
        assert "*/30 * * * *" in result.stdout

    def test_inspect_nonexistent_task(self):
        """查看不存在的任务"""
        result = _run_schedule_cmd(["inspect", "task-nonexistent"])
        assert result.returncode != 0
        assert "不存在" in result.stderr

    def test_inspect_json_format(self):
        """验证输出为合法 JSON"""
        _write_task_json(self.TASK_ID, "JSON任务")

        result = _run_schedule_cmd(["inspect", self.TASK_ID])
        assert result.returncode == 0
        # 输出应可解析为 JSON
        data = json.loads(result.stdout)
        assert data["id"] == self.TASK_ID
        assert "name" in data
        assert "schedule" in data


class TestScheduleCLIHistory:
    """groot schedule history 测试"""

    TASK_ID = "task-cli-history-1"

    @pytest.fixture(autouse=True)
    def setup(self):
        _delete_task_json(self.TASK_ID)
        _cleanup_execution_records(self.TASK_ID)
        yield
        _delete_task_json(self.TASK_ID)
        _cleanup_execution_records(self.TASK_ID)

    def test_history_empty(self):
        """无执行记录"""
        _write_task_json(self.TASK_ID, "历史任务")

        result = _run_schedule_cmd(["history", self.TASK_ID])
        assert result.returncode != 0
        assert "没有找到任务" in result.stderr or "暂无执行记录" in result.stdout

    def test_history_with_records(self):
        """有执行记录"""
        _write_task_json(self.TASK_ID, "历史任务")
        records = [
            {
                "task_id": self.TASK_ID,
                "exec_time": "2026-05-11T09:00:05Z",
                "trigger_type": "cron",
                "session_id": f"{self.TASK_ID}-20260511T090005-sched",
                "status": "completed",
                "duration_ms": 1234,
                "step_count": 3
            }
        ]
        _write_execution_records(self.TASK_ID, records)

        result = _run_schedule_cmd(["history", self.TASK_ID])
        assert result.returncode == 0
        assert "completed" in result.stdout
        assert "cron" in result.stdout
        assert "1234ms" in result.stdout

    def test_history_nonexistent_task(self):
        """查看不存在任务的执行历史"""
        result = _run_schedule_cmd(["history", "task-nonexistent"])
        # 任务不存在 -> 也没有执行记录文件 -> 返回错误
        assert result.returncode != 0


class TestScheduleCLIDelete:
    """groot schedule delete 测试"""

    TASK_ID = "task-cli-delete-1"

    @pytest.fixture(autouse=True)
    def setup(self):
        _delete_task_json(self.TASK_ID)
        yield
        _delete_task_json(self.TASK_ID)

    def test_delete_existing_task(self):
        """删除存在的任务"""
        filepath = _write_task_json(self.TASK_ID, "待删除任务")
        assert os.path.exists(filepath)

        result = _run_schedule_cmd(["delete", self.TASK_ID])
        assert result.returncode == 0
        assert "已删除" in result.stdout
        assert not os.path.exists(filepath)

    def test_delete_nonexistent_task(self):
        """删除不存在的任务"""
        result = _run_schedule_cmd(["delete", "task-nonexistent"])
        assert result.returncode != 0
        assert "不存在" in result.stderr


class TestScheduleCLIDisable:
    """groot schedule disable 测试"""

    TASK_ID = "task-cli-disable-1"

    @pytest.fixture(autouse=True)
    def setup(self):
        _delete_task_json(self.TASK_ID)
        yield
        _delete_task_json(self.TASK_ID)

    def test_disable_active_task(self):
        """禁用活跃任务（active → disabled）"""
        _write_task_json(self.TASK_ID, "待禁用任务", status="active")

        result = _run_schedule_cmd(["disable", self.TASK_ID])
        assert result.returncode == 0
        assert "已禁用" in result.stdout

        # 验证文件已移动
        active_path = os.path.join(TEST_HOME, "schedules", "active", f"{self.TASK_ID}.json")
        disabled_path = os.path.join(TEST_HOME, "schedules", "disabled", f"{self.TASK_ID}.json")
        assert not os.path.exists(active_path)
        assert os.path.exists(disabled_path)

    def test_disable_nonexistent_task(self):
        """禁用不存在的任务"""
        result = _run_schedule_cmd(["disable", "task-nonexistent"])
        assert result.returncode != 0
        assert "不存在" in result.stderr


class TestScheduleCLIEnable:
    """groot schedule enable 测试"""

    TASK_ID = "task-cli-enable-1"

    @pytest.fixture(autouse=True)
    def setup(self):
        _delete_task_json(self.TASK_ID)
        yield
        _delete_task_json(self.TASK_ID)

    def test_enable_disabled_task(self):
        """启用已禁用任务（disabled → active）"""
        _write_task_json(self.TASK_ID, "待启用任务", status="disabled")

        result = _run_schedule_cmd(["enable", self.TASK_ID])
        assert result.returncode == 0
        assert "已启用" in result.stdout

        active_path = os.path.join(TEST_HOME, "schedules", "active", f"{self.TASK_ID}.json")
        disabled_path = os.path.join(TEST_HOME, "schedules", "disabled", f"{self.TASK_ID}.json")
        assert os.path.exists(active_path)
        assert not os.path.exists(disabled_path)

    def test_enable_nonexistent_task(self):
        """启用不存在的任务"""
        result = _run_schedule_cmd(["enable", "task-nonexistent"])
        assert result.returncode != 0
        assert "不存在" in result.stderr


class TestScheduleCLIArchive:
    """groot schedule archive 测试"""

    TASK_ID = "task-cli-archive-1"

    @pytest.fixture(autouse=True)
    def setup(self):
        _delete_task_json(self.TASK_ID)
        yield
        _delete_task_json(self.TASK_ID)

    def test_archive_active_task(self):
        """归档活跃任务"""
        _write_task_json(self.TASK_ID, "待归档任务", status="active")

        result = _run_schedule_cmd(["archive", self.TASK_ID])
        assert result.returncode == 0
        assert "已归档" in result.stdout

        archive_path = os.path.join(TEST_HOME, "schedules", "archive", f"{self.TASK_ID}.json")
        active_path = os.path.join(TEST_HOME, "schedules", "active", f"{self.TASK_ID}.json")
        assert os.path.exists(archive_path)
        assert not os.path.exists(active_path)

    def test_archive_disabled_task(self):
        """归档已禁用任务"""
        _write_task_json(self.TASK_ID, "待归档任务", status="disabled")

        result = _run_schedule_cmd(["archive", self.TASK_ID])
        assert result.returncode == 0
        assert "已归档" in result.stdout

    def test_archive_nonexistent_task(self):
        """归档不存在的任务"""
        result = _run_schedule_cmd(["archive", "task-nonexistent"])
        assert result.returncode != 0
        assert "不存在" in result.stderr


class TestScheduleCLIHelp:
    """groot schedule 帮助测试"""

    def test_help_flag(self):
        """--help 输出帮助信息"""
        result = _run_schedule_cmd(["--help"])
        assert result.returncode == 0
        assert "list" in result.stdout
        assert "inspect" in result.stdout
        assert "delete" in result.stdout
        assert "disable" in result.stdout
        assert "enable" in result.stdout
        assert "archive" in result.stdout

    def test_help_with_subcommand(self):
        """子命令 --help 输出"""
        result = _run_schedule_cmd(["list", "--help"])
        assert result.returncode == 0

    def test_no_subcommand(self):
        """无子命令报错"""
        result = _run_schedule_cmd([])
        assert result.returncode != 0
        assert "缺少子命令" in result.stderr

    def test_invalid_subcommand(self):
        """无效子命令报错"""
        result = _run_schedule_cmd(["unknown"])
        assert result.returncode != 0
        assert "未知子命令" in result.stderr

    def test_missing_task_id(self):
        """缺少 task_id 报错"""
        result = _run_schedule_cmd(["inspect"])
        assert result.returncode != 0
        assert "缺少 task_id" in result.stderr


class TestScheduleCLIEdgeCases:
    """CLI 边界条件测试"""

    def test_long_task_name_display(self):
        """长任务名应正确显示"""
        long_name = "这是一个非常长的任务名称用来测试截断显示功能"
        task_id = "task-cli-long-name"
        _write_task_json(task_id, long_name)
        try:
            result = _run_schedule_cmd(["list"])
            assert result.returncode == 0
            assert task_id in result.stdout
        finally:
            _delete_task_json(task_id)

    def test_special_chars_in_instruction(self):
        """指令中包含特殊字符"""
        task_id = "task-cli-special"
        _write_task_json(task_id, "特殊字符任务",
                         instruction="测试 '引号' 和 \"双引号\" 和 中文")
        try:
            result = _run_schedule_cmd(["inspect", task_id])
            assert result.returncode == 0
            assert "特殊字符任务" in result.stdout
        finally:
            _delete_task_json(task_id)

    def test_schedule_formats(self):
        """验证各种调度格式的显示"""
        task_id = "task-cli-formats"
        cron_task = {"id": task_id + "-cron", "name": "Cron任务",
                     "schedule": "0 9 * * 1-5", "instruction": "工作日9点",
                     "missed_policy": "run_once",
                     "created_at": "2026-05-11T00:00:00Z",
                     "updated_at": "2026-05-11T00:00:00Z"}
        once_task = {"id": task_id + "-once", "name": "一次性任务",
                     "schedule": "2026-06-01T09:00:00Z", "instruction": "6月1日执行",
                     "missed_policy": "skip",
                     "created_at": "2026-05-11T00:00:00Z",
                     "updated_at": "2026-05-11T00:00:00Z"}
        interval_task = {"id": task_id + "-interval", "name": "间隔任务",
                         "schedule": "30m", "instruction": "每30分钟",
                         "missed_policy": "run_once",
                         "created_at": "2026-05-11T00:00:00Z",
                         "updated_at": "2026-05-11T00:00:00Z"}

        active_dir = os.path.join(TEST_HOME, "schedules", "active")
        os.makedirs(active_dir, exist_ok=True)

        for task in [cron_task, once_task, interval_task]:
            filepath = os.path.join(active_dir, f"{task['id']}.json")
            with open(filepath, "w") as f:
                json.dump(task, f, ensure_ascii=False)

        try:
            result = _run_schedule_cmd(["list"])
            assert result.returncode == 0
            assert "0 9 * * 1-5" in result.stdout
            assert "2026-06-01T09:00:00Z" in result.stdout
            assert "30m" in result.stdout
        finally:
            for task in [cron_task, once_task, interval_task]:
                _delete_task_json(task["id"])
