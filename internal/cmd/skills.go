package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SkillsFlags holds the parsed flags for the skills command
type SkillsFlags struct {
	Subcommand string // list, install, uninstall
	Path       string // source path for install
	Name       string // skill name for uninstall
}

// ParseSkillsFlags parses command line arguments for the skills command
func ParseSkillsFlags(args []string) (*SkillsFlags, error) {
	flags := &SkillsFlags{}

	if len(args) == 0 {
		return nil, errors.New("缺少子命令: list, install, uninstall")
	}

	// First positional argument is the subcommand
	if !strings.HasPrefix(args[0], "-") {
		flags.Subcommand = args[0]
	} else {
		if args[0] == "-h" || args[0] == "--help" {
			PrintSkillsHelp()
			os.Exit(0)
		}
		return nil, fmt.Errorf("缺少子命令，请使用: list, install, uninstall")
	}

	i := 1
	for i < len(args) {
		arg := args[i]

		switch arg {
		case "-h", "--help":
			PrintSkillsHelp()
			os.Exit(0)
		default:
			if strings.HasPrefix(arg, "-") {
				return nil, fmt.Errorf("unknown flag: %s", arg)
			}

			switch flags.Subcommand {
			case "install":
				if flags.Path != "" {
					return nil, errors.New("install 子命令只接受一个路径参数")
				}
				flags.Path = arg
			case "uninstall":
				if flags.Name != "" {
					return nil, errors.New("uninstall 子命令只接受一个名称参数")
				}
				flags.Name = arg
			case "list":
				return nil, fmt.Errorf("unexpected argument: %s", arg)
			}
		}
		i++
	}

	// Validate subcommand
	switch flags.Subcommand {
	case "list", "install", "uninstall":
		// valid
	default:
		return nil, fmt.Errorf("未知子命令: %s (可用: list, install, uninstall)", flags.Subcommand)
	}

	// Validate required args
	if flags.Subcommand == "install" && flags.Path == "" {
		return nil, errors.New("install 子命令需要指定 Skill 路径")
	}
	if flags.Subcommand == "uninstall" && flags.Name == "" {
		return nil, errors.New("uninstall 子命令需要指定 Skill 名称")
	}

	return flags, nil
}

// PrintSkillsHelp prints help for the skills command
func PrintSkillsHelp() {
	fmt.Println("用法: groot skills <子命令> [选项]")
	fmt.Println()
	fmt.Println("管理 Groot Skills")
	fmt.Println()
	fmt.Println("子命令:")
	fmt.Println("  list                    列出所有已安装的 Skills")
	fmt.Println("  install <path>          安装 Skill（支持绝对/相对路径）")
	fmt.Println("  uninstall <name>        卸载 Skill")
	fmt.Println()
	fmt.Println("选项:")
	fmt.Println("  -h, --help              显示帮助")
	fmt.Println()
	fmt.Println("示例:")
	fmt.Println("  groot skills list                            # 列出所有 Skills")
	fmt.Println("  groot skills install /home/user/my-skill     # 安装 Skill（绝对路径）")
	fmt.Println("  groot skills install ./my-skill              # 安装 Skill（相对路径）")
	fmt.Println("  groot skills uninstall my-skill              # 卸载 Skill")
}

// RunSkills is the main entry point for the skills command
func RunSkills(flags *SkillsFlags) error {
	homeDir := GetDefaultHome()
	skillsDir := filepath.Join(homeDir, "skills")

	switch flags.Subcommand {
	case "list":
		return skillsList(skillsDir)
	case "install":
		return skillsInstall(skillsDir, flags.Path)
	case "uninstall":
		return skillsUninstall(skillsDir, flags.Name)
	default:
		return fmt.Errorf("未知子命令: %s", flags.Subcommand)
	}
}

