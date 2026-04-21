"""
调试脚本 - 获取失败测试的完整响应JSON
"""

import sys
import os
import requests
import json
import time
import subprocess
import yaml

# 配置
TEST_HOST = "localhost"
TEST_PORT = "8080"
TEST_API_KEY = "test-api-key-2026"
TEST_HOME = "/tmp/groot_test_debug"
BASE_URL = f"http://{TEST_HOST}:{TEST_PORT}"
GROOT_BIN = "/Users/zhangfengda/workspace/groot/bin/groot"

headers = {'Content-Type': 'application/json', 'X-API-Key': TEST_API_KEY}


def create_config():
    """创建测试配置"""
    os.makedirs(TEST_HOME, exist_ok=True)
    os.makedirs(f"{TEST_HOME}/skills", exist_ok=True)
    os.makedirs(f"{TEST_HOME}/mcp", exist_ok=True)
    os.makedirs(f"{TEST_HOME}/memory", exist_ok=True)
    os.makedirs(f"{TEST_HOME}/logs", exist_ok=True)

    config = {
        "server": {
            "host": TEST_HOST,
            "port": int(TEST_PORT)
        },
        "api_keys": [TEST_API_KEY],
        "llm": {
            "active_model": "mock-model",
            "models": {
                "mock-model": {
                    "base_url": "http://localhost:8888/mock",
                    "api_key": "mock-key",
                    "model": "mock"
                }
            }
        },
        "attachment": {
            "max_size": 50 * 1024 * 1024,
            "max_count": 10,
            "max_total_size": 100 * 1024 * 1024,
            "allowed_types": ["pdf", "doc", "docx", "txt", "json", "csv", "xml", "yaml", "png", "jpg", "jpeg", "zip"]
        }
    }

    with open(f"{TEST_HOME}/config.yaml", "w") as f:
        yaml.dump(config, f)

    print(f"配置已创建: {TEST_HOME}/config.yaml")
    print(f"allowed_types: {config['attachment']['allowed_types']}")


def start_server():
    """启动服务器"""
    # 检查是否已有进程
    try:
        resp = requests.get(f"{BASE_URL}/health", timeout=2)
        if resp.status_code == 200:
            print("服务器已运行")
            return None
    except:
        pass

    env = os.environ.copy()
    env["GROOT_HOME"] = TEST_HOME
    env["GROOT_API_KEY"] = TEST_API_KEY

    groot_bin = "/Users/zhangfengda/workspace/groot/bin/groot"

    # 使用二进制启动
    print("启动服务器...")
    process = subprocess.Popen(
        [groot_bin, "serve"],
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE
    )

    # 等待启动
    for i in range(30):
        try:
            resp = requests.get(f"{BASE_URL}/health", timeout=1)
            if resp.status_code == 200:
                print(f"服务器启动成功 (尝试 {i+1} 次)")
                return process
        except:
            pass
        time.sleep(1)

    print("服务器启动失败")
    process.terminate()
    return None


def test_url_attachment():
    """测试 URL 附件 - 获取400响应JSON"""
    print("\n" + "=" * 60)
    print("测试名称: test_url_attachment")
    print("=" * 60)

    payload = {
        "instruction": "获取这个URL的内容",
        "attachments": [
            {"type": "url", "name": "external.pdf", "url": "https://example.com/doc.pdf"}
        ]
    }

    print(f"\n请求payload:")
    print(json.dumps(payload, indent=2, ensure_ascii=False))

    resp = requests.post(f"{BASE_URL}/chat", headers=headers, json=payload, timeout=30)

    print(f"\n期望值: HTTP 200")
    print(f"实际值: HTTP {resp.status_code}")

    try:
        json_resp = resp.json()
        print(f"\n完整响应JSON:")
        print(json.dumps(json_resp, indent=2, ensure_ascii=False))
        return json_resp
    except Exception as e:
        print(f"\nJSON解析失败: {e}")
        print(f"响应内容: {resp.text}")
        return None


