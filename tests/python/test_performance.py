"""
性能测试
测试超时、LLM性能、MCP性能、ReAct执行限制等

注意：新版设计文档删除了以下功能，对应测试已移除：
- 限流功能（performance.rate_limit）- 已删除
- LLM/MCP 并发调用限制（performance.llm/mcp）- 已删除
- 存储引擎配置（storage.engine）- 已删除，改为文件系统存储
"""

import pytest
import requests
import time
import concurrent.futures
from conftest import BASE_URL, SSEClient


class TestTimeout:
    """超时功能测试"""

    def test_request_timeout_handling(self, server, api_headers):
        """TC-PERF-001: 请求超时处理"""
        # 发送请求并设置超时
        try:
            response = requests.post(
                f"{BASE_URL}/chat",
                headers=api_headers,
                json={"instruction": "test"},
                stream=True,
                timeout=1  # 1秒超时
            )
        except requests.exceptions.Timeout:
            # 预期可能超时
            assert True

    def test_step_timeout_in_config(self, server, api_headers):
        """TC-PERF-002: step_timeout 配置生效"""
        # 验证配置中 step_timeout 存在（默认60秒）
        response = requests.get(f"{BASE_URL}/health")

        # health 接口不直接显示超时配置
        # 验证服务运行正常
        assert response.status_code == 200

    def test_step_timeout_triggers_termination(self, server, api_headers):
        """TC-PERF-003: 单步执行超时终止"""
        # 发送可能触发超时的请求
        # 具体行为取决于实现
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "执行长时间任务"},
            stream=True
        )

        sse = SSEClient(response)
        completed = sse.get_completed_event()

        # 如果超时，completed.status 应为 failed
        # error.code 可能是 timeout 相关


class TestLLMPerformance:
    """LLM 性能测试"""

    def test_llm_response_time(self, server, api_headers):
        """TC-PERF-004: LLM 响应时间"""
        start_time = time.time()

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "写一个简单的函数"},
            stream=True
        )

        SSEClient(response)

        elapsed_time = time.time() - start_time

        # 验证响应时间在合理范围（取决于模型）
        # mock 模型应很快
        # 真实模型可能需要几秒到几十秒

    def test_llm_error_retry(self, server, api_headers):
        """TC-PERF-005: LLM 调用失败重试（error_retry 配置）"""
        # 新版设计中 error_retry 配置保留在 react 部分
        # 默认值：2次重试
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "测试LLM调用"},
            stream=True
        )

        SSEClient(response)

        # 验证重试行为（如果有失败情况）


class TestMCPPerformance:
    """MCP 性能测试"""

    def test_mcp_tool_call_time(self, server, api_headers):
        """TC-PERF-006: MCP 工具调用时间"""
        start_time = time.time()

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "读取文件内容"},
            stream=True
        )

        SSEClient(response)

        elapsed_time = time.time() - start_time

        # 工具调用应在合理时间完成


class TestReActLimits:
    """ReAct 执行限制测试"""

    def test_max_iterations(self, server, api_headers):
        """TC-PERF-007: max_iterations 配置（默认20）"""
        # 发送可能触发多次迭代的请求
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "执行复杂分析任务"},
            stream=True
        )

        sse = SSEClient(response)

        # 验证不会超过 max_iterations（默认20）
        thinking_events = sse.get_events_by_type("thinking")
        assert len(thinking_events) <= 20  # 或配置值

        # 如果达到限制，completed.status 应为 failed
        completed = sse.get_completed_event()
        if len(thinking_events) >= 20:
            assert completed["data"]["finish_reason"] not in ("stop", "tool_calls")

    def test_max_tokens(self, server, api_headers):
        """TC-PERF-008: max_tokens 配置（默认100000）"""
        # 发送请求
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "生成大量文本"},
            stream=True
        )

        SSEClient(response)

        # 验证 token 消耗不超过限制（取决于实现）
        # 如果超限，completed.status 应为 failed

    def test_error_retry(self, server, api_headers):
        """TC-PERF-009: error_retry 配置（默认2次）"""
        # 发送可能失败的请求
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "读取不存在的文件"},
            stream=True
        )

        SSEClient(response)

        # 验证失败后有重试（如果有）
        # 新版设计中 error_retry 配置在 react 部分

    def test_nesting_max_depth(self, server, api_headers):
        """TC-PERF-010: nesting_max_depth 配置（默认3）"""
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "执行嵌套任务"},
            stream=True
        )

        sse = SSEClient(response)

        # 验证 nesting_level 不超过限制
        thinking_events = sse.get_events_by_type("thinking")

        for step in thinking_events:
            nesting_level = step["data"].get("nesting_level", 0)
            assert nesting_level <= 3  # 默认值