// skillsList lists all skills in the skills directory
func skillsList(skillsDir string) error {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("未安装任何 Skill")
			return nil
		}
		return fmt.Errorf("读取 Skills 目录失败: %w", err)
	}

	var items []skillItem
	nameWidth := 4  // "NAME"
	descWidth := 11 // "DESCRIPTION"

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillPath := filepath.Join(skillsDir, entry.Name())
		skillMdPath := filepath.Join(skillPath, "SKILL.md")

		name := entry.Name()
		description := ""
		valid := true
		var lastUpdated string

		info, err := os.Stat(skillMdPath)
		if err != nil {
			valid = false
		} else {
			lastUpdated = info.ModTime().Format("2006-01-02 15:04")
			desc, err := readSkillDescription(skillMdPath)
			if err != nil {
				valid = false
			} else {
				description = desc
			}
		}

		if !valid {
			description = "⚠ 缺少 SKILL.md"
		}

		if len(name) > nameWidth {
			nameWidth = len(name)
		}
		descRunes := []rune(description)
		if len(descRunes) > descWidth {
			descWidth = len(descRunes)
		}

		items = append(items, skillItem{name: name, description: description, valid: valid, lastUpdated: lastUpdated})
	}

	if len(items) == 0 {
		fmt.Println("未安装任何 Skill")
		return nil
	}

	// Cap name width at 30, desc width at 60
	if nameWidth > 30 {
		nameWidth = 30
	}
	if descWidth > 60 {
		descWidth = 60
	}

	headerFmt := fmt.Sprintf("%%-%ds  %%-16s  %%s\n", nameWidth)
	rowFmt := fmt.Sprintf("%%-%ds  %%-16s  %%s\n", nameWidth)

	fmt.Printf(headerFmt, "NAME", "LAST_UPDATED", "DESCRIPTION")
	fmt.Printf(rowFmt, strings.Repeat("-", nameWidth), strings.Repeat("-", 16), strings.Repeat("-", descWidth))

	validCount := 0
	for _, item := range items {
		desc := item.description
		descRunes := []rune(desc)
		if len(descRunes) > 60 {
			desc = string(descRunes[:57]) + "..."
		}
		fmt.Printf(rowFmt, item.name, item.lastUpdated, desc)
		if item.valid {
			validCount++
		}
	}

	fmt.Println()
	fmt.Printf("共 %d 个 Skill", len(items))
	if len(items)-validCount > 0 {
		fmt.Printf("（%d 个有效，%d 个异常）", validCount, len(items)-validCount)
	}
	fmt.Println()

	return nil
}

type skillItem struct {
	name        string
	description string
	valid       bool
	lastUpdated string
}

// readSkillDescription reads the description from a SKILL.md frontmatter
func readSkillDescription(skillMdPath string) (string, error) {
	data, err := os.ReadFile(skillMdPath)
	if err != nil {
		return "", err
	}

	content := string(data)
	if !strings.HasPrefix(content, "---") {
		return "", nil
	}

	// Find closing ---
	end := strings.Index(content[3:], "\n---")
	if end == -1 {
		return "", nil
	}

	frontmatter := content[3 : end+3]

	// Extract description field
	for _, line := range strings.Split(frontmatter, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "description:") {
			desc := strings.TrimSpace(line[len("description:"):])
			// Remove surrounding quotes if present
			desc = strings.Trim(desc, "\"'")
			return desc, nil
		}
	}

	return "", nil
}

// skillsInstall copies a skill directory to the skills directory
func skillsInstall(skillsDir string, srcPath string) error {
	// Resolve relative paths
	if !filepath.IsAbs(srcPath) {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("获取当前工作目录失败: %w", err)
		}
		srcPath = filepath.Join(cwd, srcPath)
	}

	// Check source exists
	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("源路径不存在: %s", srcPath)
		}
		return fmt.Errorf("读取源路径失败: %w", err)
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("源路径不是目录: %s", srcPath)
	}

	// Check SKILL.md exists
	skillMdPath := filepath.Join(srcPath, "SKILL.md")
	if _, err := os.Stat(skillMdPath); os.IsNotExist(err) {
		return fmt.Errorf("源目录中缺少 SKILL.md 文件: %s", srcPath)
	}

	skillName := filepath.Base(srcPath)
	destPath := filepath.Join(skillsDir, skillName)

	// Remove existing if present
	if _, err := os.Stat(destPath); err == nil {
		if err := os.RemoveAll(destPath); err != nil {
			return fmt.Errorf("删除现有 Skill 失败: %w", err)
		}
	}

	// Copy directory
	if err := copyDir(srcPath, destPath); err != nil {
		return fmt.Errorf("拷贝 Skill 失败: %w", err)
	}

	fmt.Printf("Skill \"%s\" 安装成功\n", skillName)
	fmt.Printf("路径: %s\n", destPath)
	return nil
}

// skillsUninstall removes a skill directory from the skills directory
func skillsUninstall(skillsDir string, name string) error {
	skillPath := filepath.Join(skillsDir, name)

	info, err := os.Stat(skillPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("Skill \"%s\" 不存在", name)
		}
		return fmt.Errorf("读取 Skill 目录失败: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("\"%s\" 不是有效的 Skill 目录", name)
	}

	if err := os.RemoveAll(skillPath); err != nil {
		return fmt.Errorf("删除 Skill 失败: %w", err)
	}

	fmt.Printf("Skill \"%s\" 已卸载\n", name)
	return nil
}

// copyDir recursively copies a directory from src to dst
func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}

// copyFile copies a single file from src to dst
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	// Preserve file permissions
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.Chmod(dst, srcInfo.Mode())
}
