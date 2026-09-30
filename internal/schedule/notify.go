package schedule

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/agent"
)

const (
	// notifyCaller identifies the notification round in chat records.
	notifyCaller = "schedule_notify"
	// notifyTimeout bounds a single notification round.
	notifyTimeout = 2 * time.Minute
	// maxNotifyResultRunes caps the task result embedded in the notification instruction.
	maxNotifyResultRunes = 4000
)

// notifyRequirement picks the user's notification requirement for the given status.
func notifyRequirement(task *Task, status string) string {
	if status == string(agent.StatusCompleted) {
		return strings.TrimSpace(task.Notification.OnSuccess)
	}
	return strings.TrimSpace(task.Notification.OnFailure)
}

// buildNotifyInstruction composes the instruction for the notification round.
func buildNotifyInstruction(task *Task, status, result, errMsg string, startedAt time.Time, duration time.Duration, requirement string) string {
	var b strings.Builder
	b.WriteString("定时任务已执行结束，请根据下方的通知要求，使用可用工具发送通知。\n\n")
	b.WriteString("## 任务执行信息\n")
	fmt.Fprintf(&b, "- 任务名称：%s\n", task.Name)
	fmt.Fprintf(&b, "- 任务 ID：%s\n", task.ID)
	fmt.Fprintf(&b, "- 执行状态：%s\n", status)
	fmt.Fprintf(&b, "- 开始时间：%s\n", startedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "- 耗时：%s\n", duration.Round(time.Millisecond))
	if errMsg != "" {
		fmt.Fprintf(&b, "- 错误信息：%s\n", truncateRunes(errMsg, maxNotifyResultRunes))
	}
	if result != "" {
		b.WriteString("\n## 执行结果\n")
		b.WriteString(truncateRunes(result, maxNotifyResultRunes))
		b.WriteString("\n")
	}
	b.WriteString("\n## 通知要求\n")
	b.WriteString(requirement)
	b.WriteString("\n\n## 约束\n")
	b.WriteString("只使用可用工具完成上述通知，不要重新执行任务，不要创建、修改或删除定时任务。完成后简要说明通知结果。\n")
	return b.String()
}

// truncateRunes truncates s to at most max runes, appending a marker when cut.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "\n…（内容过长已截断）"
}

// notify runs the notification round in the task's session when the user
// configured a requirement for the given status. The LLM fulfils the
// requirement with the MCP tools available to the main agent.
func (r *Runner) notify(task *Task, sessionID, status, result, errMsg string, startedAt time.Time, duration time.Duration) {
	requirement := notifyRequirement(task, status)
	if requirement == "" {
		return
	}

	notifyTask := &agent.Task{
		ID:          sessionID + "-notify",
		Instruction: buildNotifyInstruction(task, status, result, errMsg, startedAt, duration, requirement),
		ModelName:   task.TaskDef.Model,
		Caller:      notifyCaller,
		StartTime:   time.Now(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
	r.executor.Execute(ctx, sessionID, notifyTask, nil)

	fields := []zap.Field{
		zap.String("task_id", task.ID),
		zap.String("session_id", sessionID),
		zap.String("status", string(notifyTask.Status)),
	}
	if notifyTask.Status == agent.StatusCompleted {
		r.log.Info("任务通知已完成", fields...)
		return
	}
	if notifyTask.Error != nil {
		fields = append(fields, zap.String("reason", notifyTask.Error.Message))
	}
	r.log.Error("任务通知失败", fields...)
}
