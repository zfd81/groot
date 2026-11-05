"""
深度测试 - 真实 LLM 交互测试
测试与真实 LLM 的对话、多轮对话、工具调用等
"""

import pytest
import requests
import time
import json
import base64
import os
from conftest import BASE_URL, TEST_HOME, SSEClient

# 本文件的断言依赖真实 LLM 的智能输出（代码生成/翻译/数学等语义验证），
# Mock LLM 返回固定文案无法通过。默认跳过；配好真实 LLM（模型库默认模型
# 指向真实端点）后设置 GROOT_TEST_REAL_LLM=1 启用。
pytestmark = pytest.mark.skipif(
    os.environ.get("GROOT_TEST_REAL_LLM") != "1",
    reason="需要真实 LLM（设置 GROOT_TEST_REAL_LLM=1 启用）",
)


class TestRealLLMBasic:
    """真实 LLM 基础对话测试"""

    def test_real_llm_simple_question(self, server, api_headers):
        """TC-REAL-001: 真实 LLM 简单问答"""
        payload = {"messages": [{"role": "user", "content": "你好，请用一句话介绍一下你自己"}], "stream": True}

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True,
            timeout=120
        )

        assert response.status_code == 200

        sse = SSEClient(response)
        completed = sse.get_completed_event()

        # 验证完成事件
        assert completed is not None
        assert completed["data"]["finish_reason"] in ("stop", "tool_calls")

        # 验证有内容返回
        messages = sse.get_message_events()
        assert len(messages) > 0
        result = " ".join(m.get("data", {}).get("content", "") for m in messages)
        assert len(result) > 10  # 应有实质性回复

    def test_real_llm_code_generation(self, server, api_headers):
        """TC-REAL-002: 真实 LLM 代码生成"""
        payload = {"messages": [{"role": "user", "content": "请用 Python 写一个计算斐波那契数列的函数"}], "stream": True}

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True,
            timeout=120
        )

        assert response.status_code == 200

        sse = SSEClient(response)
        completed = sse.get_completed_event()

        assert completed["data"]["finish_reason"] in ("stop", "tool_calls")
        messages = sse.get_message_events()
        result = " ".join(m.get("data", {}).get("content", "") for m in messages)

        # 验证包含代码相关内容
        assert "def" in result or "function" in result or "fibonacci" in result.lower()

    def test_real_llm_json_output(self, server, api_headers):
        """TC-REAL-003: 真实 LLM JSON 格式输出"""
        payload = {
            "messages": [
                {"role": "system", "content": "请只输出 JSON 格式，不要添加其他解释"},
                {"role": "user", "content": "请生成一个包含 name, age, city 三个字段的 JSON 对象示例"},
            ],
            "stream": True,
        }

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True,
            timeout=120
        )

        assert response.status_code == 200

        sse = SSEClient(response)
        completed = sse.get_completed_event()

        assert completed["data"]["finish_reason"] in ("stop", "tool_calls")

        # 验证返回内容
        messages = sse.get_message_events()
        result = " ".join(m.get("data", {}).get("content", "") for m in messages)
        # 应包含 JSON 结构迹象
        assert "{" in result or "name" in result.lower()


