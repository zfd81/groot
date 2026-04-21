#!/bin/bash
# Groot API 测试运行脚本
# 使用 pytest 运行所有测试

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 测试目录
TEST_DIR="$(cd "$(dirname "$0")" && pwd)"

# 默认配置
GROOT_HOST="${GROOT_TEST_HOST:-localhost}"
GROOT_PORT="${GROOT_TEST_PORT:-8080}"
GROOT_API_KEY="${GROOT_TEST_API_KEY:-test-api-key-2026}"
GROOT_HOME="${GROOT_TEST_HOME:-/tmp/groot_test}"

echo "================================================"
echo "  Groot API 测试"
echo "================================================"
echo ""
echo "测试环境配置:"
echo "  GROOT_HOST:      $GROOT_HOST"
echo "  GROOT_PORT:      $GROOT_PORT"
echo "  GROOT_API_KEY:   $GROOT_API_KEY"
echo "  GROOT_HOME:      $GROOT_HOME"
echo ""

# 设置环境变量
export GROOT_TEST_HOST="$GROOT_HOST"
export GROOT_TEST_PORT="$GROOT_PORT"
export GROOT_TEST_API_KEY="$GROOT_API_KEY"
export GROOT_TEST_HOME="$GROOT_HOME"

# 创建测试目录
mkdir -p "$GROOT_HOME"
mkdir -p "$GROOT_HOME/skills"
mkdir -p "$GROOT_HOME/mcp"
mkdir -p "$GROOT_HOME/memory"
mkdir -p "$GROOT_HOME/logs"

# 检查 pytest 是否安装
if ! command -v pytest &> /dev/null; then
    echo -e "${YELLOW}pytest 未安装，正在安装...${NC}"
    pip install pytest pytest-asyncio requests pyyaml
fi

# 检查服务是否运行
echo "检查服务状态..."
HEALTH_URL="http://$GROOT_HOST:$GROOT_PORT/health"

if curl -s --connect-timeout 5 "$HEALTH_URL" > /dev/null 2>&1; then
    echo -e "${GREEN}服务已运行${NC}"
else
    echo -e "${YELLOW}服务未运行，请先启动 Groot 服务${NC}"
    echo ""
    echo "启动服务示例:"
    echo "  export GROOT_HOME=$GROOT_HOME"
    echo "  export GROOT_API_KEY=$GROOT_API_KEY"
    echo "  groot -H $GROOT_HOME -p $GROOT_PORT"
    echo ""
    read -p "是否继续测试？测试将尝试启动服务。 [y/N] " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        exit 1
    fi
fi

# 运行测试
echo ""
echo "================================================"
echo "  开始运行测试"
echo "================================================"
echo ""

# 测试参数
PYTEST_ARGS=""

# 是否生成覆盖率报告
if [ "$1" == "--coverage" ]; then
    PYTEST_ARGS="$PYTEST_ARGS --cov=. --cov-report=html --cov-report=term"
    pip install pytest-cov
fi

# 是否只运行特定测试
if [ "$1" != "" ] && [ "$1" != "--coverage" ]; then
    PYTEST_ARGS="$PYTEST_ARGS -k $1"
    echo -e "${YELLOW}只运行匹配 '$1' 的测试${NC}"
fi

# 是否详细输出
VERBOSE="${VERBOSE:-false}"
if [ "$VERBOSE" == "true" ]; then
    PYTEST_ARGS="$PYTEST_ARGS -v"
fi

# 运行 pytest
cd "$TEST_DIR"

pytest $PYTEST_ARGS \
    --tb=short \
    --strict-markers \
    -rA \
    tests/

# 测试完成
echo ""
echo "================================================"
echo -e "${GREEN}  测试完成${NC}"
echo "================================================"
echo ""

# 清理（可选）
read -p "是否清理测试目录 $GROOT_HOME？[y/N] " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    rm -rf "$GROOT_HOME"
    echo "测试目录已清理"
fi