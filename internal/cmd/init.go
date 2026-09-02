package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zfd81/groot/internal/config"
)

// InitFlags holds the parsed flags for the init command
type InitFlags struct {
}

// ParseInitFlags parses command line arguments for the init command
// args should be the arguments after "init" subcommand (e.g., ["-H", "/opt/groot"])
func ParseInitFlags(args []string) (*InitFlags, error) {
	flags := &InitFlags{}

	i := 0
	for i < len(args) {
		arg := args[i]

		switch arg {
		case "-h", "--help":
			PrintInitHelp()
			os.Exit(0)

		default:
			if strings.HasPrefix(arg, "-") {
				return nil, fmt.Errorf("unknown flag: %s", arg)
			}
			// Unknown positional argument
			return nil, fmt.Errorf("unexpected argument: %s", arg)
		}
		i++
	}

	return flags, nil
}

// PrintInitHelp prints the help message for the init command
func PrintInitHelp() {
	fmt.Println("用法: groot init [选项]")
	fmt.Println()
	fmt.Println("初始化 Groot 工作目录和配置文件")
	fmt.Println()
	fmt.Println("选项:")
	fmt.Println("  -h, --help        显示帮助")
	fmt.Println()
	fmt.Println("示例:")
	fmt.Println("  groot init                    # 初始化默认目录 ~/.groot")
}

// RunInit initializes the Groot working directory
func RunInit(homeDir string) error {
	fmt.Println("初始化 Groot 工作目录...")
	fmt.Println()

	// 创建工作目录根目录
	if err := createDir(homeDir, "工作目录", true); err != nil {
		return err
	}

	// 创建子目录
	// memory / schedules / cluster 等运行时数据已迁入数据库（SQLite/MySQL/PG），
	// 不再创建对应目录。仅保留资源类目录（skills/mcp/subagents）和日志目录。
	subDirs := []string{"skills", "mcp", "subagents", "logs"}
	for _, dir := range subDirs {
		if err := createDir(filepath.Join(homeDir, dir), "目录 "+dir, false); err != nil {
			return err
		}
	}

	// 创建配置文件
	if err := createConfigFile(homeDir); err != nil {
		return err
	}

	// 创建环境配置文件 env.yaml（基础设施凭据，默认全注释 → local 模式）
	if err := createEnvFile(homeDir); err != nil {
		return err
	}

	// 创建默认 GROOT.md（含子 Agent 调度引导段）
	if err := createGrootMdFile(homeDir); err != nil {
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
		if home != "" && strings.HasPrefix(path, home) {
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

	secret, err := config.GenerateAuthSecret()
	if err != nil {
		return fmt.Errorf("生成认证密钥失败: %w", err)
	}
	template := config.GenerateConfigTemplate(secret)
	// config.yaml 含 JWT 签名密钥，权限 0600（仅当前用户可读写），看齐 env.yaml 的凭据文件标准
	if err := os.WriteFile(configPath, []byte(template), 0600); err != nil {
		return fmt.Errorf("创建配置文件失败: %w", err)
	}

	fmt.Println("配置文件 config.yaml 创建成功")
	return nil
}

// createEnvFile 在 homeDir 写入 env.yaml；已存在则跳过避免覆盖用户填好的凭据。
// 默认内容**全注释**，等价于本地磁盘存储模式（零配置）。
func createEnvFile(homeDir string) error {
	envPath := filepath.Join(homeDir, config.EnvFileName)

	_, err := os.Stat(envPath)
	if err == nil {
		fmt.Println("环境配置文件 env.yaml 已存在，跳过创建")
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("检查环境配置文件失败: %w", err)
	}

	if err := os.WriteFile(envPath, []byte(config.GenerateEnvTemplate()), 0600); err != nil {
		return fmt.Errorf("创建环境配置文件失败: %w", err)
	}

	fmt.Println("环境配置文件 env.yaml 创建成功")
	return nil
}

// defaultGrootMdContent 是 groot init 写入的默认 GROOT.md 内容；
// 末尾「子 Agent 调度」段引导主 Agent 在拥有 call_agent 工具时如何使用子 Agent。
const defaultGrootMdContent = `# GROOT.md

本文件是主 Agent 的全局指导，每次对话都会作为 system 提示注入。请在此处填写你想让主 Agent 始终遵守的规则、风格、目标等。

## 子 Agent 调度

当你拥有 ` + "`call_agent`" + ` 工具时，意味着系统中注册了一些专门的子 Agent。请遵循：

- **按需调用**：只在子 Agent 的 description 与子任务匹配时才调用
- **逐个调用**：建议先调一个，确认返回足够信息后再决定是否调下一个；避免盲目并行
- **明确传参**：task 参数必须包含完整上下文，因为子 Agent 看不到主对话历史
- **附件引用**：如需子 Agent 访问附件，在 task 中显式写明附件路径
`

// createGrootMdFile 在 homeDir 下写入默认 GROOT.md；已存在则跳过避免覆盖用户自定义内容。
func createGrootMdFile(homeDir string) error {
	path := filepath.Join(homeDir, "GROOT.md")

	_, err := os.Stat(path)
	if err == nil {
		fmt.Println("GROOT.md 已存在，跳过创建")
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("检查 GROOT.md 失败: %w", err)
	}

	if err := os.WriteFile(path, []byte(defaultGrootMdContent), 0644); err != nil {
		return fmt.Errorf("创建 GROOT.md 失败: %w", err)
	}

	fmt.Println("GROOT.md 创建成功")
	return nil
}

func printNextSteps(homeDir string) {
	shortPath := shortenPath(homeDir, true)
	fmt.Println("下一步：")
	fmt.Println("  1. 编辑配置文件，填写 LLM API 信息")
	fmt.Printf("     vim %s/config.yaml\n", shortPath)
	fmt.Println("  2. 设置环境变量（如果配置文件使用了 ${VAR_NAME}）")
	fmt.Println("     export OPENAI_API_KEY=\"your-api-key\"")
	fmt.Println("  3. （可选）启用数据库后端：编辑环境配置文件")
	fmt.Printf("     vim %s/env.yaml   # 默认全注释 → SQLite 本地模式\n", shortPath)
	fmt.Println("  4. 启动服务")
	fmt.Println("     groot")
}
