# Init Command Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 `groot init` 子命令，将初始化和启动流程分离，提供清晰的错误提示。

**Architecture:** 新增 init 子命令创建目录结构和配置模板；修改启动流程移除自动创建配置的逻辑，增强验证并返回具体错误提示。

**Tech Stack:** Go 1.21+, 标准库 flag/os/filepath

---

## 文件结构

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/cmd/init.go` | 创建 | init 子命令实现：创建目录、生成配置模板 |
| `cmd/groot/main.go` | 修改 | 新增 init 命令分支处理 |
| `internal/config/loader.go` | 修改 | 移除自动创建逻辑，增强验证错误提示 |
| `internal/config/template.go` | 创建 | 配置模板生成函数 |
| `internal/cmd/init_test.go` | 创建 | init 命令单元测试 |
| `internal/config/loader_test.go` | 修改 | loader 验证逻辑测试 |

---

## Task 1: 创建配置模板生成函数

**Files:**
- Create: `internal/config/template.go`
- Test: `internal/config/template_test.go`

- [ ] **Step 1: 编写模板函数测试**

```go
package config

import (
	"strings"
	"testing"
)

func TestGenerateConfigTemplate(t *testing.T) {
	content := GenerateConfigTemplate()

	// 检查必要字段存在
	if !strings.Contains(content, "llm:") {
		t.Error("模板缺少 llm 配置")
	}
	if !strings.Contains(content, "default_model:") {
		t.Error("模板缺少 default_model 配置")
	}
	if !strings.Contains(content, "api_key:") {
		t.Error("模板缺少 api_key 配置")
	}
	if !strings.Contains(content, "${OPENAI_API_KEY}") {
		t.Error("模板缺少环境变量引用示例")
	}
	if !strings.Contains(content, "#") {
		t.Error("模板缺少注释说明")
	}
}
```

- [ ] **Step 2: 运行测试验证失败**

Run: `go test ./internal/config/... -run TestGenerateConfigTemplate -v`
Expected: FAIL（函数未定义）

- [ ] **Step 3: 实现模板生成函数**

```go
package config

// GenerateConfigTemplate returns a config template with helpful comments
func GenerateConfigTemplate() string {
	return `# Groot Agent 配置文件
# 请根据实际情况修改以下配置

# LLM 配置（必填）
# 请填写你的 LLM API 信息，支持 OpenAI 兼容协议
llm:
  default_model: gpt-4o           # 默认模型名称
  models:
    gpt-4o:
      base_url: https://api.openai.com/v1    # API 地址
      api_key: ${OPENAI_API_KEY}             # API 密钥（建议使用环境变量）
      model: gpt-4o                          # 模型名称
      max_completion_tokens: 4096                       # 最大 Token 数
      temperature: 0.7                       # 温度参数（0.0~2.0）
      top_p: 1.0                             # 核采样系数（0.0~1.0）
      frequency_penalty: 0.0                 # 频率惩罚（-2.0~2.0）
      presence_penalty: 0.0                  # 存在惩罚（-2.0~2.0）
      seed: 0                                # 随机种子（0 表示不设置）
      stop: []                               # 停止序列
      thinking: false                        # 深度思考模式（Qwen/DeepSeek 等模型）

# 其他配置项均有默认值，可按需修改
# 完整配置说明请参考：https://github.com/zfd81/groot
`
}
```

- [ ] **Step 4: 运行测试验证通过**

Run: `go test ./internal/config/... -run TestGenerateConfigTemplate -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/config/template.go internal/config/template_test.go
git commit -m "feat(config): add config template generator function"
```

---

## Task 2: 创建 init 命令实现

**Files:**
- Create: `internal/cmd/init.go`
- Test: `internal/cmd/init_test.go`

- [ ] **Step 1: 编写 init 命令测试**

```go
package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunInit(t *testing.T) {
	// 创建临时测试目录
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "test_groot")

	err := RunInit(homeDir)
	if err != nil {
		t.Fatalf("RunInit failed: %v", err)
	}

	// 检查目录创建
	expectedDirs := []string{"skills", "mcp", "memory", "logs", "cluster/members"}
	for _, dir := range expectedDirs {
		path := filepath.Join(homeDir, dir)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("目录 %s 未创建", dir)
		}
	}

	// 检查配置文件创建
	configPath := filepath.Join(homeDir, "config.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Errorf("配置文件未创建")
	}
}

func TestRunInitExistingDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "existing_groot")

	// 预创建目录
	os.MkdirAll(filepath.Join(homeDir, "skills"), 0755)
	os.MkdirAll(filepath.Join(homeDir, "mcp"), 0755)

	err := RunInit(homeDir)
	if err != nil {
		t.Fatalf("RunInit failed: %v", err)
	}

	// 检查所有目录仍存在
	expectedDirs := []string{"skills", "mcp", "memory", "logs", "cluster/members"}
	for _, dir := range expectedDirs {
		path := filepath.Join(homeDir, dir)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("目录 %s 不存在", dir)
		}
	}
}

func TestRunInitExistingConfig(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "config_exists")

	// 预创建配置文件
	os.MkdirAll(homeDir, 0755)
	configPath := filepath.Join(homeDir, "config.yaml")
	os.WriteFile(configPath, []byte("existing: config"), 0644)

	err := RunInit(homeDir)
	if err != nil {
		t.Fatalf("RunInit failed: %v", err)
	}

	// 检查配置文件未被覆盖
	data, _ := os.ReadFile(configPath)
	if string(data) != "existing: config" {
		t.Errorf("配置文件被覆盖了")
	}
}
```

- [ ] **Step 2: 运行测试验证失败**

Run: `go test ./internal/cmd/... -run TestRunInit -v`
Expected: FAIL（函数未定义）

- [ ] **Step 3: 实现 init 命令函数**

```go
package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zfd81/groot/internal/config"
)

// RunInit initializes the Groot working directory
func RunInit(homeDir string) error {
	fmt.Println("初始化 Groot 工作目录...")
	fmt.Println()

	// 创建工作目录根目录
	if err := createDir(homeDir, "工作目录", true); err != nil {
		return err
	}

	// 创建子目录
	subDirs := []string{"skills", "mcp", "memory", "logs", "cluster/members"}
	for _, dir := range subDirs {
		if err := createDir(filepath.Join(homeDir, dir), "目录 "+dir, false); err != nil {
			return err
		}
	}

	// 创建配置文件
	if err := createConfigFile(homeDir); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("初始化完成")
	fmt.Println()
	printNextSteps(homeDir)

	return nil
}

func createDir(path string, name string, isRoot bool) error {
	_, err := os.Stat(path)
	if err == nil {
		fmt.Printf("%s %s 已存在，跳过创建\n", name, shortenPath(path, isRoot))
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("检查目录 %s 失败: %w", path, err)
	}

	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("创建目录 %s 失败: %w", path, err)
	}

	fmt.Printf("%s %s 创建成功\n", name, shortenPath(path, isRoot))
	return nil
}

func shortenPath(path string, isRoot bool) string {
	if isRoot {
		home := os.Getenv("HOME")
		if home != "" && filepath.HasPrefix(path, home) {
			return "~" + path[len(home):]
		}
	}
	return path
}

func createConfigFile(homeDir string) error {
	configPath := filepath.Join(homeDir, "config.yaml")

	_, err := os.Stat(configPath)
	if err == nil {
		fmt.Println("配置文件 config.yaml 已存在，跳过创建")
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("检查配置文件失败: %w", err)
	}

	template := config.GenerateConfigTemplate()
	if err := os.WriteFile(configPath, []byte(template), 0644); err != nil {
		return fmt.Errorf("创建配置文件失败: %w", err)
	}

	fmt.Println("配置文件 config.yaml 创建成功")
	return nil
}

func printNextSteps(homeDir string) {
	shortPath := shortenPath(homeDir, true)
	fmt.Println("下一步：")
	fmt.Println("  1. 编辑配置文件，填写 LLM API 信息")
	fmt.Printf("     vim %s/config.yaml\n", shortPath)
	fmt.Println("  2. 设置环境变量（如果配置文件使用了 ${VAR_NAME}）")
	fmt.Println("     export OPENAI_API_KEY=\"your-api-key\"")
	fmt.Println("  3. 启动服务")
	fmt.Println("     groot")
}
```

- [ ] **Step 4: 运行测试验证通过**

Run: `go test ./internal/cmd/... -run TestRunInit -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/cmd/init.go internal/cmd/init_test.go
git commit -m "feat(cmd): add init command implementation"
```

---

## Task 3: 修改 main.go 新增 init 命令处理

**Files:**
- Modify: `cmd/groot/main.go`

- [ ] **Step 1: 在 switch 分支新增 init 命令**

在 `main.go` 的 switch 分支中，找到 `case "tail"`，在其前面添加 `case "init"`:

```go
		case "init":
			handleInitCommand(args[1:])
			return
		case "tail":
			handleTailCommand(args[1:])
