package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// McpFlags holds the parsed flags for the mcp command
type McpFlags struct {
	Subcommand string // list
}

// ParseMcpFlags parses command line arguments for the mcp command
func ParseMcpFlags(args []string) (*McpFlags, error) {
	flags := &McpFlags{}

	if len(args) == 0 {
		return nil, errors.New("缺少子命令: list")
	}

	if !strings.HasPrefix(args[0], "-") {
		flags.Subcommand = args[0]
	} else {
		if args[0] == "-h" || args[0] == "--help" {
			PrintMcpHelp()
			os.Exit(0)
		}
		return nil, fmt.Errorf("缺少子命令，请使用: list")
	}

	i := 1
	for i < len(args) {
		arg := args[i]

		switch arg {
		case "-h", "--help":
			PrintMcpHelp()
			os.Exit(0)
		default:
			if strings.HasPrefix(arg, "-") {
				return nil, fmt.Errorf("unknown flag: %s", arg)
			}

			switch flags.Subcommand {
			case "list":
				return nil, fmt.Errorf("unexpected argument: %s", arg)
			}
		}
		i++
	}

	// Validate subcommand
	switch flags.Subcommand {
	case "list":
		// valid
	default:
		return nil, fmt.Errorf("未知子命令: %s (可用: list)", flags.Subcommand)
	}

	return flags, nil
}

// PrintMcpHelp prints help for the mcp command
func PrintMcpHelp() {
	fmt.Println("用法: groot mcp <子命令> [选项]")
	fmt.Println()
	fmt.Println("管理 MCP Servers 配置")
	fmt.Println()
	fmt.Println("子命令:")
	fmt.Println("  list                    列出所有已配置的 MCP Servers")
	fmt.Println()
	fmt.Println("选项:")
	fmt.Println("  -h, --help              显示帮助")
	fmt.Println()
	fmt.Println("示例:")
	fmt.Println("  groot mcp list                                 # 列出所有 MCP Servers")
}

// RunMcp is the main entry point for the mcp command
func RunMcp(flags *McpFlags) error {
	homeDir := GetDefaultHome()
	mcpDir := filepath.Join(homeDir, "mcp")

	switch flags.Subcommand {
	case "list":
		return mcpList(mcpDir)
	default:
		return fmt.Errorf("未知子命令: %s", flags.Subcommand)
	}
}

// mcpConfigBasic holds only the fields needed for listing
type mcpConfigBasic struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	IsActive    bool   `json:"isActive"`
}

type mcpItem struct {
	name        string
	mcpType     string
	status      string
	lastUpdated string
	description string
	valid       bool
}

// mcpList lists all MCP config files in the mcp directory
func mcpList(mcpDir string) error {
	entries, err := os.ReadDir(mcpDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("未配置任何 MCP Server")
			return nil
		}
		return fmt.Errorf("读取 MCP 目录失败: %w", err)
	}

	var items []mcpItem
	nameWidth := 4  // "NAME"
	typeWidth := 4  // "TYPE"
	descWidth := 11 // "DESCRIPTION"

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		configPath := filepath.Join(mcpDir, entry.Name())
		name := strings.TrimSuffix(entry.Name(), ".json")
		item := mcpItem{
			name:  name,
			valid: true,
		}

		info, err := os.Stat(configPath)
		if err != nil {
			item.valid = false
		} else {
			item.lastUpdated = info.ModTime().Format("2006-01-02 15:04")
		}

		data, err := os.ReadFile(configPath)
		if err != nil {
			item.valid = false
		} else {
			var cfg mcpConfigBasic
			if err := json.Unmarshal(data, &cfg); err != nil {
				item.valid = false
			} else {
				item.mcpType = cfg.Type
				if cfg.IsActive {
					item.status = "active"
				} else {
					item.status = "inactive"
				}
				item.description = cfg.Description
			}
		}

		if !item.valid {
			item.description = "⚠ 配置解析失败"
		}

		if len(item.name) > nameWidth {
			nameWidth = len(item.name)
		}
		if len(item.mcpType) > typeWidth {
			typeWidth = len(item.mcpType)
		}
		descRunes := []rune(item.description)
		if len(descRunes) > descWidth {
			descWidth = len(descRunes)
		}

		items = append(items, item)
	}

	if len(items) == 0 {
		fmt.Println("未配置任何 MCP Server")
		return nil
	}

	// Cap widths
	if nameWidth > 30 {
		nameWidth = 30
	}
	if typeWidth > 20 {
		typeWidth = 20
	}
	if descWidth > 60 {
		descWidth = 60
	}

	headerFmt := fmt.Sprintf("%%-%ds  %%-%ds  %%-8s  %%-19s  %%s\n", nameWidth, typeWidth)
	rowFmt := fmt.Sprintf("%%-%ds  %%-%ds  %%-8s  %%-19s  %%s\n", nameWidth, typeWidth)

	fmt.Printf(headerFmt, "NAME", "TYPE", "STATUS", "LAST_UPDATED", "DESCRIPTION")
	fmt.Printf(rowFmt, strings.Repeat("-", nameWidth), strings.Repeat("-", typeWidth), strings.Repeat("-", 8), strings.Repeat("-", 19), strings.Repeat("-", descWidth))

	activeCount := 0
	inactiveCount := 0
	invalidCount := 0

	for _, item := range items {
		desc := item.description
		descRunes := []rune(desc)
		if len(descRunes) > 60 {
			desc = string(descRunes[:57]) + "..."
		}
		fmt.Printf(rowFmt, item.name, item.mcpType, item.status, item.lastUpdated, desc)

		if !item.valid {
			invalidCount++
		} else if item.status == "active" {
			activeCount++
		} else {
			inactiveCount++
		}
	}

	fmt.Println()
	fmt.Printf("共 %d 个 MCP Server", len(items))
	parts := []string{}
	if activeCount > 0 {
		parts = append(parts, fmt.Sprintf("%d 个活跃", activeCount))
	}
	if inactiveCount > 0 {
		parts = append(parts, fmt.Sprintf("%d 个未激活", inactiveCount))
	}
	if invalidCount > 0 {
		parts = append(parts, fmt.Sprintf("%d 个异常", invalidCount))
	}
	if len(parts) > 0 {
		fmt.Printf("（%s）", strings.Join(parts, "，"))
	}
	fmt.Println()

	return nil
}