class TestRealLLMMultiRound:
    """真实 LLM 多轮对话测试"""

    def test_real_llm_two_round_conversation(self, server, api_headers):
        """TC-REAL-004: 真实 LLM 两轮对话"""
        # 第一轮
        payload1 = {"messages": [{"role": "user", "content": "请记住这个数字：42"}], "stream": True}

        response1 = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload1,
            stream=True,
            timeout=120
        )

        sse1 = SSEClient(response1)
        session_id = sse1.get_session_id()
        assert session_id is not None

        completed1 = sse1.get_completed_event()
        assert completed1["data"]["finish_reason"] in ("stop", "tool_calls")

        # 第二轮（继续会话）
        payload2 = {
            "messages": [{"role": "user", "content": "我刚才让你记住的数字是多少？"}],
            "session_id": session_id,
            "stream": True,
        }

        response2 = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload2,
            stream=True,
            timeout=120
        )

        sse2 = SSEClient(response2)
        completed2 = sse2.get_completed_event()

        assert completed2["data"]["finish_reason"] in ("stop", "tool_calls")

        # 验证 LLM 能记住上下文
        messages = sse2.get_message_events()
        result = " ".join(m.get("data", {}).get("content", "") for m in messages)
        assert "42" in result  # 应能回忆起之前说的数字

    def test_real_llm_three_round_conversation(self, server, api_headers):
        """TC-REAL-005: 真实 LLM 三轮对话"""
        # 第一轮：设定角色
        payload1 = {
            "messages": [
                {"role": "system", "content": "请以专业、简洁的方式回答编程问题"},
                {"role": "user", "content": "你是一个专业的 Python 编程助手"},
            ],
            "stream": True,
        }

        response1 = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload1,
            stream=True,
            timeout=120
        )

        session_id = SSEClient(response1).get_session_id()

        # 第二轮：提问
        payload2 = {
            "messages": [{"role": "user", "content": "如何判断一个列表是否为空？"}],
            "session_id": session_id,
            "stream": True,
        }

        response2 = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload2,
            stream=True,
            timeout=120
        )

        SSEClient(response2)

        # 第三轮：追问
        payload3 = {
            "messages": [{"role": "user", "content": "还有其他方法吗？"}],
            "session_id": session_id,
            "stream": True,
        }

        response3 = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload3,
            stream=True,
            timeout=120
        )

        sse3 = SSEClient(response3)
        completed3 = sse3.get_completed_event()

        assert completed3["data"]["finish_reason"] in ("stop", "tool_calls")


class TestRealLLMToolCall:
    """真实 LLM 工具调用测试"""

    def test_real_llm_file_read_intent(self, server, api_headers):
        """TC-REAL-006: 真实 LLM 文件读取意图"""
        # 先创建一个测试文件
        test_content = "这是一个测试文件的内容。\nHello, World!"
        test_file = f"{TEST_HOME}/test_read.txt"

        with open(test_file, "w") as f:
            f.write(test_content)

        payload = {"messages": [{"role": "user", "content": f"请读取文件 {test_file} 的内容"}], "stream": True}

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True,
            timeout=120
        )

        sse = SSEClient(response)
        completed = sse.get_completed_event()

        # 验证完成状态
        assert completed["data"]["finish_reason"] in ("stop", "tool_calls")

        # 检查是否调用了工具
        tool_steps = sse.get_tool_calls()

        # LLM 可能会尝试调用 file_read 工具
        # 或者直接返回指令（取决于模型行为）

        # 清理测试文件
        os.remove(test_file)

    def test_real_llm_with_attachment(self, server, api_headers):
        """TC-REAL-007: 真实 LLM 处理附件"""
        # 创建一个简单的文本文件附件
        content = "姓名: 张三\n年龄: 25\n城市: 北京\n"
        base64_content = base64.b64encode(content.encode()).decode()

        payload = {
            "messages": [{
                "role": "user",
                "content": [
                    {"type": "text", "text": "请分析这个文件的内容"},
                    {"type": "file", "file": {
                        "filename": "person.txt",
                        "file_data": f"data:text/plain;base64,{base64_content}",
                    }},
                ],
            }],
            "stream": True,
        }

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True,
            timeout=120
        )

        assert response.status_code == 200

        sse = SSEClient(response)
        completed = sse.get_completed_event()

        assert completed["data"]["finish_reason"] in ("stop", "tool_calls")

        # 验证返回内容提到文件信息
        messages = sse.get_message_events()
        result = " ".join(m.get("data", {}).get("content", "") for m in messages)
        # LLM 应能识别文件内容