```

- [ ] **Step 2: 新增 handleInitCommand 函数**

在 `handleTailCommand` 函数后面添加：

```go
func handleInitCommand(args []string) {
	// Parse init-specific flags
	initFlags := flag.NewFlagSet("init", flag.ExitOnError)
	var initHomeDir string
	initFlags.StringVar(&initHomeDir, "H", "", "工作目录 (默认 ~/.groot)")
	initFlags.StringVar(&initHomeDir, "home", "", "工作目录 (默认 ~/.groot)")
	var initHelp bool
	initFlags.BoolVar(&initHelp, "h", false, "显示帮助")
	initFlags.BoolVar(&initHelp, "help", false, "显示帮助")

	if err := initFlags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", err)
		os.Exit(1)
	}

	if initHelp {
		printInitHelp()
		return
	}

	// Determine home directory
	if initHomeDir == "" {
		initHomeDir = os.Getenv("GROOT_HOME")
		if initHomeDir == "" {
			initHomeDir = filepath.Join(os.Getenv("HOME"), ".groot")
		}
	}

	if err := cmd.RunInit(initHomeDir); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", err)
		os.Exit(1)
	}
}

func printInitHelp() {
	fmt.Println("用法: groot init [选项]")
	fmt.Println()
	fmt.Println("初始化 Groot 工作目录和配置文件")
	fmt.Println()
	fmt.Println("选项:")
	fmt.Println("  -h, --help        显示帮助")
	fmt.Println()
	fmt.Println("示例:")
	fmt.Println("  groot init                    # 初始化默认目录 ~/.groot")
	fmt.Println("  groot init -H /opt/groot      # 初始化指定目录")
}
```

- [ ] **Step 3: 在 printHelp 中添加 init 命令说明**

在 `printHelp` 函数的子命令列表中添加 init：

```go
	fmt.Println("子命令:")
	fmt.Println("  init              初始化工作目录")
	fmt.Println("  tail              实时日志查看")
```

- [ ] **Step 4: 运行测试验证命令可用**

Run: `go run ./cmd/groot init -h`
Expected: 显示 init 命令帮助信息

- [ ] **Step 5: 提交**

```bash
git add cmd/groot/main.go
git commit -m "feat(cli): add init subcommand to main"
```

---

## Task 4: 修改 loader.go 移除自动创建配置逻辑

**Files:**
- Modify: `internal/config/loader.go`
- Modify: `internal/config/loader_test.go`

- [ ] **Step 1: 修改 Load 函数，配置不存在时返回错误**

修改 `loader.go` 的 `Load` 函数开头部分：

```go
func Load(homeDir string) (*Config, error) {
	configPath := filepath.Join(homeDir, "config.yaml")

	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("配置文件不存在，请先运行 'groot init' 初始化\n\n提示: groot init -H %s", homeDir)
	}
```

删除原来的 `generateDefaultConfig` 调用逻辑。

- [ ] **Step 2: 编写测试验证错误提示**

在 `config_test.go` 中添加：

```go
func TestLoadConfigNotExists(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "no_config")

	_, err := Load(homeDir)
	if err == nil {
		t.Fatal("配置不存在时应返回错误")
	}

	// 检查错误信息包含提示
	if !strings.Contains(err.Error(), "groot init") {
		t.Errorf("错误信息应包含 'groot init' 提示: %s", err.Error())
	}
}
```

- [ ] **Step 3: 运行测试验证通过**

Run: `go test ./internal/config/... -run TestLoadConfigNotExists -v`
Expected: PASS

- [ ] **Step 4: 提交**

```bash
git add internal/config/loader.go internal/config/loader_test.go
git commit -m "feat(config): remove auto-create config, return init hint error"
```

---

## Task 5: 增强 LLM 配置验证错误提示

**Files:**
- Modify: `internal/config/loader.go`
- Modify: `internal/config/config.go`（验证函数）
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: 增强验证函数返回详细错误**

修改 `config.go` 的 `ValidateLLMConfig` 函数，使其检查更多场景：

```go
// ValidateLLMConfig validates LLM configuration at startup.
// Returns detailed error messages to help users fix configuration issues.
func ValidateLLMConfig(cfg *LLMConfig) error {
	if len(cfg.Models) == 0 {
		return fmt.Errorf("LLM models 配置为空，请编辑 config.yaml 添加模型配置")
	}

	if cfg.DefaultModel == "" {
		// Use first model as default if not specified
		for name := range cfg.Models {
			cfg.DefaultModel = name
			break
		}
	}

	if !cfg.ValidateModel(cfg.DefaultModel) {
		return fmt.Errorf("default_model '%s' 不存在于 models 配置中", cfg.DefaultModel)
	}

	// Check each model's configuration
	for name, model := range cfg.Models {
		if model.BaseURL == "" {
			return fmt.Errorf("模型 %s 的 base_url 为空，请编辑 config.yaml", name)
		}
		if model.APIKey == "" {
			return fmt.Errorf("模型 %s 的 api_key 为空，请编辑 config.yaml 或设置对应的环境变量", name)
		}
		// Check if APIKey is an env var reference that's not set
		if strings.HasPrefix(model.APIKey, "${") && strings.HasSuffix(model.APIKey, "}") {
			envVar := model.APIKey[2:len(model.APIKey)-1]
			if os.Getenv(envVar) == "" {
				return fmt.Errorf("环境变量 %s 未设置，请设置后重试\n\n提示: export %s=\"your-api-key\"\n      或在 config.yaml 中直接填写 api_key", envVar, envVar)
			}
		}
	}

	return nil
}
```

- [ ] **Step 2: 编写验证错误测试**

在 `config_test.go` 中添加：

```go
func TestValidateLLMConfigEmptyModels(t *testing.T) {
	cfg := &LLMConfig{Models: map[string]ModelConfig{}}
	err := ValidateLLMConfig(cfg)
	if err == nil {
		t.Fatal("空 models 应返回错误")
	}
	if !strings.Contains(err.Error(), "models 配置为空") {
		t.Errorf("错误信息不符合预期: %s", err.Error())
	}
}

