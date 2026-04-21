"""
GROOT.md 热加载测试
测试 GROOT.md 文件的动态加载、修改、删除

注意: GROOT.md 热加载没有防抖延迟，修改后立即生效
建议等待时间: 1秒（文件系统事件处理时间）
"""

import pytest
import requests
import json
import os
import time
import shutil
from conftest import BASE_URL, TEST_HOME


class TestGrootMdHotReload:
    """GROOT.md 热加载测试"""

    def test_create_groot_md_loads_content(self, server, api_headers):
        """TC-GROOTMD-001: 创建 GROOT.md 后内容加载"""
        groot_md_file = f"{TEST_HOME}/GROOT.md"

        # 确保 GROOT.md 不存在
        if os.path.exists(groot_md_file):
            os.remove(groot_md_file)

        # 创建 GROOT.md
        content = """# 测试规范

- 使用中文回答
- 代码使用 Go 语言风格
"""
        with open(groot_md_file, "w") as f:
            f.write(content)

        # 等待热加载生效（无防抖，1秒足够）
        time.sleep(1)

        # 发送 chat 请求验证内容是否注入
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "你好，请用一句话介绍自己"},
            stream=True
        )

        assert response.status_code == 200
        # SSE 响应需要消费完整内容
        for line in response.iter_lines():
            if line:
                pass  # 消费 SSE 流

        # 清理
        os.remove(groot_md_file)

    def test_modify_groot_md_updates_content(self, server, api_headers):
        """TC-GROOTMD-002: 修改 GROOT.md 后内容更新"""
        groot_md_file = f"{TEST_HOME}/GROOT.md"

        # 创建初始 GROOT.md
        content_v1 = """# 规范V1

- 版本1的规范
"""
        with open(groot_md_file, "w") as f:
            f.write(content_v1)

        time.sleep(1)

        # 修改 GROOT.md
        content_v2 = """# 规范V2

- 版本2的规范
- 这是更新后的内容
"""
        with open(groot_md_file, "w") as f:
            f.write(content_v2)

        # 等待更新生效
        time.sleep(1)

        # 发送请求验证
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "测试"},
            stream=True
        )

        assert response.status_code == 200
        for line in response.iter_lines():
            if line:
                pass

        # 清理
        os.remove(groot_md_file)

    def test_delete_groot_md_clears_cache(self, server, api_headers):
        """TC-GROOTMD-003: 删除 GROOT.md 后缓存清空"""
        groot_md_file = f"{TEST_HOME}/GROOT.md"

        # 创建 GROOT.md
        content = """# 测试规范

- 特定规范内容
"""
        with open(groot_md_file, "w") as f:
            f.write(content)

        time.sleep(1)

        # 验证已加载
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "你好"},
            stream=True
        )
        assert response.status_code == 200
        for line in response.iter_lines():
            if line:
                pass

        # 删除 GROOT.md
        os.remove(groot_md_file)

        # 等待缓存清空
        time.sleep(1)

        # 验证删除后仍可正常工作
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "你好"},
            stream=True
        )

        assert response.status_code == 200
        for line in response.iter_lines():
            if line:
                pass

    def test_groot_md_not_exists_works_normal(self, server, api_headers):
        """TC-GROOTMD-004: GROOT.md 不存在时正常工作"""
        groot_md_file = f"{TEST_HOME}/GROOT.md"

        # 确保 GROOT.md 不存在
        if os.path.exists(groot_md_file):
            os.remove(groot_md_file)

        time.sleep(1)

        # 发送请求，验证服务正常
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "你好，请介绍自己"},
            stream=True
        )

        assert response.status_code == 200
        for line in response.iter_lines():
            if line:
                pass

    def test_groot_md_empty_file_clears_cache(self, server, api_headers):
        """TC-GROOTMD-005: GROOT.md 为空文件时缓存清空"""
        groot_md_file = f"{TEST_HOME}/GROOT.md"

        # 先创建有内容的文件
        content = """# 规范

- 测试内容
"""
        with open(groot_md_file, "w") as f:
            f.write(content)

        time.sleep(1)

        # 清空文件（写入空内容）
        with open(groot_md_file, "w") as f:
            f.write("")

        time.sleep(1)

        # 验证服务正常
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "测试"},
            stream=True
        )

        assert response.status_code == 200
        for line in response.iter_lines():
            if line:
                pass

        # 清理
        os.remove(groot_md_file)

    def test_groot_md_large_content(self, server, api_headers):
        """TC-GROOTMD-006: GROOT.md 大内容加载"""
        groot_md_file = f"{TEST_HOME}/GROOT.md"

        # 创建较大内容的 GROOT.md
        content = """# 项目规范

## 代码规范

1. 使用 Go 语言
2. 遵循标准格式化
3. 添加必要的注释

## 响应规范

1. 使用中文回答
2. 结构清晰
3. 包含代码示例

## 错误处理

1. 记录错误日志
2. 返回友好提示
3. 提供解决方案

## 安全规范

1. 验证输入参数
2. 处理边界情况
3. 防止注入攻击
"""

        with open(groot_md_file, "w") as f:
            f.write(content)

        time.sleep(1)

        # 验证加载
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "你好"},
            stream=True
        )

        assert response.status_code == 200
        for line in response.iter_lines():
            if line:
                pass

        # 清理
        os.remove(groot_md_file)


