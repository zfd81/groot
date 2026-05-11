package schedule

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// Schedule tool names
const (
	ToolScheduleCreate  = "schedule_create"
	ToolScheduleList    = "schedule_list"
	ToolScheduleDelete  = "schedule_delete"
	ToolScheduleDisable = "schedule_disable"
	ToolScheduleEnable  = "schedule_enable"
	ToolScheduleArchive = "schedule_archive"
	ToolScheduleHistory = "schedule_history"
	ToolScheduleInspect = "schedule_inspect"
)

// NewScheduleTools creates all 8 schedule built-in tools
func NewScheduleTools(mgr *Manager) map[string]tool.BaseTool {
	return map[string]tool.BaseTool{
		ToolScheduleCreate:  &createTool{mgr: mgr},
		ToolScheduleList:    &listTool{mgr: mgr},
		ToolScheduleDelete:  &deleteTool{mgr: mgr},
		ToolScheduleDisable: &disableTool{mgr: mgr},
		ToolScheduleEnable:  &enableTool{mgr: mgr},
		ToolScheduleArchive: &archiveTool{mgr: mgr},
		ToolScheduleHistory: &historyTool{mgr: mgr},
		ToolScheduleInspect: &inspectTool{mgr: mgr},
	}
}

func taskIDParam() map[string]*schema.ParameterInfo {
	return map[string]*schema.ParameterInfo{
		"task_id": {
			Type:     schema.String,
			Desc:     "任务 ID",
			Required: true,
		},
	}
}

// ============ schedule_create ============

type createTool struct{ mgr *Manager }

func (t *createTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: ToolScheduleCreate,
		Desc: "创建定时任务。schedule 支持三种格式：cron 表达式（如 0 9 * * *）、ISO8601 时间戳（一次性任务）、Go duration（如 30m/1h 间隔执行）",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"name":              {Type: schema.String, Desc: "任务名称", Required: true},
			"schedule":          {Type: schema.String, Desc: "调度表达式（cron/ISO8601/duration）", Required: true},
			"instruction":       {Type: schema.String, Desc: "要执行的指令", Required: true},
			"model":             {Type: schema.String, Desc: "LLM 模型名称（可选）"},
			"missed_policy":     {Type: schema.String, Desc: "错过策略，默认 run_once", Enum: []string{"run_once", "skip"}},
			"notify_on_success": {Type: schema.Array, ElemInfo: &schema.ParameterInfo{Type: schema.String}, Desc: "成功通知渠道列表"},
			"notify_on_failure": {Type: schema.Array, ElemInfo: &schema.ParameterInfo{Type: schema.String}, Desc: "失败通知渠道列表"},
		}),
	}, nil
}

