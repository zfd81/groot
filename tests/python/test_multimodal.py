"""
多模态附件集成测试
测试 image/audio/video/file 附件透传给大模型
"""

import pytest
import requests
import base64
import struct
import zlib
import math
import json
import os
from conftest import BASE_URL

# 语义类用例（要求 LLM 真正"看懂"图片/文件内容）依赖真实多模态 LLM，
# Mock LLM 无法通过；默认跳过，设置 GROOT_TEST_REAL_LLM=1 启用
_needs_real_llm = pytest.mark.skipif(
    os.environ.get("GROOT_TEST_REAL_LLM") != "1",
    reason="需要真实多模态 LLM（设置 GROOT_TEST_REAL_LLM=1 启用）",
)


def make_test_png(width=100, height=100, color=(255, 0, 0)):
    """生成一个最小 PNG 文件（红色方块），纯标准库实现"""
    def make_chunk(chunk_type, data):
        chunk = chunk_type + data
        crc = struct.pack(">I", zlib.crc32(chunk) & 0xFFFFFFFF)
        return struct.pack(">I", len(data)) + chunk + crc

    # PNG 签名
    signature = b"\x89PNG\r\n\x1a\n"

    # IHDR 块
    ihdr_data = struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0)
    ihdr = make_chunk(b"IHDR", ihdr_data)

    # IDAT 块：每行像素数据
    raw_data = b""
    r, g, b = color
    for y in range(height):
        raw_data += b"\x00"  # filter: none
        for x in range(width):
            raw_data += struct.pack("BBB", r, g, b)

    idat = make_chunk(b"IDAT", zlib.compress(raw_data))

    # IEND 块
    iend = make_chunk(b"IEND", b"")

    return signature + ihdr + idat + iend


def make_test_wav(duration=0.5, sample_rate=8000, frequency=440):
    """生成一个最小 WAV 音频文件（正弦波），纯标准库实现

    Args:
        duration: 时长（秒）
        sample_rate: 采样率（Hz），默认 8000
        frequency: 正弦波频率（Hz），默认 440（标准 A 音）

    Returns:
        bytes: WAV 文件二进制数据
    """
    num_samples = int(sample_rate * duration)
    num_channels = 1
    bits_per_sample = 16
    byte_rate = sample_rate * num_channels * bits_per_sample // 8
    block_align = num_channels * bits_per_sample // 8

    # 生成正弦波样本（16-bit PCM）
    samples = b""
    for i in range(num_samples):
        value = int(32767 * math.sin(2 * math.pi * frequency * i / sample_rate))
        samples += struct.pack("<h", value)

    # RIFF header
    data_size = len(samples)
    file_size = 36 + data_size

    wav = b""
    wav += b"RIFF"
    wav += struct.pack("<I", file_size)
    wav += b"WAVE"

    # fmt chunk
    wav += b"fmt "
    wav += struct.pack("<I", 16)  # chunk size
    wav += struct.pack("<H", 1)   # PCM format
    wav += struct.pack("<H", num_channels)
    wav += struct.pack("<I", sample_rate)
    wav += struct.pack("<I", byte_rate)
    wav += struct.pack("<H", block_align)
    wav += struct.pack("<H", bits_per_sample)

    # data chunk
    wav += b"data"
    wav += struct.pack("<I", data_size)
    wav += samples

    return wav


