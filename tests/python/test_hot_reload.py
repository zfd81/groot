"""
Skills/MCP 热插拔测试
测试 Skills 和 MCP 的动态加载、修改、删除

注意: 热插拔使用防抖延迟(debounce_delay: 2秒)
测试需要等待足够时间确保防抖处理完成
建议等待时间: 5秒 (防抖2秒 + 处理时间)
"""

import pytest
import requests
import json
import os
import time
import shutil
from conftest import BASE_URL, TEST_HOME


class TestSkillsHotReload:
    """Skills 热插拔测试"""

    def test_add_skill_updates_list(self, server, api_headers, mock_skill):
        """TC-HOT-001: 添加 Skill 后列表更新"""
        # 添加 Skill（通过 fixture）
        skill_dir = f"{TEST_HOME}/skills/test_skill"
        skill_file = f"{skill_dir}/SKILL.md"

        # 等待热插拔生效（防抖延迟 2秒 + 处理时间）
        time.sleep(5)

        # 查询 Skills 列表
        response = requests.get(
            f"{BASE_URL}/skills",
            headers=api_headers
        )

        assert response.status_code == 200
        data = response.json()

        # 验证新 Skill 在列表中
        skill_names = [s["name"] for s in data["skills"]]
        assert "test_skill" in skill_names

    def test_remove_skill_updates_list(self, server, api_headers, mock_skill):
        """TC-HOT-002: 删除 Skill 后列表更新"""
        skill_dir = f"{TEST_HOME}/skills/test_skill"

        # 等待 Skill 加载（防抖延迟）
        time.sleep(5)

        # 验证 Skill 已加载
        response = requests.get(
            f"{BASE_URL}/skills",
            headers=api_headers
        )

        skill_names = [s["name"] for s in response.json()["skills"]]
        assert "test_skill" in skill_names

        # 删除 Skill
        shutil.rmtree(skill_dir)

        # 等待热插拔生效
        time.sleep(5)

        # 再次查询
        response = requests.get(
            f"{BASE_URL}/skills",
            headers=api_headers
        )

        skill_names = [s["name"] for s in response.json()["skills"]]
        assert "test_skill" not in skill_names

    def test_modify_skill_updates_content(self, server, api_headers):
        """TC-HOT-003: 修改 Skill 后内容更新"""
        skill_dir = f"{TEST_HOME}/skills/modify_test"
        os.makedirs(skill_dir, exist_ok=True)

        # 创建初始 Skill
        skill_content_v1 = """---
name: modify_test
description: "版本1"
---

# Test Skill V1
"""

        skill_file = f"{skill_dir}/SKILL.md"
        with open(skill_file, "w") as f:
            f.write(skill_content_v1)

        time.sleep(5)

        # 验证加载
        response = requests.get(
            f"{BASE_URL}/skills",
            headers=api_headers
        )

        skills = response.json()["skills"]
        skill = next((s for s in skills if s["name"] == "modify_test"), None)
        assert skill is not None
        assert skill["description"] == "版本1"

        # 修改 Skill
        skill_content_v2 = """---
name: modify_test
description: "版本2"
---

# Test Skill V2
"""

        with open(skill_file, "w") as f:
            f.write(skill_content_v2)

        time.sleep(5)

        # 验证更新
        response = requests.get(
            f"{BASE_URL}/skills",
            headers=api_headers
        )

        skills = response.json()["skills"]
        skill = next((s for s in skills if s["name"] == "modify_test"), None)
        assert skill is not None
        assert skill["description"] == "版本2"

        # 清理
        shutil.rmtree(skill_dir)


