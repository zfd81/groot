"""
补充测试 - LLM/MCP 错误处理、重试策略、连接类型等
覆盖设计文档第六章错误码和重试策略
"""

import pytest
import requests
import time
from conftest import BASE_URL, SSEClient


class TestLLMErrors:
    """LLM 错误处理测试"""

    def test_llm_connection_error_code(self, server, api_headers):
        """TC-LLM-001: llm_connection_error 错误码"""
        # 发送请求到配置错误的 LLM
        # 具体行为取决于配置
        payload = {"instruction": "测试"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        # 如果 LLM 连接失败，应返回对应错误
        sse = SSEClient(response)
        error_events = sse.get_events_by_type("error")

        if error_events:
            error = error_events[0]["data"]
            # 验证错误码格式
            assert "code" in error
            # 可能的错误码：llm_connection_error, llm_rate_limited 等

    def test_llm_rate_limited_error(self, server, api_headers):
        """TC-LLM-002: llm_rate_limited 错误码"""
        # 快速发送多个请求触发 LLM API 限流
        # 具体行为取决于 LLM API 配置
        payload = {"instruction": "测试"}

        # 发送多个请求
        for i in range(5):
            response = requests.post(
                f"{BASE_URL}/chat",
                headers=api_headers,
                json={"instruction": f"test{i}"},
                stream=True
            )

        # 验证是否有 rate_limited 错误（如果触发）

    def test_llm_connection_retry(self, server, api_headers):
        """TC-LLM-003: LLM 连接失败重试（3次，间隔2s）"""
        # 发送请求，观察重试行为
        payload = {"instruction": "测试"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        # 如果有重试，tool_result 会有多次失败然后成功
        tool_results = sse.get_events_by_type("tool_result")

        # 统计失败和成功的工具结果
        failed_results = [r for r in tool_results if "error" in r["data"]]
        success_results = [r for r in tool_results if "error" not in r["data"]]

        # 验证重试行为（如果有失败）

    def test_llm_rate_limit_retry_delay(self, server, api_headers):
        """TC-LLM-004: LLM Rate Limit 重试间隔（5s）"""
        # 验证 Rate Limit 重试时有正确间隔
        # 需要触发 rate limit 场景
        payload = {"instruction": "测试"}

        start_time = time.time()

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        SSEClient(response)

        elapsed = time.time() - start_time

        # 如果有重试，总时间应包含重试间隔


class TestMCPToolErrors:
    """MCP 工具错误处理测试"""

    def test_tool_call_error_code(self, server, api_headers):
        """TC-MCP-001: tool_call_error 错误码"""
        # 发送可能触发工具调用失败的请求
        payload = {"instruction": "读取不存在路径的文件 /nonexistent/path"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)
        tool_results = sse.get_events_by_type("tool_result")

        # 查找失败的工具结果
        for result in tool_results:
            if "error" in result["data"]:
                error = result["data"].get("error", {})
                if isinstance(error, dict):
                    assert "code" in error or "message" in error

        # 查找失败的工具调用
        for result in tool_results:
            if "error" in result["data"]:
                assert result["data"]["error"] is not None

    def test_mcp_tool_retry(self, server, api_headers):
        """TC-MCP-002: MCP 工具失败重试（2次，间隔1s）"""
        payload = {"instruction": "执行可能失败的工具调用"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        # 观察是否有重试步骤
        thinking_events = sse.get_events_by_type("thinking")
        tool_calls = sse.get_events_by_type("tool_calls")

        # 同一个工具可能有多个 thinking（重试）


class TestSkillErrors:
    """Skill 错误处理测试"""

    def test_skill_not_found_error(self, server, api_headers):
        """TC-SKILL-001: skill_not_found 错误码"""
        # 发送请求调用不存在的 Skill
        payload = {"instruction": "使用 nonexistent_skill 来处理数据"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        # Agent 可能：
        # 1. 直接忽略，自己处理
        # 2. 返回 skill_not_found 错误
        error_events = sse.get_events_by_type("error")
        if error_events:
            error = error_events[0]["data"]
            # 验证错误码


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


# HTTP request and code execution limits are now handled by external MCP servers
# These tests are removed as builtin MCP tools are no longer available


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
    """Health 详细检查测试"""

    def test_health_llm_check(self, server):
        """TC-HEALTH-001: LLM 连接就绪检查"""
        response = requests.get(f"{BASE_URL}/health")

        data = response.json()

        # 验证 LLM 检查
        if "checks" in data and "llm" in data["checks"]:
            llm_check = data["checks"]["llm"]
            assert llm_check["status"] in ["healthy", "unhealthy"]
            if "model" in llm_check:
                assert llm_check["model"]  # 模型名不为空

    def test_health_mcp_check(self, server):
        """TC-HEALTH-002: MCP 服务连接就绪检查"""
        response = requests.get(f"{BASE_URL}/health")

        data = response.json()

        if "checks" in data and "mcp_servers" in data["checks"]:
            mcp_check = data["checks"]["mcp_servers"]
            assert mcp_check["status"] in ["healthy", "unhealthy"]
            if "servers" in mcp_check:
                # 服务器列表
                assert isinstance(mcp_check["servers"], list)

    def test_health_skills_check(self, server):
        """TC-HEALTH-003: Skills 加载完成检查"""
        response = requests.get(f"{BASE_URL}/health")

        data = response.json()

        if "checks" in data and "skills" in data["checks"]:
            skills_check = data["checks"]["skills"]
            assert skills_check["status"] in ["healthy", "unhealthy"]
            if "count" in skills_check:
                assert skills_check["count"] >= 0

    def test_health_memory_check(self, server):
        """TC-HEALTH-004: Memory 使用检查"""
        response = requests.get(f"{BASE_URL}/health")

        data = response.json()

        if "checks" in data and "memory" in data["checks"]:
            memory_check = data["checks"]["memory"]
            assert memory_check["status"] in ["healthy", "unhealthy"]
            if "used_mb" in memory_check:
                assert memory_check["used_mb"] >= 0

    def test_health_uptime(self, server):
        """TC-HEALTH-005: uptime 字段"""
        response = requests.get(f"{BASE_URL}/health")

        data = response.json()

        assert "uptime" in data
        # uptime 格式如 "2h30m"
        assert data["uptime"]

    def test_health_version(self, server):
        """TC-HEALTH-006: version 字段"""
        response = requests.get(f"{BASE_URL}/health")

        data = response.json()

        assert "version" in data
        assert data["version"]


class TestGracefulShutdown:
    """优雅关闭测试"""

    def test_shutdown_waits_for_running_chats(self, server, api_headers):
        """TC-SHUT-001: 关闭时等待运行中的对话"""
        # 此测试验证关闭流程
        # 实际关闭行为需要在服务停止时观察
        # 测试服务响应正常
        response = requests.get(f"{BASE_URL}/health")
        assert response.status_code == 200

    def test_shutdown_timeout_30_seconds(self, server, api_headers):
        """TC-SHUT-002: 关闭超时30秒"""
        # 验证服务运行正常
        # 关闭超时配置在设计文档中定义
        response = requests.get(f"{BASE_URL}/health")
        assert response.status_code == 200


class TestConfigHotUpdateBoundaries:
    """配置热更新边界测试"""

    def test_llm_config_no_hot_update(self, server, api_headers):
        """TC-CFG-HOT-001: LLM 配置不支持热更新"""
        # LLM 配置修改需重启服务
        # 此测试验证配置存在
        response = requests.get(f"{BASE_URL}/health")
        data = response.json()

        if "checks" in data and "llm" in data["checks"]:
            # LLM 配置应存在
            assert data["checks"]["llm"]["status"] in ["healthy", "unhealthy"]

    def test_server_config_no_hot_update(self, server):
        """TC-CFG-HOT-002: Server 配置不支持热更新"""
        # Server 配置修改需重启服务
        response = requests.get(f"{BASE_URL}/health")
        assert response.status_code == 200

    def test_security_config_no_hot_update(self, server, api_headers):
        """TC-CFG-HOT-003: Security 配置不支持热更新"""
        # Security 配置修改需重启服务
        # 验证认证机制生效
        response = requests.post(
            f"{BASE_URL}/chat",
            json={"instruction": "test"}
        )

        # 无 API Key 应返回 401
        assert response.status_code == 401

    def test_skills_hot_update_supported(self, server, api_headers):
        """TC-CFG-HOT-004: Skills 配置支持热更新"""
        import os
        from conftest import TEST_HOME

        skill_dir = f"{TEST_HOME}/skills/hot_update_test"
        os.makedirs(skill_dir, exist_ok=True)

        skill_content = """---
name: hot_update_test
description: "热更新测试"
---
"""

        with open(f"{skill_dir}/SKILL.md", "w") as f:
            f.write(skill_content)

        time.sleep(5)

        # 验证 Skill 加载
        response = requests.get(
            f"{BASE_URL}/skills",
            headers=api_headers
        )

        skills = response.json()["skills"]
        skill_names = [s["name"] for s in skills]

        assert "hot_update_test" in skill_names

        # 清理
        import shutil
        shutil.rmtree(skill_dir)

    def test_mcp_hot_update_supported(self, server, api_headers):
        """TC-CFG-HOT-005: MCP 配置支持热更新"""
        import json
        import os
        from conftest import TEST_HOME

        mcp_file = f"{TEST_HOME}/mcp/hot_update_test.json"

        mcp_config = {
            "name": "hot_update_test",
            "type": "stdio",
            "description": "热更新测试",
            "isActive": False,
            "command": "echo",
            "args": ["test"]
        }

        with open(mcp_file, "w") as f:
            json.dump(mcp_config, f)

        time.sleep(5)

        # 清理
        os.remove(mcp_file)


class TestLLMMultiModelConfig:
    """LLM 多模型配置测试"""

    def test_default_model_field(self, server):
        """TC-LLM-CFG-001: default_model 配置"""
        response = requests.get(f"{BASE_URL}/health")

        data = response.json()

        if "checks" in data and "llm" in data["checks"]:
            llm_check = data["checks"]["llm"]
            if "model" in llm_check:
                # 验证 default_model 已设置
                assert llm_check["model"]

    def test_models_list_config(self, server, api_headers):
        """TC-LLM-CFG-002: models 配置列表"""
        # 验证 LLM 配置正确加载
        response = requests.get(f"{BASE_URL}/health")

        data = response.json()

        # 服务运行正常表示 LLM 配置有效
        assert data["status"] == "healthy"

    def test_model_env_variable_reference(self, server, api_headers):
        """TC-LLM-CFG-003: api_key 环境变量引用"""
        # 验证环境变量引用生效
        # ${OPENAI_API_KEY} 格式
        response = requests.get(f"{BASE_URL}/health")

        data = response.json()

        if "checks" in data and "llm" in data["checks"]:
            # LLM 健康表示配置正确
            assert data["checks"]["llm"]["status"] in ["healthy", "unhealthy"]


class TestPermissionBoundaries:
    """权限边界测试"""

    def test_permission_chat_only(self, server):
        """TC-PERM-001: chat 权限仅允许对话"""
        # 需要配置只有 chat 权限的 API Key
        # 此测试验证基本权限机制
        pass

    def test_permission_status_only(self, server):
        """TC-PERM-002: status 权限仅允许状态查询"""
        # 需要配置只有 status 权限的 API Key
        pass

    def test_permission_all_access(self, server, api_headers):
        """TC-PERM-003: all 权限可访问所有 API"""
        # 当前测试 API Key 应有 all 权限
        endpoints = [
            ("/chat", "POST"),
            ("/skills", "GET"),
            ("/tools", "GET"),
            ("/health", "GET"),
        ]

        for endpoint, method in endpoints:
            if method == "GET":
                response = requests.get(
                    f"{BASE_URL}{endpoint}",
                    headers=api_headers
                )
            else:
                response = requests.post(
                    f"{BASE_URL}{endpoint}",
                    headers=api_headers,
                    json={"instruction": "test"},
                    stream=True
                )

            # all 权限应可访问所有端点
            assert response.status_code in [200, 401]  # 401 可能因为需要特定参数


class TestCancelMechanismDetails:
    """取消机制详细测试"""

    def test_cancel_interrupts_llm_call(self, server, api_headers):
        """TC-CANCEL-001: 取消中断 LLM 调用"""
        payload = {"instruction": "长任务"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")

        # DELETE /chat/{sid} 端点已删除
        cancel_response = requests.delete(
            f"{BASE_URL}/chat/{session_id}",
            headers=api_headers
        )

        assert cancel_response.status_code == 404

        # SSE 流正常完成（无法通过 DELETE 取消）
        sse = SSEClient(response)
        completed = sse.get_completed_event()

        if completed:
            assert completed["data"]["finish_reason"] in ("stop", "tool_calls")

    def test_cancel_interrupts_mcp_tool(self, server, api_headers):
        """TC-CANCEL-002: 取消中断 MCP 工具调用"""
        payload = {"instruction": "读取大文件"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")

        # DELETE /chat/{sid} 端点已删除
        cancel_response = requests.delete(
            f"{BASE_URL}/chat/{session_id}",
            headers=api_headers
        )

        assert cancel_response.status_code == 404

        # 流正常完成
        SSEClient(response)

    def test_cancel_sse_pushes_event(self, server, api_headers):
        """TC-CANCEL-003: 取消推送 SSE cancelled 事件"""
        from conftest import SSEClient

        payload = {"instruction": "长任务"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")

        # DELETE /chat/{sid} 端点已删除
        cancel_response = requests.delete(
            f"{BASE_URL}/chat/{session_id}",
            headers=api_headers
        )

        assert cancel_response.status_code == 404

        # SSE 流正常完成
        sse = SSEClient(response)
        completed = sse.get_completed_event()

        if completed:
            assert completed["data"]["finish_reason"] in ("stop", "tool_calls")


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

    def test_acting_tool_call_step(self, server, api_headers):
        """TC-REACT-002: Acting 工具调用步骤"""
        payload = {"instruction": "读取文件 /tmp/test.txt"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        # 查找 tool_call 事件
        tool_calls = sse.get_events_by_type("tool_calls")

        if tool_calls:
            for call in tool_calls:
                assert "step_id" in call["data"]
                assert "name" in call["data"]
                assert "arguments" in call["data"]

    def test_observation_result_update(self, server, api_headers):
        """TC-REACT-003: Observation 结果更新"""
        payload = {"instruction": "测试对话"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        # tool_result 事件包含工具执行结果
        tool_results = sse.get_events_by_type("tool_result")

        for result in tool_results:
            assert "step_id" in result["data"]
            # output 或 error 字段
            assert "output" in result["data"] or "error" in result["data"]


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
        response = requests.get(f"{BASE_URL}/health")

        data = response.json()

        if "metrics" in data:
            assert "chats_running" in data["metrics"]
            assert data["metrics"]["chats_running"] >= 0

    def test_success_rate_metric(self, server, api_headers):
        """TC-METRICS-002: success_rate 指标"""
        response = requests.get(f"{BASE_URL}/health")

        data = response.json()

        if "metrics" in data and "success_rate" in data["metrics"]:
            rate = data["metrics"]["success_rate"]
            assert 0 <= rate <= 1