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
	// Create 响应应回读库中实际状态：首个模型自动成为默认且启用
	var created types.ModelInfo
	if err := json.Unmarshal(rc.Response.Body(), &created); err != nil {
		t.Fatalf("unmarshal create resp: %v", err)
	}
	if !created.IsDefault || !created.Enabled {
		t.Errorf("Create 响应首个模型应 is_default=true enabled=true, got %+v", created)
	}

	rc = callJSON(h.List, consts.MethodGet, "", nil)
	var resp types.ModelsResponse
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Total != 1 || resp.Default != "gpt-4o" {
		t.Errorf("List: %+v", resp)
	}
	// api_key 必须脱敏且不含原文
	if resp.Models[0].APIKey != "****abcd" || strings.Contains(string(rc.Response.Body()), "sk-test") {
		t.Errorf("api_key 未脱敏: %s", resp.Models[0].APIKey)
	}
	if !resp.Models[0].IsDefault {
		t.Error("首个模型应为默认")
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
