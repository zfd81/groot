package handler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/logger"
)

// fakeSkillBackend 是 skill.Backend 的最小实现，覆盖 List 成功/失败两条路径。
//
// skill.Backend 接口只有 List 与 Get 两个方法（见 eino skill.go）；
// listAgentSkills 仅消费 List，Get 在这里给出未实现桩——若有调用立刻失败暴露问题。
type fakeSkillBackend struct {
	matters []skill.FrontMatter
	err     error
}

func (f *fakeSkillBackend) List(ctx context.Context) ([]skill.FrontMatter, error) {
	return f.matters, f.err
}

func (f *fakeSkillBackend) Get(ctx context.Context, name string) (skill.Skill, error) {
	return skill.Skill{}, errors.New("not implemented")
}

// TestAgentsHandler_ListsGrootFirst 验证 /agents 响应：
//   - 主 Agent groot 永远是 Agents[0]
//   - 子 Agent 按 Names() 字典序
//   - 没有 SubAgentRegistry（nil）时只返回主 Agent
//   - skills 字段总是非 nil（即使为空 []）
func TestAgentsHandler_ListsGrootFirst(t *testing.T) {
	reg := agent.NewRegistryForTest(1)
	reg.SetEntryForTest("db-agent", &agent.SubAgentEntry{Name: "db-agent", Description: "数据库专家"})
	reg.SetEntryForTest("weather-agent", &agent.SubAgentEntry{Name: "weather-agent", Description: "天气查询"})

	h := NewAgentsHandler(reg, nil, logger.NewNop())

	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	h.Serve(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 200 {
		t.Fatalf("expected 200, got %d body=%s", got, rc.Response.Body())
	}

	var resp types.AgentsResponse
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rc.Response.Body())
	}
	if len(resp.Agents) != 3 {
		t.Fatalf("expected 3 agents, got %d: %+v", len(resp.Agents), resp.Agents)
	}
	if resp.Agents[0].Name != agent.MainAgentName {
		t.Errorf("Agents[0] = %s, want %s (groot first)", resp.Agents[0].Name, agent.MainAgentName)
	}
	// 字典序：db-agent 在前，weather-agent 在后
	if resp.Agents[1].Name != "db-agent" || resp.Agents[2].Name != "weather-agent" {
		t.Errorf("子 Agent 排序错误: %s, %s", resp.Agents[1].Name, resp.Agents[2].Name)
	}
	// skills 字段非 nil
	for i, a := range resp.Agents {
		if a.Skills == nil {
			t.Errorf("Agents[%d] (%s).Skills is nil, want empty slice", i, a.Name)
		}
	}
}

// TestAgentsHandler_NilRegistryReturnsOnlyGroot 验证 registry 为 nil 时只返回主 Agent。
func TestAgentsHandler_NilRegistryReturnsOnlyGroot(t *testing.T) {
	h := NewAgentsHandler(nil, nil, logger.NewNop())
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	h.Serve(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 200 {
		t.Fatalf("expected 200, got %d", got)
	}
	var resp types.AgentsResponse
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Agents) != 1 || resp.Agents[0].Name != agent.MainAgentName {
		t.Fatalf("expected [groot], got %+v", resp.Agents)
	}
}

// TestAgentsHandler_BackendListError 验证 backend.List 返回 err 时降级为空 slice，
// 整体仍 200，listAgentSkills 不会让 handler 抛 5xx。
//
// 这是修复后的契约：失败已被显式 Info 出来（通过 logger.NewNop() 吞掉日志），
// 但响应结构保持稳定——空数组而非 nil，前端可解析。
func TestAgentsHandler_BackendListError(t *testing.T) {
	reg := agent.NewRegistryForTest(1)
	reg.SetEntryForTest("flaky-agent", &agent.SubAgentEntry{
		Name:        "flaky-agent",
		Description: "skill backend 不稳",
		SkillBK:     &fakeSkillBackend{err: errors.New("boom")},
	})
	h := NewAgentsHandler(reg, nil, logger.NewNop())

	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	h.Serve(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 200 {
		t.Fatalf("expected 200 (degraded), got %d body=%s", got, rc.Response.Body())
	}
	var resp types.AgentsResponse
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Agents) != 2 {
		t.Fatalf("expected 2 agents (groot + flaky-agent), got %d", len(resp.Agents))
	}
	flaky := resp.Agents[1]
	if flaky.Name != "flaky-agent" {
		t.Fatalf("unexpected order: %+v", resp.Agents)
	}
	if flaky.Skills == nil {
		t.Fatalf("Skills should be empty slice, not nil")
	}
	if len(flaky.Skills) != 0 {
		t.Fatalf("expected 0 skills due to backend error, got %+v", flaky.Skills)
	}
}

// TestAgentsHandler_BackendListSuccess 验证 backend.List 返回 frontMatter 时被正确转换为 SkillInfo。
//
// 覆盖正常路径：FrontMatter.Name/Description 字段映射到 SkillInfo 同名字段，
// 顺序保持与 backend 给出的顺序一致。
func TestAgentsHandler_BackendListSuccess(t *testing.T) {
	matters := []skill.FrontMatter{
		{Name: "echo", Description: "回显工具"},
		{Name: "sql", Description: "查 SQL"},
	}
	reg := agent.NewRegistryForTest(1)
	reg.SetEntryForTest("data-agent", &agent.SubAgentEntry{
		Name:        "data-agent",
		Description: "数据查询",
		SkillBK:     &fakeSkillBackend{matters: matters},
	})
	h := NewAgentsHandler(reg, nil, logger.NewNop())

	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	h.Serve(context.Background(), rc)

	if got := rc.Response.StatusCode(); got != 200 {
		t.Fatalf("expected 200, got %d", got)
	}
	var resp types.AgentsResponse
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data := resp.Agents[1]
	if len(data.Skills) != 2 {
		t.Fatalf("expected 2 skills, got %+v", data.Skills)
	}
	if data.Skills[0].Name != "echo" || data.Skills[0].Description != "回显工具" {
		t.Errorf("Skills[0] mismatch: %+v", data.Skills[0])
	}
	if data.Skills[1].Name != "sql" || data.Skills[1].Description != "查 SQL" {
		t.Errorf("Skills[1] mismatch: %+v", data.Skills[1])
	}
}
