package schedule

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/logger"
)

func TestNotificationConfig_UnmarshalJSON(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		wantSuccess string
		wantFailure string
	}{
		{"字符串", `{"on_success":"用邮件发给 a@example.com","on_failure":"发 webhook"}`, "用邮件发给 a@example.com", "发 webhook"},
		{"旧数组格式", `{"on_success":["webhook"],"on_failure":["webhook","email"]}`, "", ""},
		{"null", `{"on_success":null,"on_failure":null}`, "", ""},
		{"字段缺失", `{}`, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var n NotificationConfig
			if err := json.Unmarshal([]byte(c.input), &n); err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if n.OnSuccess != c.wantSuccess || n.OnFailure != c.wantFailure {
				t.Errorf("期望 (%q, %q)，实际 (%q, %q)", c.wantSuccess, c.wantFailure, n.OnSuccess, n.OnFailure)
			}
		})
	}
}

func TestNotificationConfig_TaskRoundTrip(t *testing.T) {
	// 旧任务整体 payload 需能正常加载
	legacy := `{"id":"t1","name":"日报","schedule":"0 9 * * *","task":{"instruction":"x"},"notification":{"on_success":["webhook"],"on_failure":[]}}`
	var task Task
	if err := json.Unmarshal([]byte(legacy), &task); err != nil {
		t.Fatalf("旧任务解析失败: %v", err)
	}
	if task.ID != "t1" || task.Notification.OnSuccess != "" {
		t.Errorf("旧任务解析结果异常: %+v", task)
	}

	task.Notification.OnSuccess = "发邮件"
	data, err := json.Marshal(&task)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	var back Task
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if back.Notification.OnSuccess != "发邮件" {
		t.Errorf("往返后通知要求丢失: %+v", back.Notification)
	}
}

func TestBuildNotifyInstruction(t *testing.T) {
	task := &Task{ID: "t1", Name: "日报"}
	start := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)

	t.Run("成功包含结果与要求", func(t *testing.T) {
		got := buildNotifyInstruction(task, "completed", "今日数据正常", "", start, 3*time.Second, "用邮件发给 a@example.com")
		for _, want := range []string{"日报", "t1", "completed", "今日数据正常", "用邮件发给 a@example.com", "不要重新执行任务"} {
			if !strings.Contains(got, want) {
				t.Errorf("指令缺少 %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, "错误信息") {
			t.Errorf("成功时不应包含错误信息:\n%s", got)
		}
	})

	t.Run("失败包含错误", func(t *testing.T) {
		got := buildNotifyInstruction(task, "failed", "", "模型配置不可用", start, time.Second, "发 webhook")
		if !strings.Contains(got, "模型配置不可用") || !strings.Contains(got, "failed") {
			t.Errorf("失败指令缺少错误信息:\n%s", got)
		}
	})

	t.Run("超长结果截断", func(t *testing.T) {
		long := strings.Repeat("数", maxNotifyResultRunes+100)
		got := buildNotifyInstruction(task, "completed", long, "", start, time.Second, "x")
		if strings.Contains(got, long) {
			t.Error("超长结果未被截断")
		}
		if !strings.Contains(got, strings.Repeat("数", maxNotifyResultRunes)) || !strings.Contains(got, "已截断") {
			t.Error("截断后应保留上限长度内容并带截断标记")
		}
	})
}

// fakeExecutor records calls and sets a preset status on the task.
type fakeExecutor struct {
	calls     int
	sessionID string
	task      *agent.Task
	status    agent.TaskStatus
	err       *agent.TaskError
}

func (f *fakeExecutor) Execute(ctx context.Context, sessionID string, task *agent.Task, sse *agent.SSEWriter) {
	f.calls++
	f.sessionID = sessionID
	f.task = task
	task.Status = f.status
	task.Error = f.err
}

func TestRunner_Notify(t *testing.T) {
	task := &Task{
		ID:           "t1",
		Name:         "日报",
		TaskDef:      TaskDef{Model: "m1"},
		Notification: NotificationConfig{OnSuccess: "成功发邮件", OnFailure: "失败发 webhook"},
	}
	start := time.Now()

	t.Run("要求为空不执行", func(t *testing.T) {
		fe := &fakeExecutor{status: agent.StatusCompleted}
		r := &Runner{executor: fe, log: logger.NewNop()}
		r.notify(&Task{ID: "t2"}, "s1", "completed", "ok", "", start, time.Second)
		r.notify(&Task{ID: "t2", Notification: NotificationConfig{OnSuccess: "  "}}, "s1", "completed", "ok", "", start, time.Second)
		if fe.calls != 0 {
			t.Errorf("要求为空时不应调用执行器，实际调用 %d 次", fe.calls)
		}
	})

	cases := []struct {
		status string
		want   string
	}{
		{"completed", "成功发邮件"},
		{"failed", "失败发 webhook"},
		{"cancelled", "失败发 webhook"},
	}
	for _, c := range cases {
		t.Run("状态_"+c.status, func(t *testing.T) {
			fe := &fakeExecutor{status: agent.StatusCompleted}
			r := &Runner{executor: fe, log: logger.NewNop()}
			r.notify(task, "sess-1", c.status, "res", "", start, time.Second)
			if fe.calls != 1 {
				t.Fatalf("期望调用执行器 1 次，实际 %d", fe.calls)
			}
			if !strings.Contains(fe.task.Instruction, c.want) {
				t.Errorf("状态 %s 应使用要求 %q，指令:\n%s", c.status, c.want, fe.task.Instruction)
			}
			if fe.sessionID != "sess-1" {
				t.Errorf("通知轮应在原会话执行，实际 session %q", fe.sessionID)
			}
			if fe.task.ID != "sess-1-notify" {
				t.Errorf("通知轮 ID 应为 sess-1-notify，实际 %q", fe.task.ID)
			}
			if fe.task.Caller != notifyCaller || fe.task.ModelName != "m1" {
				t.Errorf("Caller/ModelName 异常: %q / %q", fe.task.Caller, fe.task.ModelName)
			}
		})
	}

	t.Run("执行失败不 panic", func(t *testing.T) {
		fe := &fakeExecutor{status: agent.StatusFailed, err: &agent.TaskError{Code: "execution_error", Message: "boom"}}
		r := &Runner{executor: fe, log: logger.NewNop()}
		r.notify(task, "sess-2", "failed", "", "err", start, time.Second)
		if fe.calls != 1 {
			t.Errorf("期望调用执行器 1 次，实际 %d", fe.calls)
		}
	})
}