func TestValidateLLMConfigEmptyAPIKey(t *testing.T) {
	cfg := &LLMConfig{
		DefaultModel: "gpt-4o",
		Models: map[string]ModelConfig{
			"gpt-4o": {BaseURL: "https://api.openai.com/v1", APIKey: ""},
		},
	}
	err := ValidateLLMConfig(cfg)
	if err == nil {
		t.Fatal("空 api_key 应返回错误")
	}
	if !strings.Contains(err.Error(), "api_key 为空") {
		t.Errorf("错误信息不符合预期: %s", err.Error())
	}
}

func TestValidateLLMConfigEnvVarNotSet(t *testing.T) {
	// 确保环境变量未设置
	os.Unsetenv("TEST_API_KEY_FOR_UNIT_TEST")

	cfg := &LLMConfig{
		DefaultModel: "gpt-4o",
		Models: map[string]ModelConfig{
			"gpt-4o": {BaseURL: "https://api.openai.com/v1", APIKey: "${TEST_API_KEY_FOR_UNIT_TEST}"},
		},
	}
	err := ValidateLLMConfig(cfg)
	if err == nil {
		t.Fatal("环境变量未设置应返回错误")
	}
	if !strings.Contains(err.Error(), "TEST_API_KEY_FOR_UNIT_TEST 未设置") {
		t.Errorf("错误信息不符合预期: %s", err.Error())
	}
}
```

- [ ] **Step 3: 运行测试验证通过**

Run: `go test ./internal/config/... -v`
Expected: PASS

- [ ] **Step 4: 提交**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): enhance LLM config validation with detailed errors"
```

---

## Task 6: 集成测试 - 验证完整流程

**Files:**
- 无新增，验证整体功能

- [ ] **Step 1: 编译程序**

Run: `go build -o bin/groot ./cmd/groot`
Expected: 编译成功

- [ ] **Step 2: 测试 init 命令**

Run: `./bin/groot init -H /tmp/test_groot_init`
Expected: 输出初始化完成信息，目录和配置文件创建成功

- [ ] **Step 3: 测试启动时配置不存在**

Run: `./bin/groot -H /tmp/no_groot_dir`
Expected: 输出错误提示"配置文件不存在，请先运行 'groot init' 初始化"

- [ ] **Step 4: 测试启动时环境变量未设置**

先运行 init 创建配置，然后不设置环境变量启动：

Run: 
```bash
./bin/groot init -H /tmp/test_env_var
./bin/groot -H /tmp/test_env_var
```
Expected: 输出错误提示"环境变量 OPENAI_API_KEY 未设置"

- [ ] **Step 5: 运行所有单元测试**

Run: `go test ./internal/... -v`
Expected: PASS

- [ ] **Step 6: 提交（如需要）**

如果有任何修改：

```bash
git add -A
git commit -m "test: verify init command and startup flow"
```

---

## 完成检查

- [ ] 所有单元测试通过
- [ ] `groot init` 命令正常工作
- [ ] `groot` 启动时配置不存在给出正确提示
- [ ] `groot` 启动时环境变量未设置给出正确提示