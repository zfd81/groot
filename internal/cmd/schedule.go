package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zfd81/groot/internal/schedule"
)

// ScheduleFlags holds the parsed flags for the schedule command
type ScheduleFlags struct {
	Subcommand string // list, inspect, history, delete, disable, enable, archive
	TaskID     string // for inspect, history, delete, disable, enable, archive
}

// ParseScheduleFlags parses command line arguments for the schedule command
func ParseScheduleFlags(args []string) (*ScheduleFlags, error) {
	flags := &ScheduleFlags{}

	if len(args) == 0 {
		return nil, errors.New("缺少子命令: list, inspect, history, delete, disable, enable, archive")
	}

	if !strings.HasPrefix(args[0], "-") {
		flags.Subcommand = args[0]
	} else {
		if args[0] == "-h" || args[0] == "--help" {
			PrintScheduleHelp()
			os.Exit(0)
		}
		return nil, fmt.Errorf("缺少子命令，请使用: list, inspect, history, delete, disable, enable, archive")
	}

	i := 1
	for i < len(args) {
		arg := args[i]

		switch arg {
		case "-h", "--help":
			PrintScheduleHelp()
			os.Exit(0)
		default:
			if strings.HasPrefix(arg, "-") {
				return nil, fmt.Errorf("unknown flag: %s", arg)
			}

			switch flags.Subcommand {
			case "list":
				return nil, fmt.Errorf("unexpected argument: %s (list 不需要参数)", arg)
			case "inspect", "history", "delete", "disable", "enable", "archive":
				if flags.TaskID == "" {
					flags.TaskID = arg
				} else {
					return nil, fmt.Errorf("unexpected argument: %s", arg)
				}
			}
		}
		i++
	}

	// Validate subcommand
	switch flags.Subcommand {
	case "list", "inspect", "history", "delete", "disable", "enable", "archive":
		// valid
	default:
		return nil, fmt.Errorf("未知子命令: %s (可用: list, inspect, history, delete, disable, enable, archive)", flags.Subcommand)
	}

	// Validate required args
	switch flags.Subcommand {
	case "inspect", "history", "delete", "disable", "enable", "archive":
		if flags.TaskID == "" {
			return nil, fmt.Errorf("缺少 task_id")
		}
	}

	return flags, nil
}

// PrintScheduleHelp prints help for the schedule command
func PrintScheduleHelp() {
	fmt.Println("用法: groot schedule <子命令> [选项]")
	fmt.Println()
	fmt.Println("管理定时任务")
	fmt.Println()
	fmt.Println("子命令:")
	fmt.Println("  list                    列出所有定时任务")
	fmt.Println("  inspect <task_id>       查看任务详情")
	fmt.Println("  history <task_id>       查看任务执行历史")
	fmt.Println("  delete <task_id>        删除任务")
	fmt.Println("  disable <task_id>       禁用任务")
	fmt.Println("  enable <task_id>        启用任务")
	fmt.Println("  archive <task_id>       归档任务")
	fmt.Println()
	fmt.Println("选项:")
	fmt.Println("  -h, --help              显示帮助")
	fmt.Println()
	fmt.Println("示例:")
	fmt.Println("  groot schedule list                      # 列出所有任务")
	fmt.Println("  groot schedule inspect task-xxx          # 查看任务详情")
	fmt.Println("  groot schedule history task-xxx          # 查看执行历史")
}

// RunSchedule is the main entry point for the schedule command
func RunSchedule(flags *ScheduleFlags) error {
	homeDir := GetDefaultHome()
	scheduleDir := filepath.Join(homeDir, "schedules")

	switch flags.Subcommand {
	case "list":
		return scheduleList(scheduleDir)
	case "inspect":
		return scheduleInspect(scheduleDir, flags.TaskID)
	case "history":
		return scheduleHistory(scheduleDir, flags.TaskID)
	case "delete":
		return scheduleDelete(scheduleDir, flags.TaskID)
	case "disable":
		return scheduleMove(scheduleDir, flags.TaskID, "active", "disabled", "禁用")
	case "enable":
		return scheduleMove(scheduleDir, flags.TaskID, "disabled", "active", "启用")
	case "archive":
		return scheduleArchive(scheduleDir, flags.TaskID)
	default:
		return fmt.Errorf("未知子命令: %s", flags.Subcommand)
	}
}

type taskSummary struct {
	ID       string
	Name     string
	Schedule string
	Status   string
}

