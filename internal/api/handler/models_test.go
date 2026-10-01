package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route/param"

	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/modeldb"
)

func newModelsHandlerForTest(t *testing.T) *ModelsHandler {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return NewModelsHandler(llm.NewModelService(modeldb.New(sqlxDB, dialect)), logger.NewNop())
}

func callJSON(h func(context.Context, *app.RequestContext), method, body string, params map[string]string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(method)
	if body != "" {
		rc.Request.SetBody([]byte(body))
		rc.Request.Header.SetContentTypeBytes([]byte("application/json"))
	}
	for k, v := range params {
		rc.Params = append(rc.Params, param.Param{Key: k, Value: v})
	}
	h(context.Background(), rc)
	return rc
}

const createBody = `{"name":"gpt-4o","model":"gpt-4o","base_url":"https://api.openai.com/v1",
	"api_key":"sk-test-1234abcd","temperature":0.7,"top_p":1.0,"stop":[],"enabled":true}`

func TestModelsHandler_CreateAndList(t *testing.T) {
	h := newModelsHandlerForTest(t)

	rc := callJSON(h.Create, consts.MethodPost, createBody, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("Create status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if strings.Contains(string(rc.Response.Body()), "sk-test") {
		t.Errorf("Create 响应体不应包含明文 api_key: %s", rc.Response.Body())
	}
	// Create 响应应回读库中实际状态：首个模型自动成为默认对话模型且启用
	var created types.ModelInfo
	if err := json.Unmarshal(rc.Response.Body(), &created); err != nil {
		t.Fatalf("unmarshal create resp: %v", err)
	}
	if len(created.DefaultTypes) != 1 || created.DefaultTypes[0] != "chat" || !created.Enabled {
		t.Errorf("Create 响应首个模型应 default_types=[chat] enabled=true, got %+v", created)
	}

	rc = callJSON(h.List, consts.MethodGet, "", nil)
	var resp types.ModelsResponse
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Total != 1 || resp.Defaults.Chat != "gpt-4o" {
		t.Errorf("List: %+v", resp)
	}
	// api_key 必须脱敏且不含原文
	if resp.Models[0].APIKey != "****abcd" || strings.Contains(string(rc.Response.Body()), "sk-test") {
		t.Errorf("api_key 未脱敏: %s", resp.Models[0].APIKey)
	}
	if len(resp.Models[0].DefaultTypes) != 1 || resp.Models[0].DefaultTypes[0] != "chat" {
		t.Errorf("首个模型应为默认对话模型, got %v", resp.Models[0].DefaultTypes)
	}
	if resp.Defaults.Voice != "" || resp.Defaults.Vision != "" {
		t.Errorf("未设置的类型应为空串, got %+v", resp.Defaults)
	}
}

func TestModelsHandler_CreateDuplicate(t *testing.T) {
	h := newModelsHandlerForTest(t)
	callJSON(h.Create, consts.MethodPost, createBody, nil)
	rc := callJSON(h.Create, consts.MethodPost, createBody, nil)
	if rc.Response.StatusCode() != 409 {
		t.Errorf("重名创建应 409, got %d", rc.Response.StatusCode())
	}
}

func TestModelsHandler_DeleteDefaultRejected(t *testing.T) {
	h := newModelsHandlerForTest(t)
	callJSON(h.Create, consts.MethodPost, createBody, nil)
	rc := callJSON(h.Delete, consts.MethodDelete, "", map[string]string{"name": "gpt-4o"})
	if rc.Response.StatusCode() != 409 {
		t.Errorf("删除默认模型应 409, got %d", rc.Response.StatusCode())
	}
}

func TestModelsHandler_SetDefaultAndDelete(t *testing.T) {
	h := newModelsHandlerForTest(t)
	callJSON(h.Create, consts.MethodPost, createBody, nil)
	second := strings.Replace(createBody, `"gpt-4o"`, `"backup"`, 1)
	callJSON(h.Create, consts.MethodPost, second, nil)

	rc := callJSON(h.SetDefault, consts.MethodPut, "", map[string]string{"name": "backup"})
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("SetDefault: %d %s", rc.Response.StatusCode(), rc.Response.Body())
	}
	rc = callJSON(h.Delete, consts.MethodDelete, "", map[string]string{"name": "gpt-4o"})
	if rc.Response.StatusCode() != 200 {
		t.Errorf("切换默认后删除应成功, got %d", rc.Response.StatusCode())
	}
}