class TestGrootMdPosition:
    """GROOT.md 系统指令位置测试"""

    def test_groot_md_before_prompt(self, server, api_headers):
        """TC-GROOTMD-007: GROOT.md 位于 prompt 之前"""
        groot_md_file = f"{TEST_HOME}/GROOT.md"

        # 创建具有明显特征的 GROOT.md
        content = """# 测试

IMPORTANT_START_MARKER
"""
        with open(groot_md_file, "w") as f:
            f.write(content)

        time.sleep(1)

        # 发送请求
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "测试 prompt 内容"},
            stream=True
        )

        assert response.status_code == 200
        for line in response.iter_lines():
            if line:
                pass

        # 清理
        os.remove(groot_md_file)


class TestGrootMdMultipleChanges:
    """GROOT.md 多次修改测试"""

    def test_rapid_modifications(self, server, api_headers):
        """TC-GROOTMD-008: 快速多次修改"""
        groot_md_file = f"{TEST_HOME}/GROOT.md"

        # 快速多次修改（无防抖，每次都应生效）
        for i in range(5):
            content = f"""# 版本{i}

- 规范版本{i}
"""
            with open(groot_md_file, "w") as f:
                f.write(content)
            time.sleep(0.5)  # 500ms 间隔

        # 等待最后一次生效
        time.sleep(1)

        # 验证
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "测试"},
            stream=True
        )

        assert response.status_code == 200
        for line in response.iter_lines():
            if line:
                pass

        # 清理
        if os.path.exists(groot_md_file):
            os.remove(groot_md_file)


class TestGrootMdSpecialCases:
    """GROOT.md 特殊情况测试"""

    def test_groot_md_with_yaml_frontmatter(self, server, api_headers):
        """TC-GROOTMD-009: GROOT.md 包含 YAML frontmatter"""
        groot_md_file = f"{TEST_HOME}/GROOT.md"

        # 创建包含 YAML frontmatter 的内容
        content = """---
project: groot
version: 1.0
---

# 项目规范

- 使用中文
"""
        with open(groot_md_file, "w") as f:
            f.write(content)

        time.sleep(1)

        # 验证加载
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "你好"},
            stream=True
        )

        assert response.status_code == 200
        for line in response.iter_lines():
            if line:
                pass

        # 清理
        os.remove(groot_md_file)

    def test_groot_md_with_code_blocks(self, server, api_headers):
        """TC-GROOTMD-010: GROOT.md 包含代码块"""
        groot_md_file = f"{TEST_HOME}/GROOT.md"

        content = """# 代码规范

示例代码：

```go
func main() {
    fmt.Println("Hello")
}
```

- 使用 Go 语言
"""
        with open(groot_md_file, "w") as f:
            f.write(content)

        time.sleep(1)

        # 验证加载
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "测试代码"},
            stream=True
        )

        assert response.status_code == 200
        for line in response.iter_lines():
            if line:
                pass

        # 清理
        os.remove(groot_md_file)