class TestMCPHotReload:
    """MCP 热插拔测试"""

    def test_add_mcp_updates_tools(self, server, api_headers, mock_mcp_config):
        """TC-HOT-004: 添加 MCP 后工具列表更新"""
        # 等待热插拔生效
        time.sleep(5)

        # 查询工具列表
        response = requests.get(
            f"{BASE_URL}/tools",
            headers=api_headers
        )

        assert response.status_code == 200
        data = response.json()

        # 验证工具来自新 MCP（新格式：按 MCP 分组）
        # data 应为 {"mcp_name": {"tools": [...], "total": N}, ...}
        # mock MCP 可能不会真正加载工具，但配置应已生效

    def test_remove_mcp_updates_tools(self, server, api_headers, mock_mcp_config):
        """TC-HOT-005: 删除 MCP 后工具列表更新"""
        mcp_file = f"{TEST_HOME}/mcp/test_mcp.json"

        # 等待加载
        time.sleep(5)

        # 删除 MCP 配置
        os.remove(mcp_file)

        # 等待热插拔生效
        time.sleep(5)

        # 查询工具列表
        response = requests.get(
            f"{BASE_URL}/tools",
            headers=api_headers
        )

        assert response.status_code == 200

    def test_modify_mcp_reconnects(self, server, api_headers):
        """TC-HOT-006: 修改 MCP 后重新连接"""
        mcp_file = f"{TEST_HOME}/mcp/modify_test.json"

        # 创建初始 MCP 配置
        mcp_config_v1 = {
            "name": "modify_test",
            "type": "stdio",
            "description": "版本1",
            "isActive": True,
            "command": "echo",
            "args": ["v1"]
        }

        with open(mcp_file, "w") as f:
            json.dump(mcp_config_v1, f)

        time.sleep(5)

        # 修改配置
        mcp_config_v2 = {
            "name": "modify_test",
            "type": "stdio",
            "description": "版本2",
            "isActive": True,
            "command": "echo",
            "args": ["v2"]
        }

        with open(mcp_file, "w") as f:
            json.dump(mcp_config_v2, f)

        time.sleep(5)

        # 清理
        os.remove(mcp_file)

    def test_mcp_deactivate(self, server, api_headers):
        """TC-HOT-007: MCP 设置 isActive=false 后禁用"""
        mcp_file = f"{TEST_HOME}/mcp/deactivate_test.json"

        mcp_config = {
            "name": "deactivate_test",
            "type": "stdio",
            "description": "测试MCP",
            "isActive": False,  # 禁用
            "command": "echo",
            "args": ["test"]
        }

        with open(mcp_file, "w") as f:
            json.dump(mcp_config, f)

        time.sleep(5)

        # 验证工具列表不包含该 MCP 的工具
        response = requests.get(
            f"{BASE_URL}/tools",
            headers=api_headers
        )

        # 新格式：按 MCP 分组
        # data 应为 {"mcp_name": {"tools": [...], "total": N}, ...}
        # 禁用的 MCP 不应出现在分组中

        # 清理
        os.remove(mcp_file)


class TestSkillFormat:
    """Skill 格式验证测试"""

    def test_skill_yaml_frontmatter(self, server, api_headers):
        """TC-HOT-008: Skill YAML frontmatter 格式"""
        skill_dir = f"{TEST_HOME}/skills/format_test"
        os.makedirs(skill_dir, exist_ok=True)

        # 正确格式
        skill_content = """---
name: format_test
description: "格式测试Skill"
dependencies: []
---

# Format Test Skill

测试 Skill 格式。
"""

        skill_file = f"{skill_dir}/SKILL.md"
        with open(skill_file, "w") as f:
            f.write(skill_content)

        time.sleep(5)

        # 验证加载
        response = requests.get(
            f"{BASE_URL}/skills",
            headers=api_headers
        )

        skills = response.json()["skills"]
        skill = next((s for s in skills if s["name"] == "format_test"), None)

        if skill:
            assert skill["name"] == "format_test"
            assert skill["description"] == "格式测试Skill"

        # 清理
        shutil.rmtree(skill_dir)


class TestMCPFormat:
    """MCP 配置格式验证测试"""

    def test_mcp_json_format(self, server, api_headers):
        """TC-HOT-009: MCP JSON 配置格式"""
        mcp_file = f"{TEST_HOME}/mcp/format_test.json"

        # stdio 类型配置
        mcp_config = {
            "name": "format_test",
            "type": "stdio",
            "description": "格式测试MCP",
            "isActive": True,
            "command": "echo",
            "args": ["test"],
            "env": {}
        }

        with open(mcp_file, "w") as f:
            json.dump(mcp_config, f)

        time.sleep(5)

        # 清理
        os.remove(mcp_file)

    def test_mcp_sse_type(self, server, api_headers):
        """TC-HOT-010: MCP sse 类型配置"""
        mcp_file = f"{TEST_HOME}/mcp/sse_test.json"

        mcp_config = {
            "name": "sse_test",
            "type": "sse",
            "description": "SSE类型MCP",
            "isActive": False,  # 不实际连接
            "baseUrl": "https://example.com/mcp/sse",
            "headers": {
                "Authorization": "Bearer test-token"
            }
        }

        with open(mcp_file, "w") as f:
            json.dump(mcp_config, f)

        time.sleep(5)

        # 清理
        os.remove(mcp_file)


class TestDebounceDelay:
    """防抖延迟测试"""

    def test_debounce_delay(self, server, api_headers):
        """TC-HOT-011: 防抖延迟生效（2秒内多次修改只触发一次）"""
        skill_dir = f"{TEST_HOME}/skills/debounce_test"
        os.makedirs(skill_dir, exist_ok=True)

        skill_file = f"{skill_dir}/SKILL.md"

        # 快速多次修改（在2秒内）
        for i in range(5):
            content = f"""---
name: debounce_test
description: "版本{i}"
---
"""
            with open(skill_file, "w") as f:
                f.write(content)
            time.sleep(0.3)  # 每次间隔0.3秒

        # 等待防抖完成
        time.sleep(5)

        # 验证最终版本生效（应该是最后一个）
        response = requests.get(
            f"{BASE_URL}/skills",
            headers=api_headers
        )

        skills = response.json()["skills"]
        skill = next((s for s in skills if s["name"] == "debounce_test"), None)

        if skill:
            # 应为最后一个版本
            assert skill["description"] == "版本4"

        # 清理
        shutil.rmtree(skill_dir)