def parse_sse_stream(response):
    """解析当前 SSE 格式的流式响应（只有 data: 行，无 event: 行）

    返回 (result_text, tool_calls, finish_reason, tool_results)
    """
    result_text = ""
    tool_calls = []
    finish_reason = ""
    tool_results = []

    for line in response.iter_lines(decode_unicode=False):
        if not line:
            continue
        line_str = line.decode("utf-8")
        if not line_str.startswith("data: "):
            continue
        data_str = line_str[6:]
        if data_str == "[DONE]":
            break
        try:
            event = json.loads(data_str)
        except json.JSONDecodeError:
            continue

        role = event.get("role", "")
        if "content" in event and role == "assistant":
            result_text += event["content"]
        if "reasoning_content" in event:
            pass  # thinking content, not needed for verification
        if "tool_calls" in event:
            tool_calls.extend(event["tool_calls"])
        if "finish_reason" in event:
            finish_reason = event["finish_reason"]
        if role == "tool":
            tool_results.append({
                "tool_call_id": event.get("tool_call_id"),
                "tool_name": event.get("tool_name"),
                "content": event.get("content"),
            })

    return result_text, tool_calls, finish_reason, tool_results


class TestMultimodalImage:
    """多模态图片附件测试"""

    @_needs_real_llm
    def test_image_attachment_to_llm(self, server, api_headers):
        """TC-MM-001: 上传红色方块图片，验证 LLM 能识别颜色"""
        # 生成红色 PNG 图片
        png_bytes = make_test_png(100, 100, color=(255, 0, 0))
        png_base64 = base64.b64encode(png_bytes).decode()

        payload = {
            "instruction": "请描述这张图片的内容，包括颜色和形状",
            "attachments": [
                {
                    "type": "image",
                    "name": "red_square.png",
                    "content": png_base64
                }
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True,
            timeout=120
        )

        assert response.status_code == 200, f"请求失败: {response.text[:500]}"
        session_id = response.headers.get("X-Session-ID")
        assert session_id, "缺少 X-Session-ID"

        result_text, tool_calls, finish_reason, _ = parse_sse_stream(response)
        print(f"\n[TC-MM-001] LLM 返回结果: {result_text}")

        # 验证结果包含颜色/图片相关描述
        result_lower = result_text.lower()
        contains_color = any(c in result_lower for c in ["红", "red", "颜色", "color", "方", "square", "矩形", "图片", "image"])
        assert contains_color, f"模型未识别图片内容，返回: {result_text[:200]}"

    def test_image_with_instruction(self, server, api_headers):
        """TC-MM-002: 上传图片并附文字指令"""
        # 生成绿色 PNG 图片
        png_bytes = make_test_png(50, 50, color=(0, 255, 0))
        png_base64 = base64.b64encode(png_bytes).decode()

        payload = {
            "instruction": "这张图片是什么颜色的？只回答颜色名称，不要其他内容",
            "attachments": [
                {
                    "type": "image",
                    "name": "green_square.png",
                    "content": png_base64
                }
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True,
            timeout=120
        )

        assert response.status_code == 200
        result_text, _, _, _ = parse_sse_stream(response)
        print(f"\n[TC-MM-002] LLM 返回结果: {result_text}")

    @_needs_real_llm
    def test_file_attachment_as_base64(self, server, api_headers):
        """TC-MM-003: 上传 file 类型附件（文本文件），验证 Base64 透传"""
        file_content = "Hello from test file\nLine 2: test data"
        file_base64 = base64.b64encode(file_content.encode()).decode()

        payload = {
            "instruction": "请读取附件文件的内容并告诉我里面写了什么",
            "attachments": [
                {
                    "type": "file",
                    "name": "test_data.txt",
                    "content": file_base64
                }
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True,
            timeout=120
        )

        assert response.status_code == 200
        result_text, _, _, _ = parse_sse_stream(response)
        print(f"\n[TC-MM-003] LLM 返回结果: {result_text}")

        # 验证模型能看到文件内容
        result_lower = result_text.lower()
        assert any(w in result_lower for w in ["hello", "test", "data"]), \
            f"模型未能读取文件内容，返回: {result_text[:200]}"

    def test_multiple_image_attachments(self, server, api_headers):
        """TC-MM-004: 上传多张图片"""
        red_png = base64.b64encode(make_test_png(30, 30, color=(255, 0, 0))).decode()
        blue_png = base64.b64encode(make_test_png(30, 30, color=(0, 0, 255))).decode()

        payload = {
            "instruction": "我上传了两张图片，请描述它们分别是什么颜色",
            "attachments": [
                {"type": "image", "name": "red.png", "content": red_png},
                {"type": "image", "name": "blue.png", "content": blue_png}
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True,
            timeout=120
        )

        assert response.status_code == 200
        result_text, _, _, _ = parse_sse_stream(response)
        print(f"\n[TC-MM-004] LLM 返回结果: {result_text}")

    def test_mixed_attachments(self, server, api_headers):
        """TC-MM-005: 混合类型附件（image + file）"""
        png_bytes = make_test_png(30, 30, color=(255, 255, 0))
        png_base64 = base64.b64encode(png_bytes).decode()
        file_base64 = base64.b64encode(b"IMPORTANT: The answer is 42").decode()

        payload = {
            "instruction": "看图片和文件内容，图片是什么颜色？文件里写的重要信息是什么？",
            "attachments": [
                {"type": "image", "name": "yellow.png", "content": png_base64},
                {"type": "file", "name": "secret.txt", "content": file_base64}
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True,
            timeout=120
        )

        assert response.status_code == 200
        result_text, _, _, _ = parse_sse_stream(response)
        print(f"\n[TC-MM-005] LLM 返回结果: {result_text}")


class TestMultimodalAudio:
    """多模态音频附件测试"""

    def test_audio_attachment_to_llm(self, server, api_headers):
        """TC-MM-007: 上传 WAV 音频文件，验证 LLM 能识别音频属性"""
        wav_bytes = make_test_wav(duration=0.5, sample_rate=8000, frequency=440)
        wav_base64 = base64.b64encode(wav_bytes).decode()

        payload = {
            "instruction": "请分析这个音频文件的基本属性，比如采样率、声道数等",
            "attachments": [
                {
                    "type": "audio",
                    "name": "test_tone.wav",
                    "content": wav_base64
                }
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True,
            timeout=120
        )

        assert response.status_code == 200, f"请求失败: {response.text[:500]}"
        session_id = response.headers.get("X-Session-ID")
        assert session_id, "缺少 X-Session-ID"

        result_text, _, _, _ = parse_sse_stream(response)
        print(f"\n[TC-MM-007] LLM 返回结果: {result_text}")

    def test_audio_with_instruction(self, server, api_headers):
        """TC-MM-008: 上传音频并附文字指令"""
        wav_bytes = make_test_wav(duration=0.3, sample_rate=8000, frequency=523)
        wav_base64 = base64.b64encode(wav_bytes).decode()

        payload = {
            "instruction": "这个音频的频率大概是多少？只回答频率，不要其他内容",
            "attachments": [
                {
                    "type": "audio",
                    "name": "c5_note.wav",
                    "content": wav_base64
                }
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True,
            timeout=120
        )

        assert response.status_code == 200
        result_text, _, _, _ = parse_sse_stream(response)
        print(f"\n[TC-MM-008] LLM 返回结果: {result_text}")

    def test_mixed_image_audio(self, server, api_headers):
        """TC-MM-010: 混合附件（image + audio）"""
        png_bytes = make_test_png(30, 30, color=(255, 0, 0))
        png_base64 = base64.b64encode(png_bytes).decode()
        wav_bytes = make_test_wav(duration=0.2, sample_rate=8000, frequency=440)
        wav_base64 = base64.b64encode(wav_bytes).decode()

        payload = {
            "instruction": "我上传了一张图片和一个音频文件，请描述图片颜色和音频大致属性",
            "attachments": [
                {"type": "image", "name": "red_square.png", "content": png_base64},
                {"type": "audio", "name": "tone.wav", "content": wav_base64}
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True,
            timeout=120
        )

        assert response.status_code == 200
        result_text, _, _, _ = parse_sse_stream(response)
        print(f"\n[TC-MM-010] LLM 返回结果: {result_text}")
