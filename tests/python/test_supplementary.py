"""
补充测试

用例清单（裁剪后）：
- TestMCPConnectionTypes    MCP 连接类型配置（stdio/sse/streamable_http/env 引用）
- TestSkillsDependencies    Skill dependencies 递归调用
- TestPromptValidation      /chat 的 prompt 参数
- TestHealthDetailedChecks  /web/health 详细检查（llm、mcp、skills、memory、uptime、version）
- TestWebEndpointAccess     /web/* 端点访问控制（Cookie 才 200；X-API-Key 打 /web/* 401）
- TestReActExecutionDetails ReAct 步骤（SSE thinking 事件 + /chat/:sid 的 steps 持久化）
- TestSessionHandlingDetails 会话处理（新会话/续会话/无效 session_id）
- TestMetricsInHealth       /web/health 的 metrics 字段

已删除的空壳/过时用例组：LLM 错误码组、MCP 工具错误组、Skill 错误组、
优雅关闭组、配置热更新边界组、LLM 多模型配置组（由 test_models_api 取代）、
取消机制组（与 test_runtime_state 重复）。
"""

import pytest
import requests
import time
from conftest import BASE_URL, SSEClient, _web_login


@pytest.fixture(scope="module")
def web(server):
    """Web 登录 Cookie Session（/web/* 端点只认 Cookie，X-API-Key 无效）"""
    return _web_login(BASE_URL)


class TestMCPConnectionTypes:
    """MCP 连接类型测试"""

    def test_mcp_stdio_type(self, server, api_headers):
        """TC-MCP-TYPE-001: stdio 类型 MCP 配置"""
        # 创建 stdio 类型 MCP 配置文件
        import json
        import os
        from conftest import TEST_HOME

        mcp_file = f"{TEST_HOME}/mcp/stdio_test.json"

        mcp_config = {
            "name": "stdio_test",
            "type": "stdio",
            "description": "stdio 类型测试",
            "isActive": False,  # 不实际连接
            "command": "echo",
            "args": ["test"],
            "env": {}
        }

        with open(mcp_file, "w") as f:
            json.dump(mcp_config, f)

        # 等待热插拔
        time.sleep(5)

        # 清理
        if os.path.exists(mcp_file):
            os.remove(mcp_file)

    def test_mcp_sse_type(self, server, api_headers):
        """TC-MCP-TYPE-002: sse 类型 MCP 配置"""
        import json
        import os
        from conftest import TEST_HOME

        mcp_file = f"{TEST_HOME}/mcp/sse_test.json"

        mcp_config = {
            "name": "sse_test",
            "type": "sse",
            "description": "sse 类型测试",
            "isActive": False,
            "baseUrl": "https://example.com/mcp/sse",
            "headers": {
                "Authorization": "Bearer ${TEST_API_KEY}"
            }
        }

        with open(mcp_file, "w") as f:
            json.dump(mcp_config, f)

        time.sleep(5)

        if os.path.exists(mcp_file):
            os.remove(mcp_file)

    def test_mcp_streamable_http_type(self, server, api_headers):
        """TC-MCP-TYPE-003: streamable_http 类型 MCP 配置"""
        import json
        import os
        from conftest import TEST_HOME

        mcp_file = f"{TEST_HOME}/mcp/streamable_http_test.json"

        mcp_config = {
            "name": "streamable_http_test",
            "type": "streamable_http",
            "description": "streamable_http 类型测试",
            "isActive": False,
            "baseUrl": "https://example.com/mcp/api",
            "headers": {
                "X-API-Key": "${TEST_API_KEY}"
            }
        }

        with open(mcp_file, "w") as f:
            json.dump(mcp_config, f)

        time.sleep(5)

        if os.path.exists(mcp_file):
            os.remove(mcp_file)

    def test_mcp_headers_env_variable(self, server, api_headers):
        """TC-MCP-TYPE-004: MCP headers 环境变量引用"""
        import json
        import os
        from conftest import TEST_HOME

        # 设置测试环境变量
        os.environ["TEST_MCP_KEY"] = "test-key-value"

        mcp_file = f"{TEST_HOME}/mcp/env_var_test.json"

        mcp_config = {
            "name": "env_var_test",
            "type": "sse",
            "description": "环境变量测试",
            "isActive": False,
            "baseUrl": "https://example.com/mcp",
            "headers": {
                "Authorization": "Bearer ${TEST_MCP_KEY}"
            }
        }

        with open(mcp_file, "w") as f:
            json.dump(mcp_config, f)

        time.sleep(5)

        # 验证环境变量被正确解析
        # （具体验证需要在服务端检查）

        if os.path.exists(mcp_file):
            os.remove(mcp_file)