func TestModelsHandler_UpdateNotFound(t *testing.T) {
	h := newModelsHandlerForTest(t)
	rc := callJSON(h.Update, consts.MethodPut, createBody, map[string]string{"name": "ghost"})
	if rc.Response.StatusCode() != 404 {
		t.Errorf("更新不存在模型应 404, got %d", rc.Response.StatusCode())
	}
}

// TestModelsHandler_CreateOmittedEnabledDefaultsTrue 请求体省略 enabled 字段时默认启用。
func TestModelsHandler_CreateOmittedEnabledDefaultsTrue(t *testing.T) {
	h := newModelsHandlerForTest(t)
	// 先建一个默认模型，避免"首个模型强制启用"掩盖缺省逻辑
	callJSON(h.Create, consts.MethodPost, createBody, nil)

	body := `{"name":"no-enabled","model":"gpt-4o","base_url":"https://api.openai.com/v1",
	"api_key":"sk-test-1234abcd","temperature":0.7,"top_p":1.0,"stop":[]}`
	rc := callJSON(h.Create, consts.MethodPost, body, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("Create status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}

	rc = callJSON(h.List, consts.MethodGet, "", nil)
	var resp types.ModelsResponse
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, m := range resp.Models {
		if m.Name == "no-enabled" {
			if !m.Enabled {
				t.Error("省略 enabled 字段创建的模型应默认启用")
			}
			return
		}
	}
	t.Error("List 中未找到模型 no-enabled")
}

// callJSONQuery 与 callJSON 相同，但带完整请求 URI（含查询串），供读取 type 参数的接口使用。
func callJSONQuery(h func(context.Context, *app.RequestContext), method, uri string, params map[string]string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(method)
	rc.Request.SetRequestURI(uri)
	for k, v := range params {
		rc.Params = append(rc.Params, param.Param{Key: k, Value: v})
	}
	h(context.Background(), rc)
	return rc
}

// newModelsHandlerWithTwo 建 gpt-4o（默认对话）与 backup 两个模型。
func newModelsHandlerWithTwo(t *testing.T) *ModelsHandler {
	t.Helper()
	h := newModelsHandlerForTest(t)
	callJSON(h.Create, consts.MethodPost, createBody, nil)
	second := strings.Replace(createBody, `"gpt-4o"`, `"backup"`, 1)
	callJSON(h.Create, consts.MethodPost, second, nil)
	return h
}

func listModels(t *testing.T, h *ModelsHandler) types.ModelsResponse {
	t.Helper()
	rc := callJSON(h.List, consts.MethodGet, "", nil)
	var resp types.ModelsResponse
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

func TestModelsHandler_SetDefaultByType(t *testing.T) {
	h := newModelsHandlerWithTwo(t)
	name := map[string]string{"name": "backup"}

	rc := callJSONQuery(h.SetDefault, consts.MethodPut, "/web/models/backup/default?type=voice", name)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("SetDefault voice: %d %s", rc.Response.StatusCode(), rc.Response.Body())
	}
	rc = callJSONQuery(h.SetDefault, consts.MethodPut, "/web/models/gpt-4o/default?type=vision",
		map[string]string{"name": "gpt-4o"})
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("SetDefault vision: %d %s", rc.Response.StatusCode(), rc.Response.Body())
	}

	resp := listModels(t, h)
	want := types.DefaultModels{Chat: "gpt-4o", Voice: "backup", Vision: "gpt-4o"}
	if resp.Defaults != want {
		t.Errorf("defaults = %+v, want %+v", resp.Defaults, want)
	}
	for _, m := range resp.Models {
		got := strings.Join(m.DefaultTypes, ",")
		switch m.Name {
		case "gpt-4o":
			if got != "chat,vision" {
				t.Errorf("gpt-4o default_types = %q, want chat,vision", got)
			}
		case "backup":
			if got != "voice" {
				t.Errorf("backup default_types = %q, want voice", got)
			}
		}
	}
}

// TestModelsHandler_SetDefaultOmittedType type 省略时按 chat 处理。
func TestModelsHandler_SetDefaultOmittedType(t *testing.T) {
	h := newModelsHandlerWithTwo(t)
	rc := callJSONQuery(h.SetDefault, consts.MethodPut, "/web/models/backup/default",
		map[string]string{"name": "backup"})
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("SetDefault: %d %s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if d := listModels(t, h).Defaults.Chat; d != "backup" {
		t.Errorf("defaults.chat = %q, want backup", d)
	}
}

func TestModelsHandler_DefaultInvalidType(t *testing.T) {
	h := newModelsHandlerWithTwo(t)
	name := map[string]string{"name": "backup"}
	for _, fn := range []func(context.Context, *app.RequestContext){h.SetDefault, h.ClearDefault} {
		rc := callJSONQuery(fn, consts.MethodPut, "/web/models/backup/default?type=audio", name)
		if rc.Response.StatusCode() != 400 || !strings.Contains(string(rc.Response.Body()), "invalid_request") {
			t.Errorf("非法 type 应 400 invalid_request, got %d %s", rc.Response.StatusCode(), rc.Response.Body())
		}
	}
}

func TestModelsHandler_ClearDefault(t *testing.T) {
	h := newModelsHandlerWithTwo(t)
	name := map[string]string{"name": "backup"}
	callJSONQuery(h.SetDefault, consts.MethodPut, "/web/models/backup/default?type=voice", name)

	// 持有语音默认的模型不可删除
	rc := callJSON(h.Delete, consts.MethodDelete, "", name)
	if rc.Response.StatusCode() != 409 {
		t.Errorf("删除默认语音模型应 409, got %d", rc.Response.StatusCode())
	}

	rc = callJSONQuery(h.ClearDefault, consts.MethodDelete, "/web/models/backup/default?type=voice", name)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("ClearDefault voice: %d %s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if d := listModels(t, h).Defaults.Voice; d != "" {
		t.Errorf("取消后 defaults.voice 应为空, got %q", d)
	}
	for _, m := range listModels(t, h).Models {
		if m.Name == "backup" && (m.DefaultTypes == nil || len(m.DefaultTypes) != 0) {
			t.Errorf("非默认模型 default_types 应为空数组, got %#v", m.DefaultTypes)
		}
	}

	rc = callJSON(h.Delete, consts.MethodDelete, "", name)
	if rc.Response.StatusCode() != 200 {
		t.Errorf("取消默认后删除应成功, got %d", rc.Response.StatusCode())
	}
}

// TestModelsHandler_EmptyDefaultTypesSerialized 非默认模型序列化为 "default_types":[] 而不是 null。
func TestModelsHandler_EmptyDefaultTypesSerialized(t *testing.T) {
	h := newModelsHandlerWithTwo(t)
	rc := callJSON(h.List, consts.MethodGet, "", nil)
	if !strings.Contains(string(rc.Response.Body()), `"default_types":[]`) {
		t.Errorf("响应应含 \"default_types\":[]: %s", rc.Response.Body())
	}
}

func TestModelsHandler_ClearChatDefaultRejected(t *testing.T) {
	h := newModelsHandlerWithTwo(t)
	for _, uri := range []string{"/web/models/gpt-4o/default?type=chat", "/web/models/gpt-4o/default"} {
		rc := callJSONQuery(h.ClearDefault, consts.MethodDelete, uri, map[string]string{"name": "gpt-4o"})
		if rc.Response.StatusCode() != 400 || !strings.Contains(string(rc.Response.Body()), "default_chat_required") {
			t.Errorf("%s: 取消对话默认应 400 default_chat_required, got %d %s",
				uri, rc.Response.StatusCode(), rc.Response.Body())
		}
	}
}

// TestParseDefaultType 仅校验 repo 标识与 handler 解析的一致性。
func TestParseDefaultType(t *testing.T) {
	for _, f := range repo.AllDefaultFlags {
		rc := app.NewContext(0)
		rc.Request.SetRequestURI("/x?type=" + f.String())
		got, ok := parseDefaultType(rc)
		if !ok || got != f {
			t.Errorf("parseDefaultType(%s) = %v, %v", f, got, ok)
		}
	}
}
