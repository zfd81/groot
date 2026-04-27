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
	subDirs := []string{"skills", "mcp", "memory", "logs"}
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