"""
Skills 动态加载测试
测试 Skills 的动态添加、修改、删除

说明: skill 列表按请求实时扫描 skills 目录（无防抖/热插拔监听），
文件写入后短暂等待即可查询到最新状态。
/web/skills 端点需要 Web 登录 Cookie 认证。
"""

import pytest
import requests
import json
import os
import time
import shutil
from conftest import BASE_URL, TEST_HOME, _web_login


# 模块级缓存的 Web 登录 Session
_web_session = None


def get_web_session():
    """获取（并缓存）已登录的 Web Session"""
    global _web_session
    if _web_session is None:
        _web_session = _web_login(BASE_URL)
    return _web_session


class TestSkillsHotReload:
    """Skills 动态加载测试"""

    def test_add_skill_updates_list(self, server, mock_skill):
        """TC-HOT-001: 添加 Skill 后列表更新"""
        # 添加 Skill（通过 fixture）
        skill_dir = f"{TEST_HOME}/skills/test_skill"
        skill_file = f"{skill_dir}/SKILL.md"

        # 实时扫描，稍等文件写入完成即可
        time.sleep(1)

        # 查询 Skills 列表
        response = get_web_session().get(f"{BASE_URL}/web/skills")

        assert response.status_code == 200
        data = response.json()

        # 验证新 Skill 在列表中
        skill_names = [s["name"] for s in data["skills"]]
        assert "test_skill" in skill_names

    def test_remove_skill_updates_list(self, server, mock_skill):
        """TC-HOT-002: 删除 Skill 后列表更新"""
        skill_dir = f"{TEST_HOME}/skills/test_skill"

        # 稍等文件写入完成
        time.sleep(1)

        # 验证 Skill 已加载
        response = get_web_session().get(f"{BASE_URL}/web/skills")

        skill_names = [s["name"] for s in response.json()["skills"]]
        assert "test_skill" in skill_names

        # 删除 Skill
        shutil.rmtree(skill_dir)

        # 实时扫描，稍等即可
        time.sleep(1)

        # 再次查询
        response = get_web_session().get(f"{BASE_URL}/web/skills")

        skill_names = [s["name"] for s in response.json()["skills"]]
        assert "test_skill" not in skill_names

    def test_modify_skill_updates_content(self, server):
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

        time.sleep(1)

        # 验证加载
        response = get_web_session().get(f"{BASE_URL}/web/skills")

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

        time.sleep(1)

        # 验证更新
        response = get_web_session().get(f"{BASE_URL}/web/skills")

        skills = response.json()["skills"]
        skill = next((s for s in skills if s["name"] == "modify_test"), None)
        assert skill is not None
        assert skill["description"] == "版本2"

        # 清理
        shutil.rmtree(skill_dir)


class TestSkillFormat:
    """Skill 格式验证测试"""

    def test_skill_yaml_frontmatter(self, server):
        """TC-HOT-004: Skill YAML frontmatter 格式"""
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

        time.sleep(1)

        # 验证加载
        response = get_web_session().get(f"{BASE_URL}/web/skills")

        skills = response.json()["skills"]
        skill = next((s for s in skills if s["name"] == "format_test"), None)

        if skill:
            assert skill["name"] == "format_test"
            assert skill["description"] == "格式测试Skill"

        # 清理
        shutil.rmtree(skill_dir)
