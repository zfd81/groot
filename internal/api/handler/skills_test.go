package handler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/logger"
)

// 注：fakeSkillBackend 已在 agents_test.go 中声明（同 package），此处直接复用，
// 避免重复声明导致编译失败。

// TestSkillsHandler_UnknownAgent 验证 X-Agent-Name 指向未注册子 Agent 时返 400。
func TestSkillsHandler_UnknownAgent(t *testing.T) {
	reg := agent.NewRegistryForTest(1)
	h := NewSkillsHandler(nil, reg, logger.NewNop())
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	rc.Request.Header.Set("X-Agent-Name", "nope")
	h.Serve(context.Background(), rc)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("expected 400, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if !strings.Contains(string(rc.Response.Body()), "Unknown agent") {
		t.Errorf("body should contain 'Unknown agent', got %s", rc.Response.Body())
	}
}

// TestSkillsHandler_NilRegistryReturns400 验证 registry 为 nil 时同样返 400。
// 这是配置异常，但用户视角仍为 unknown_agent；运维通过日志区分。
func TestSkillsHandler_NilRegistryReturns400(t *testing.T) {
	h := NewSkillsHandler(nil, nil, logger.NewNop())
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	rc.Request.Header.Set("X-Agent-Name", "any")
	h.Serve(context.Background(), rc)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("expected 400, got %d", rc.Response.StatusCode())
	}
}

// TestSkillsHandler_MainAgentDelegatesToBackend 验证不传 header 与 X-Agent-Name=groot
// 两条路径等价，都走主 Agent backend。
func TestSkillsHandler_MainAgentDelegatesToBackend(t *testing.T) {
	matters := []skill.FrontMatter{
		{Name: "echo", Description: "回显"},
	}
	backend := &fakeSkillBackend{matters: matters}
	h := NewSkillsHandler(backend, agent.NewRegistryForTest(1), logger.NewNop())

	cases := []struct{ name, header string }{
		{"omit", ""},
		{"groot", agent.MainAgentName},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rc := app.NewContext(0)
			rc.Request.Header.SetMethod(consts.MethodGet)
			if c.header != "" {
				rc.Request.Header.Set("X-Agent-Name", c.header)
			}
			h.Serve(context.Background(), rc)
			if rc.Response.StatusCode() != 200 {
				t.Fatalf("expected 200, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
			}
			var resp types.SkillsResponse
			if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
				t.Fatal(err)
			}
			if len(resp.Skills) != 1 || resp.Skills[0].Name != "echo" {
				t.Fatalf("unexpected resp: %+v", resp)
			}
		})
	}
}

// TestSkillsHandler_SubAgentBackend 验证子 Agent 路径正确从 entry.SkillBK 取数据，
// 且不污染到主 Agent backend。
func TestSkillsHandler_SubAgentBackend(t *testing.T) {
	reg := agent.NewRegistryForTest(1)
	sub := &fakeSkillBackend{matters: []skill.FrontMatter{{Name: "sub-skill", Description: "子"}}}
	reg.SetEntryForTest("db-agent", &agent.SubAgentEntry{Name: "db-agent", SkillBK: sub})

	// 主 backend 给一个不同的值，确认不会被错误返回
	mainBackend := &fakeSkillBackend{matters: []skill.FrontMatter{{Name: "main-skill", Description: "主"}}}
	h := NewSkillsHandler(mainBackend, reg, logger.NewNop())

	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	rc.Request.Header.Set("X-Agent-Name", "db-agent")
	h.Serve(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d", rc.Response.StatusCode())
	}
	var resp types.SkillsResponse
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Skills) != 1 || resp.Skills[0].Name != "sub-skill" {
		t.Fatalf("expected sub-skill from sub agent, got %+v", resp.Skills)
	}
}

// TestSkillsHandler_BackendListErrorDegrades 验证 backend.List 失败时降级为 200 + 空数组。
// 与 SkillsResponse 的 200 + 空数组形态保持一致；故障细节通过日志暴露。
func TestSkillsHandler_BackendListErrorDegrades(t *testing.T) {
	backend := &fakeSkillBackend{err: errors.New("boom")}
	h := NewSkillsHandler(backend, nil, logger.NewNop())
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	h.Serve(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200 (degraded), got %d", rc.Response.StatusCode())
	}
	var resp types.SkillsResponse
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Skills) != 0 {
		t.Fatalf("expected empty skills, got %+v", resp.Skills)
	}
}
