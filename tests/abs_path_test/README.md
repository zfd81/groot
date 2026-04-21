# 绝对路径测试目录

此目录用于测试目录路径配置的绝对路径功能。

## 目录结构

| 目录 | 用途 |
|------|------|
| `logs/` | 绝对路径日志测试 |
| `memory/` | 绝对路径 memory 测试 |
| `temp/` | 绝对路径附件临时文件测试 |
| `groot_home/` | 测试时的 GROOT_HOME（运行时自动创建） |

## 测试配置

在 `test_path_config.py` 的 `abs_path_server` fixture 中：
- `skills.directory`: 使用相对路径 `skills`（相对于 groot_home）
- `mcp.directory`: 使用相对路径 `mcp`（相对于 groot_home）
- `memory.directory`: 使用绝对路径 `tests/abs_path_test/memory`
- `logging.file.directory`: 使用绝对路径 `tests/abs_path_test/logs`
- `attachment.temp_directory`: 使用绝对路径 `tests/abs_path_test/temp`