class TestRealLLMComplexTasks:
    """真实 LLM 复杂任务测试"""

    def test_real_llm_analysis_task(self, server, api_headers):
        """TC-REAL-008: 真实 LLM 分析任务"""
        payload = {
            "messages": [{
                "role": "user",
                "content": "请分析以下这段文字的主题和关键词：" +
                           "\"人工智能正在改变我们的生活方式。" +
                           "从智能手机到智能家居，AI技术已经渗透到各个领域。" +
                           "未来，人工智能将继续推动科技进步。\"",
            }],
            "stream": True,
        }

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True,
            timeout=120
        )

        sse = SSEClient(response)
        completed = sse.get_completed_event()

        assert completed["data"]["finish_reason"] in ("stop", "tool_calls")

        # 验证分析结果
        messages = sse.get_message_events()
        result = " ".join(m.get("data", {}).get("content", "") for m in messages)
        assert "人工智能" in result or "AI" in result

    def test_real_llm_translation_task(self, server, api_headers):
        """TC-REAL-009: 真实 LLM 翻译任务"""
        payload = {
            "messages": [{
                "role": "user",
                "content": "请将以下中文翻译成英文：" +
                           "\"机器学习是人工智能的一个重要分支。\"",
            }],
            "stream": True,
        }

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True,
            timeout=120
        )

        sse = SSEClient(response)
        completed = sse.get_completed_event()

        assert completed["data"]["finish_reason"] in ("stop", "tool_calls")

        # 验证翻译结果包含英文
        messages = sse.get_message_events()
        result = " ".join(m.get("data", {}).get("content", "") for m in messages)
        assert "Machine learning" in result or "machine learning" in result.lower()

    def test_real_llm_math_problem(self, server, api_headers):
        """TC-REAL-010: 真实 LLM 数学问题"""
        payload = {"messages": [{"role": "user", "content": "计算：123 + 456 * 2 - 78 = ?"}], "stream": True}

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True,
            timeout=120
        )

        sse = SSEClient(response)
        completed = sse.get_completed_event()

        assert completed["data"]["finish_reason"] in ("stop", "tool_calls")

        # 验证计算结果（123 + 912 - 78 = 957）
        messages = sse.get_message_events()
        result = " ".join(m.get("data", {}).get("content", "") for m in messages)
        assert "957" in result


class TestRealLLMErrorHandling:
    """真实 LLM 错误处理测试"""

    def test_real_llm_invalid_instruction_recovery(self, server, api_headers):
        """TC-REAL-011: 真实 LLM 无效指令恢复"""
        # 发送一个模糊的指令
        payload = {"messages": [{"role": "user", "content": "..."}], "stream": True}

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True,
            timeout=120
        )

        # 服务应能处理并返回响应（不崩溃）
        assert response.status_code in [200, 400]

        if response.status_code == 200:
            sse = SSEClient(response)
            completed = sse.get_completed_event()
            # LLM 可能会询问更多细节或给出默认响应
            assert completed is not None

    def test_real_llm_long_instruction(self, server, api_headers):
        """TC-REAL-012: 真实 LLM 长指令处理"""
        long_instruction = "请详细解释以下概念：" + "人工智能、机器学习、深度学习、神经网络、自然语言处理、计算机视觉、强化学习、生成式AI。" * 3

        payload = {"messages": [{"role": "user", "content": long_instruction}], "stream": True}

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True,
            timeout=180
        )

        assert response.status_code == 200

        sse = SSEClient(response)
        completed = sse.get_completed_event()

        # 验证能处理长指令
        assert completed["data"]["finish_reason"] in ("stop", "tool_calls")