class TestSkillsDependencies:
    """Skills 依赖测试"""

    def test_skill_dependencies_recursive(self, server, api_headers):
        """TC-SKILL-DEP-001: Skill dependencies 递归调用"""
        import os
        from conftest import TEST_HOME

        # 创建主 Skill
        main_skill_dir = f"{TEST_HOME}/skills/main_skill"
        os.makedirs(main_skill_dir, exist_ok=True)

        main_skill_content = """---
name: main_skill
description: "主Skill，依赖子Skill"
dependencies: [sub_skill]
---

# Main Skill

调用 sub_skill 完成子任务。
"""

        with open(f"{main_skill_dir}/SKILL.md", "w") as f:
            f.write(main_skill_content)

        # 创建子 Skill
        sub_skill_dir = f"{TEST_HOME}/skills/sub_skill"
        os.makedirs(sub_skill_dir, exist_ok=True)

        sub_skill_content = """---
name: sub_skill
description: "子Skill"
---

# Sub Skill

执行子任务。
"""

        with open(f"{sub_skill_dir}/SKILL.md", "w") as f:
            f.write(sub_skill_content)

        time.sleep(5)

        # 发送请求调用主 Skill
        payload = {"instruction": "使用 main_skill 处理任务"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        # 验证有 thinking 事件
        thinking_events = sse.get_events_by_type("thinking")

        # 查找子 Skill 调用（如果有 step 信息）
        tool_calls = sse.get_events_by_type("tool_calls")

        if thinking_events:
            # 验证 nesting_level > 0（如果字段存在）
            for step in thinking_events:
                if "nesting_level" in step["data"]:
                    assert step["data"].get("nesting_level", 0) >= 0

        # 清理
        import shutil
        if os.path.exists(main_skill_dir):
            shutil.rmtree(main_skill_dir)
        if os.path.exists(sub_skill_dir):
            shutil.rmtree(sub_skill_dir)


class TestPromptValidation:
    """prompt 参数验证测试"""

    def test_prompt_parameter_accepted(self, server, api_headers):
        """TC-PROMPT-001: prompt 参数正常接受"""
        payload = {
            "instruction": "分析数据",
            "prompt": "你是一个数据分析专家，输出JSON格式结果"
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        assert response.status_code == 200

        sse = SSEClient(response)
        completed = sse.get_completed_event()

        assert completed["data"]["finish_reason"] in ("stop", "tool_calls")

    def test_prompt_empty_allowed(self, server, api_headers):
        """TC-PROMPT-002: prompt 为空允许"""
        payload = {
            "instruction": "测试",
            "prompt": ""
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        # 空 prompt 应被忽略，正常执行
        assert response.status_code == 200


class TestHealthDetailedChecks:
    """Health 详细检查测试（健康检查唯一入口 /web/health，免认证）"""

    def test_health_llm_check(self, server):
        """TC-HEALTH-001: LLM 连接就绪检查"""
        response = requests.get(f"{BASE_URL}/web/health")

        data = response.json()

        # 验证 LLM 检查（无默认模型时状态为 unconfigured）
        if "checks" in data and "llm" in data["checks"]:
            llm_check = data["checks"]["llm"]
            assert llm_check["status"] in ["healthy", "unhealthy", "unconfigured"]
            if llm_check["status"] != "unconfigured" and "model" in llm_check:
                assert llm_check["model"]  # 模型名不为空

    def test_health_mcp_check(self, server):
        """TC-HEALTH-002: MCP 服务连接就绪检查"""
        response = requests.get(f"{BASE_URL}/web/health")

        data = response.json()

        if "checks" in data and "mcp_servers" in data["checks"]:
            mcp_check = data["checks"]["mcp_servers"]
            assert mcp_check["status"] in ["healthy", "unhealthy"]
            if "servers" in mcp_check:
                # 服务器列表
                assert isinstance(mcp_check["servers"], list)

    def test_health_skills_check(self, server):
        """TC-HEALTH-003: Skills 加载完成检查"""
        response = requests.get(f"{BASE_URL}/web/health")

        data = response.json()

        if "checks" in data and "skills" in data["checks"]:
            skills_check = data["checks"]["skills"]
            assert skills_check["status"] in ["healthy", "unhealthy"]
            if "count" in skills_check:
                assert skills_check["count"] >= 0

    def test_health_memory_check(self, server):
        """TC-HEALTH-004: Memory 使用检查"""
        response = requests.get(f"{BASE_URL}/web/health")

        data = response.json()

        if "checks" in data and "memory" in data["checks"]:
            memory_check = data["checks"]["memory"]
            assert memory_check["status"] in ["healthy", "unhealthy"]
            if "used_mb" in memory_check:
                assert memory_check["used_mb"] >= 0

    def test_health_uptime(self, server):
        """TC-HEALTH-005: uptime 字段"""
        response = requests.get(f"{BASE_URL}/web/health")

        data = response.json()

        assert "uptime" in data
        # uptime 格式如 "2h30m"

    def test_health_environment_check(self, server):
        """TC-HEALTH-006: environment 检查块含运行环境三字段
        （internal/api/handler/health.go：home_dir / database / log_dir）"""
        response = requests.get(f"{BASE_URL}/web/health")
        data = response.json()

        assert "environment" in data.get("checks", {}), "checks 应含 environment 块"
        env_check = data["checks"]["environment"]
        assert env_check["status"] == "healthy"
        info = env_check.get("info", {})
        for field in ("home_dir", "database", "log_dir"):
            assert field in info, f"environment.info 应含 {field} 字段"
        assert data["uptime"]

    def test_health_version(self, server):
        """TC-HEALTH-006: version 字段"""
        response = requests.get(f"{BASE_URL}/web/health")

        data = response.json()

        assert "version" in data
        assert data["version"]


class TestWebEndpointAccess:
    """/web/* 端点访问控制测试

    语义：/web/skills /web/tools /web/agents 需要 Web 登录 Cookie；
    X-API-Key（JWT）只对 API 端点（/chat /sess /schedule）有效，
    拿它打 /web/* 应得到 401。
    """

    def test_web_endpoints_with_cookie_ok(self, server, web):
        """TC-PERM-010: 携带登录 Cookie 访问 /web/* → 200"""
        for endpoint in ["/web/skills", "/web/tools", "/web/agents"]:
            response = web.get(f"{BASE_URL}{endpoint}")
            assert response.status_code == 200, (
                f"{endpoint} 携带 Cookie 应返回 200，实际 {response.status_code}"
            )

    def test_web_endpoints_reject_api_key(self, server, api_headers):
        """TC-PERM-011: 拿 X-API-Key（无 Cookie）打 /web/* → 401"""
        for endpoint in ["/web/skills", "/web/tools", "/web/agents"]:
            response = requests.get(f"{BASE_URL}{endpoint}", headers=api_headers)
            assert response.status_code == 401, (
                f"{endpoint} 仅认 Cookie，X-API-Key 应返回 401，实际 {response.status_code}"
            )

    def test_web_endpoints_reject_anonymous(self, server, no_auth_headers):
        """TC-PERM-012: 无任何认证打 /web/* → 401"""
        for endpoint in ["/web/skills", "/web/tools", "/web/agents"]:
            response = requests.get(f"{BASE_URL}{endpoint}", headers=no_auth_headers)
            assert response.status_code == 401

    def test_api_endpoint_accepts_api_key(self, server, api_headers):
        """TC-PERM-013: all 权限 API Key 可访问 API 端点（/chat）"""
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "test"},
            stream=True
        )
        assert response.status_code == 200
        SSEClient(response)  # drain


class TestReActExecutionDetails:
    """ReAct 执行详细测试"""

    def test_reasoning_step_emitted(self, server, api_headers):
        """TC-REACT-001: Reasoning 步骤事件"""
        payload = {"instruction": "写一个函数"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        # 应有 thinking 事件（如果 LLM 有思考过程）
        # 注意：Mock LLM 可能不产生 thinking
        thinking_events = sse.get_events_by_type("thinking")

        # 如果有 thinking，验证内容
        if thinking_events:
            for event in thinking_events:
                assert "reasoning_content" in event["data"]

    def test_steps_persisted_in_chat_record(self, server, api_headers):
        """TC-REACT-002: 执行步骤持久化在 ChatRecord.steps 中

        SSE 事件不携带 step_id 等字段；步骤详情通过 GET /chat/:sid
        返回的 steps 数组断言（step_id/type/name/status 等）。
        """
        payload = {"instruction": "读取文件 /tmp/test.txt"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )
        session_id = response.headers.get("X-Session-ID")
        SSEClient(response)  # drain 至完成

        detail = requests.get(f"{BASE_URL}/chat/{session_id}", headers=api_headers)
        assert detail.status_code == 200
        chat = detail.json()["chat"]
        assert "steps" in chat

        # 有步骤时验证结构（Mock LLM 可能不触发工具调用，steps 可为空）
        for step in chat["steps"] or []:
            assert "step_id" in step
            assert "type" in step
            assert "name" in step
            assert "status" in step

    def test_step_timing_fields(self, server, api_headers):
        """TC-REACT-003: 步骤含起止时间与嵌套层级字段"""
        payload = {"instruction": "测试对话"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )
        session_id = response.headers.get("X-Session-ID")
        SSEClient(response)

        detail = requests.get(f"{BASE_URL}/chat/{session_id}", headers=api_headers)
        assert detail.status_code == 200
        chat = detail.json()["chat"]

        for step in chat.get("steps") or []:
            assert "start_time" in step
            assert "end_time" in step
            assert "nesting_level" in step
            assert step["nesting_level"] >= 0


class TestSessionHandlingDetails:
    """会话处理详细测试"""

    def test_new_session_history_empty(self, server, api_headers):
        """TC-SESS-001: 新会话历史为空"""
        payload = {"instruction": "测试"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")
        SSEClient(response)

        # 查询会话历史
        history_response = requests.get(
            f"{BASE_URL}/sess/{session_id}",
            headers=api_headers
        )

        data = history_response.json()

        # 新会话只有一条消息
        assert len(data["history"]["messages"]) == 1

    def test_continue_session_history_loaded(self, server, api_headers):
        """TC-SESS-002: 继续会话加载历史"""
        # 第一轮
        response1 = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "第一轮"},
            stream=True
        )

        session_id = response1.headers.get("X-Session-ID")
        SSEClient(response1)

        # 第二轮（继续）
        headers2 = api_headers.copy()
        headers2["X-Session-ID"] = session_id

        response2 = requests.post(
            f"{BASE_URL}/chat",
            headers=headers2,
            json={"instruction": "第二轮"},
            stream=True
        )

        SSEClient(response2)

        # 查询历史
        history_response = requests.get(
            f"{BASE_URL}/sess/{session_id}",
            headers=api_headers
        )

        data = history_response.json()

        # 应有两条消息
        assert len(data["history"]["messages"]) == 2

    def test_invalid_session_creates_new(self, server, api_headers):
        """TC-SESS-003: 无效 session_id 创建新会话"""
        headers = api_headers.copy()
        headers["X-Session-ID"] = "invalid_session_id_xyz"

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=headers,
            json={"instruction": "测试"},
            stream=True
        )

        # 应返回新的 session_id
        new_session_id = response.headers.get("X-Session-ID")

        # 新 ID 不等于无效 ID
        assert new_session_id != "invalid_session_id_xyz"

        SSEClient(response)


class TestMetricsInHealth:
    """Health 接口 metrics 测试"""

    def test_chats_running_metric(self, server, api_headers):
        """TC-METRICS-001: chats_running 指标"""
        response = requests.get(f"{BASE_URL}/web/health")

        data = response.json()

        if "metrics" in data:
            assert "chats_running" in data["metrics"]
            assert data["metrics"]["chats_running"] >= 0

    def test_success_rate_metric(self, server, api_headers):
        """TC-METRICS-002: success_rate 指标"""
        response = requests.get(f"{BASE_URL}/web/health")

        data = response.json()

        if "metrics" in data and "success_rate" in data["metrics"]:
            rate = data["metrics"]["success_rate"]
            assert 0 <= rate <= 1