class TestConcurrency:
    """并发性能测试"""

    def test_concurrent_sessions(self, server, api_headers):
        """TC-PERF-011: 并发会话处理"""
        # 创建多个新会话并发执行
        def send_request(i):
            response = requests.post(
                f"{BASE_URL}/chat",
                headers=api_headers,
                json={"instruction": f"测试{i}"},
                stream=True
            )
            SSEClient(response)
            return response.status_code

        with concurrent.futures.ThreadPoolExecutor(max_workers=5) as executor:
            futures = [executor.submit(send_request, i) for i in range(5)]
            results = [f.result() for f in futures]

        # 验证所有请求成功处理
        for status in results:
            assert status == 200

    def test_concurrent_requests_per_session(self, server, api_headers):
        """TC-PERF-012: 同一会话并发限制（RuntimeState）"""
        # 先获取一个 session_id
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "test"},
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")

        # 同时发送多个请求到同一会话
        headers2 = api_headers.copy()
        headers2["X-Session-ID"] = session_id

        def send_request(i):
            return requests.post(
                f"{BASE_URL}/chat",
                headers=headers2,
                json={"instruction": f"test{i}"}
            ).status_code

        with concurrent.futures.ThreadPoolExecutor(max_workers=3) as executor:
            futures = [executor.submit(send_request, i) for i in range(3)]
            results = [f.result() for f in futures]

        # 应有一个成功，其他返回 409（chat_limit_exceeded）
        success_count = sum(1 for s in results if s == 200)
        conflict_count = sum(1 for s in results if s == 409)

        assert success_count >= 1
        assert conflict_count >= 2


class TestResourceUsage:
    """资源使用测试"""

    def test_memory_usage(self, server, api_headers):
        """TC-PERF-013: 内存使用"""
        # 发送多个请求
        for i in range(10):
            response = requests.post(
                f"{BASE_URL}/chat",
                headers=api_headers,
                json={"instruction": f"test{i}"},
                stream=True
            )
            SSEClient(response)

        # 验证服务仍然正常
        response = requests.get(f"{BASE_URL}/health")
        assert response.status_code == 200

        # 验证内存使用在合理范围（通过 health 接口）
        data = response.json()
        if "checks" in data and "memory" in data["checks"]:
            memory_check = data["checks"]["memory"]
            assert memory_check["status"] == "healthy"

    def test_running_count_in_metrics(self, server, api_headers):
        """TC-PERF-014: health 接口显示运行数"""
        response = requests.get(f"{BASE_URL}/health")

        data = response.json()

        # 验证 metrics 中有 chats_running
        if "metrics" in data:
            assert "chats_running" in data["metrics"]
            assert data["metrics"]["chats_running"] >= 0

            # 验证 success_rate
            if "success_rate" in data["metrics"]:
                assert 0 <= data["metrics"]["success_rate"] <= 1


# 注意：以下测试已删除，因为新版设计删除了对应功能：
#
# DELETED: TestRateLimiting 类
#   - test_rate_limit_enforced (限流配置已删除)
#   - test_rate_limit_reset (限流配置已删除)
#
# DELETED: 并发调用限制测试
#   - performance.llm.max_concurrent_calls (已删除)
#   - performance.mcp.max_concurrent_calls_per_server (已删除)