// scheduleList lists all tasks across active/disabled/archive directories
func scheduleList(scheduleDir string) error {
	allTasks := make([]taskSummary, 0)

	for _, status := range []string{"active", "disabled", "archive"} {
		dir := filepath.Join(scheduleDir, status)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var task schedule.Task
			if err := json.Unmarshal(data, &task); err != nil {
				continue
			}
			allTasks = append(allTasks, taskSummary{
				ID:       task.ID,
				Name:     task.Name,
				Schedule: task.Schedule,
				Status:   status,
			})
		}
	}

	if len(allTasks) == 0 {
		fmt.Println("没有定时任务")
		return nil
	}

	sort.Slice(allTasks, func(i, j int) bool {
		return allTasks[i].ID < allTasks[j].ID
	})

	fmt.Printf("%-40s  %-30s  %-20s  %-10s\n", "ID", "NAME", "SCHEDULE", "STATUS")
	fmt.Printf("%-40s  %-30s  %-20s  %-10s\n",
		strings.Repeat("-", 40), strings.Repeat("-", 30), strings.Repeat("-", 20), strings.Repeat("-", 10))

	for _, t := range allTasks {
		fmt.Printf("%-40s  %-30s  %-20s  %-10s\n", truncate(t.ID, 40), truncate(t.Name, 30), truncate(t.Schedule, 20), t.Status)
	}

	activeCount := 0
	disabledCount := 0
	archiveCount := 0
	for _, t := range allTasks {
		switch t.Status {
		case "active":
			activeCount++
		case "disabled":
			disabledCount++
		case "archive":
			archiveCount++
		}
	}
	fmt.Println()
	fmt.Printf("共 %d 个任务（活跃: %d, 禁用: %d, 归档: %d）\n", len(allTasks), activeCount, disabledCount, archiveCount)
	return nil
}

// scheduleInspect prints task details
func scheduleInspect(scheduleDir, taskID string) error {
	task, _, err := findTask(scheduleDir, taskID)
	if err != nil {
		return err
	}

	data, _ := json.MarshalIndent(task, "", "  ")
	fmt.Println(string(data))
	return nil
}

// scheduleHistory prints execution history
func scheduleHistory(scheduleDir, taskID string) error {
	recordPath := filepath.Join(scheduleDir, "executions", taskID+".json")
	data, err := os.ReadFile(recordPath)
	if err != nil {
		return fmt.Errorf("没有找到任务 %s 的执行记录", taskID)
	}

	var records []schedule.ExecutionRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return fmt.Errorf("读取执行记录失败: %w", err)
	}

	if len(records) == 0 {
		fmt.Println("暂无执行记录")
		return nil
	}

	fmt.Printf("%-20s  %-15s  %-10s  %-10s  %-10s\n", "EXEC_TIME", "TRIGGER", "STATUS", "DURATION", "STEPS")
	fmt.Printf("%-20s  %-15s  %-10s  %-10s  %-10s\n",
		strings.Repeat("-", 20), strings.Repeat("-", 15), strings.Repeat("-", 10), strings.Repeat("-", 10), strings.Repeat("-", 10))

	for _, r := range records {
		duration := fmt.Sprintf("%dms", r.DurationMs)
		fmt.Printf("%-20s  %-15s  %-10s  %-10s  %-10d\n",
			r.StartedAt.Format("2006-01-02 15:04:05"), r.TriggerType, r.Status, duration, r.StepCount)
	}

	fmt.Printf("\n共 %d 条记录\n", len(records))
	return nil
}

// scheduleDelete deletes a task file
func scheduleDelete(scheduleDir, taskID string) error {
	_, status, err := findTask(scheduleDir, taskID)
	if err != nil {
		return err
	}

	path := filepath.Join(scheduleDir, status, taskID+".json")
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("删除任务失败: %w", err)
	}
	fmt.Printf("任务 %s 已删除\n", taskID)
	return nil
}

// scheduleMove moves a task between directories
func scheduleMove(scheduleDir, taskID, fromStatus, toStatus, actionName string) error {
	_, currentStatus, err := findTask(scheduleDir, taskID)
	if err != nil {
		return err
	}
	if currentStatus != fromStatus {
		return fmt.Errorf("任务 %s 当前状态为 %s，无法%s", taskID, currentStatus, actionName)
	}

	fromPath := filepath.Join(scheduleDir, fromStatus, taskID+".json")
	toPath := filepath.Join(scheduleDir, toStatus, taskID+".json")

	if err := os.MkdirAll(filepath.Join(scheduleDir, toStatus), 0755); err != nil {
		return err
	}
	if err := os.Rename(fromPath, toPath); err != nil {
		return fmt.Errorf("%s任务失败: %w", actionName, err)
	}
	fmt.Printf("任务 %s 已%s\n", taskID, actionName)
	return nil
}

// scheduleArchive archives a task from any status
func scheduleArchive(scheduleDir, taskID string) error {
	_, status, err := findTask(scheduleDir, taskID)
	if err != nil {
		return err
	}

	fromPath := filepath.Join(scheduleDir, status, taskID+".json")
	toPath := filepath.Join(scheduleDir, "archive", taskID+".json")

	if err := os.MkdirAll(filepath.Join(scheduleDir, "archive"), 0755); err != nil {
		return err
	}
	if err := os.Rename(fromPath, toPath); err != nil {
		return fmt.Errorf("归档任务失败: %w", err)
	}
	fmt.Printf("任务 %s 已归档\n", taskID)
	return nil
}

// findTask searches for a task across active/disabled/archive directories
func findTask(scheduleDir, taskID string) (*schedule.Task, string, error) {
	for _, status := range []string{"active", "disabled", "archive"} {
		path := filepath.Join(scheduleDir, status, taskID+".json")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var task schedule.Task
		if err := json.Unmarshal(data, &task); err != nil {
			continue
		}
		return &task, status, nil
	}
	return nil, "", fmt.Errorf("任务 %s 不存在", taskID)
}

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-3]) + "..."
}