def test_multi_attachments():
    """测试多附件 - 获取400响应JSON"""
    print("\n" + "=" * 60)
    print("测试名称: test_multi_attachments")
    print("=" * 60)

    payload = {
        "instruction": "对比分析这两个文件",
        "attachments": [
            {"type": "file", "name": "file1.csv", "content": "bmFtZSxhZ2UsY2l0eQpBbGljZSwyNSxCZWlqaW5nCkJvYiwzMCxTaGFuZ2hhaQo="},
            {"type": "file", "name": "file2.pdf", "content": "JVBERi0xLjQKJcOiw6PDj8OTCjEgMCBvYmoKPDwvVHlwZS9DYXRhbG9nL1BhZ2VzIDIgMCBSPj4KZW5kb2JqCg=="},
            {"type": "url", "name": "external.pdf", "url": "https://example.com/doc.pdf"}
        ]
    }

    print(f"\n请求payload:")
    print(json.dumps(payload, indent=2, ensure_ascii=False))

    resp = requests.post(f"{BASE_URL}/chat", headers=headers, json=payload, timeout=30)

    print(f"\n期望值: HTTP 200")
    print(f"实际值: HTTP {resp.status_code}")

    try:
        json_resp = resp.json()
        print(f"\n完整响应JSON:")
        print(json.dumps(json_resp, indent=2, ensure_ascii=False))
        return json_resp
    except Exception as e:
        print(f"\nJSON解析失败: {e}")
        print(f"响应内容: {resp.text}")
        return None


def test_get_chat_detail():
    """测试 chat 详情 - 验证 ended_at 字段"""
    print("\n" + "=" * 60)
    print("测试名称: test_get_chat_detail")
    print("=" * 60)

    # 先创建一个chat
    payload = {"instruction": "测试任务"}
    print(f"\n创建chat请求:")
    print(json.dumps(payload, indent=2, ensure_ascii=False))

    resp = requests.post(f"{BASE_URL}/chat", headers=headers, json=payload, stream=True, timeout=60)
    session_id = resp.headers.get("X-Session-ID")
    print(f"\nSession ID: {session_id}")

    # 等待完成
    print("\n等待chat完成...")
    events = []
    for line in resp.iter_lines():
        if line:
            line_str = line.decode('utf-8')
            if line_str.startswith('event:'):
                event_type = line_str[6:].strip()
            elif line_str.startswith('data:'):
                try:
                    data = json.loads(line_str[5:].strip())
                    events.append({'event': event_type, 'data': data})
                    if event_type == 'completed':
                        print(f"Completed event: {json.dumps(data, indent=2, ensure_ascii=False)}")
                        chat_id = data.get('chat_id', '')
                except:
                    pass

    # 查询chat详情
    print("\n查询chat详情...")
    resp2 = requests.get(f"{BASE_URL}/chat/{session_id}", headers=headers, timeout=30)
    print(f"\n期望值: chat包含 'ended_at' 字段")
    print(f"HTTP Status: {resp2.status_code}")

    try:
        chat_data = resp2.json()
        print(f"\n完整响应JSON:")
        print(json.dumps(chat_data, indent=2, ensure_ascii=False))

        chat = chat_data.get('chat', {})
        print(f"\nchat字段内容:")
        print(json.dumps(chat, indent=2, ensure_ascii=False))
        print(f"\nended_at字段: {'存在' if 'ended_at' in chat else '不存在'}")

        # 查看实际文件
        if session_id:
            memory_dir = f"{TEST_HOME}/memory/{session_id}"
            print(f"\nmemory目录: {memory_dir}")
            if os.path.exists(memory_dir):
                for root, dirs, files in os.walk(memory_dir):
                    for file in files:
                        if file.endswith('.json'):
                            filepath = os.path.join(root, file)
                            print(f"\n文件: {filepath}")
                            with open(filepath) as f:
                                content = json.load(f)
                                print(json.dumps(content, indent=2, ensure_ascii=False))

        return chat_data
    except Exception as e:
        print(f"\n解析失败: {e}")
        print(f"响应内容: {resp2.text}")
        return None


def main():
    """主函数"""
    print("=" * 60)
    print("Groot 调试脚本 - 获取完整响应JSON")
    print("=" * 60)

    # 创建配置
    create_config()

    # 启动服务器
    server = start_server()
    if not server:
        print("无法启动服务器，退出")
        return

    try:
        # 运行测试
        url_resp = test_url_attachment()
        multi_resp = test_multi_attachments()
        chat_resp = test_get_chat_detail()

        print("\n" + "=" * 60)
        print("测试完成")
        print("=" * 60)

    finally:
        if server:
            print("\n关闭服务器...")
            server.terminate()
            server.wait(timeout=5)


if __name__ == "__main__":
    main()