func (t *createTool) InvokableRun(ctx context.Context, argsJSON string, opts ...tool.Option) (string, error) {
	var input struct {
		Name            string   `json:"name"`
		Schedule        string   `json:"schedule"`
		Instruction     string   `json:"instruction"`
		Model           string   `json:"model"`
		MissedPolicy    string   `json:"missed_policy"`
		NotifyOnSuccess []string `json:"notify_on_success"`
		NotifyOnFailure []string `json:"notify_on_failure"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &input); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}

	task := &Task{
		Name:         input.Name,
		Schedule:     input.Schedule,
		MissedPolicy: input.MissedPolicy,
		TaskDef: TaskDef{
			Instruction: input.Instruction,
			Model:       input.Model,
		},
		Notification: NotificationConfig{
			OnSuccess: input.NotifyOnSuccess,
			OnFailure: input.NotifyOnFailure,
		},
	}

	if err := t.mgr.Create(task); err != nil {
		return "", err
	}

	return fmt.Sprintf("任务已创建: %s (id: %s)", task.Name, task.ID), nil
}

// ============ schedule_list ============

type listTool struct{ mgr *Manager }

func (t *listTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: ToolScheduleList,
		Desc: "查询所有定时任务，支持按状态过滤",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"status": {Type: schema.String, Desc: "状态过滤：active/disabled/archive/all（默认 all）", Enum: []string{"active", "disabled", "archive", "all"}},
		}),
	}, nil
}

func (t *listTool) InvokableRun(ctx context.Context, argsJSON string, opts ...tool.Option) (string, error) {
	var input struct {
		Status string `json:"status"`
	}
	json.Unmarshal([]byte(argsJSON), &input)
	if input.Status == "" {
		input.Status = "all"
	}

	tasks, err := t.mgr.List(input.Status)
	if err != nil {
		return "", err
	}

	result, _ := json.MarshalIndent(tasks, "", "  ")
	return string(result), nil
}

// ============ schedule_delete ============

type deleteTool struct{ mgr *Manager }

func (t *deleteTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name:        ToolScheduleDelete,
		Desc:        "删除定时任务（物理删除）",
		ParamsOneOf: schema.NewParamsOneOfByParams(taskIDParam()),
	}, nil
}

func (t *deleteTool) InvokableRun(ctx context.Context, argsJSON string, opts ...tool.Option) (string, error) {
	var input struct{ TaskID string `json:"task_id"` }
	if err := json.Unmarshal([]byte(argsJSON), &input); err != nil {
		return "", err
	}
	if err := t.mgr.Delete(input.TaskID); err != nil {
		return "", err
	}
	return "任务已删除: " + input.TaskID, nil
}

// ============ schedule_disable ============

type disableTool struct{ mgr *Manager }

func (t *disableTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name:        ToolScheduleDisable,
		Desc:        "禁用定时任务（active → disabled）",
		ParamsOneOf: schema.NewParamsOneOfByParams(taskIDParam()),
	}, nil
}

func (t *disableTool) InvokableRun(ctx context.Context, argsJSON string, opts ...tool.Option) (string, error) {
	var input struct{ TaskID string `json:"task_id"` }
	if err := json.Unmarshal([]byte(argsJSON), &input); err != nil {
		return "", err
	}
	if err := t.mgr.Disable(input.TaskID); err != nil {
		return "", err
	}
	return "任务已禁用: " + input.TaskID, nil
}

// ============ schedule_enable ============

type enableTool struct{ mgr *Manager }

func (t *enableTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name:        ToolScheduleEnable,
		Desc:        "启用定时任务（disabled → active）",
		ParamsOneOf: schema.NewParamsOneOfByParams(taskIDParam()),
	}, nil
}

func (t *enableTool) InvokableRun(ctx context.Context, argsJSON string, opts ...tool.Option) (string, error) {
	var input struct{ TaskID string `json:"task_id"` }
	if err := json.Unmarshal([]byte(argsJSON), &input); err != nil {
		return "", err
	}
	if err := t.mgr.Enable(input.TaskID); err != nil {
		return "", err
	}
	return "任务已启用: " + input.TaskID, nil
}

// ============ schedule_archive ============

type archiveTool struct{ mgr *Manager }

func (t *archiveTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name:        ToolScheduleArchive,
		Desc:        "归档定时任务（→ archive）",
		ParamsOneOf: schema.NewParamsOneOfByParams(taskIDParam()),
	}, nil
}

func (t *archiveTool) InvokableRun(ctx context.Context, argsJSON string, opts ...tool.Option) (string, error) {
	var input struct{ TaskID string `json:"task_id"` }
	if err := json.Unmarshal([]byte(argsJSON), &input); err != nil {
		return "", err
	}
	if err := t.mgr.Archive(input.TaskID); err != nil {
		return "", err
	}
	return "任务已归档: " + input.TaskID, nil
}

// ============ schedule_history ============

type historyTool struct{ mgr *Manager }

func (t *historyTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name:        ToolScheduleHistory,
		Desc:        "查看某任务的执行历史",
		ParamsOneOf: schema.NewParamsOneOfByParams(taskIDParam()),
	}, nil
}

func (t *historyTool) InvokableRun(ctx context.Context, argsJSON string, opts ...tool.Option) (string, error) {
	var input struct{ TaskID string `json:"task_id"` }
	if err := json.Unmarshal([]byte(argsJSON), &input); err != nil {
		return "", err
	}

	records, err := t.mgr.GetHistory(input.TaskID)
	if err != nil {
		return "", err
	}

	result, _ := json.MarshalIndent(records, "", "  ")
	return string(result), nil
}

// ============ schedule_inspect ============

type inspectTool struct{ mgr *Manager }

func (t *inspectTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name:        ToolScheduleInspect,
		Desc:        "查看任务详情",
		ParamsOneOf: schema.NewParamsOneOfByParams(taskIDParam()),
	}, nil
}

func (t *inspectTool) InvokableRun(ctx context.Context, argsJSON string, opts ...tool.Option) (string, error) {
	var input struct{ TaskID string `json:"task_id"` }
	if err := json.Unmarshal([]byte(argsJSON), &input); err != nil {
		return "", err
	}

	task, err := t.mgr.Get(input.TaskID)
	if err != nil {
		return "", err
	}

	result, _ := json.MarshalIndent(task, "", "  ")
	return string(result), nil
}