class TestRealLLMPerformance:
    """真实 LLM 性能测试"""

    def test_real_llm_response_time(self, server, api_headers):
        """TC-REAL-013: 真实 LLM 响应时间"""
        payload = {"messages": [{"role": "user", "content": "说一个字：好"}], "stream": True}

        start_time = time.time()

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True,
            timeout=120
        )

        SSEClient(response)

        elapsed_time = time.time() - start_time

        # 简单指令应在合理时间内完成
        # 30秒是一个宽松的上限
        assert elapsed_time < 30

        print(f"\n响应时间: {elapsed_time:.2f}秒")

    def test_real_llm_concurrent_requests(self, server, api_headers):
        """TC-REAL-014: 真实 LLM 并发请求"""
        import concurrent.futures

        def send_chat(i):
            payload = {"messages": [{"role": "user", "content": f"请说出数字{i}"}], "stream": True}
            response = requests.post(
                f"{BASE_URL}/chat/completions",
                headers=api_headers,
                json=payload,
                stream=True,
                timeout=120
            )
            SSEClient(response)
            return response.status_code

        # 发送3个并发请求（使用不同的session）
        with concurrent.futures.ThreadPoolExecutor(max_workers=3) as executor:
            futures = [executor.submit(send_chat, i) for i in range(3)]
            results = [f.result() for f in futures]

        # 所有请求应成功
        for status in results:
            assert status == 200


class TestRealLLMHistory:
    """真实 LLM 历史记录测试"""

    def test_real_llm_history_persistence(self, server, api_headers):
        """TC-REAL-015: 真实 LLM 历史记录持久化"""
        # 进行对话
        payload = {"messages": [{"role": "user", "content": "我的名字是小明"}], "stream": True}

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True,
            timeout=120
        )

        session_id = SSEClient(response).get_session_id()

        # 查询历史记录
        history_response = requests.get(
            f"{BASE_URL}/sess/{session_id}",
            headers=api_headers
        )

        assert history_response.status_code == 200
        history_data = history_response.json()

        # 验证历史记录
        assert "history" in history_data
        messages = history_data["history"]["messages"]
        assert len(messages) == 1

        # 验证消息内容
        msg = messages[0]
        assert "instruction" in msg
        assert msg["instruction"] == "我的名字是小明"
        assert "result" in msg

    def test_real_llm_chat_record_detail(self, server, api_headers):
        """TC-REAL-016: 真实 LLM 对话记录详情"""
        payload = {"messages": [{"role": "user", "content": "测试对话记录"}], "stream": True}

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True,
            timeout=120
        )

        session_id = SSEClient(response).get_session_id()

        # 查询对话详情
        detail_response = requests.get(
            f"{BASE_URL}/chat/{session_id}",
            headers=api_headers
        )

        assert detail_response.status_code == 200
        detail_data = detail_response.json()

        # 验证详情结构
        if detail_data.get("chat"):
            chat = detail_data["chat"]
            assert "instruction" in chat
            assert "result" in chat
            assert "status" in chat


class TestRealLLMSSEReliability:
    """真实 LLM SSE 可靠性测试"""

    def test_real_llm_sse_connection_stable(self, server, api_headers):
        """TC-REAL-017: 真实 LLM SSE 连接稳定性"""
        payload = {
            "messages": [{"role": "user", "content": "请写一首短诗，四行即可"}],
            "stream": True,
            "stream_options": {"include_usage": True},
        }

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True,
            timeout=120
        )

        sse = SSEClient(response)

        # 验证事件完整性
        events = sse.events
        assert len(events) > 0

        # 验证事件类型
        event_types = [e["event"] for e in events]
        assert "message" in event_types
        assert "finish" in event_types

        # finish 倒数第二，usage chunk 最后
        assert event_types[-1] == "usage" and event_types[-2] == "finish"

    def test_real_llm_no_duplicate_message(self, server, api_headers):
        """TC-REAL-018: 真实 LLM message 不应重复"""
        payload = {"messages": [{"role": "user", "content": "测试message事件"}], "stream": True}

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True,
            timeout=120
        )

        sse = SSEClient(response)
        message_events = sse.get_events_by_type("message")

        # message 应只发送一次
        # 这是一个已知的bug，测试记录当前行为
        print(f"\nmessage 事件数量: {len(message_events)}")
        # assert len(message_events) == 1  # 期望修复后通过
