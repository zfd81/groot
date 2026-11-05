package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/modeldb"
)

// newModelsPublicHandlerForTest 建一套真实的模型仓库，写入 3 个模型（2 启用、1 禁用），
// 使「只列启用模型」这条规则有可观测的差异。
func newModelsPublicHandlerForTest(t *testing.T) *ModelsPublicHandler {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })

	ctx := context.Background()
	models := llm.NewModelService(modeldb.New(sqlxDB, dialect))
	for _, m := range []*repo.Model{
		{Name: "chat-a", Model: "gpt-4o", BaseURL: "https://api.openai.com/v1", APIKey: "sk-test-aaaa1111", Enabled: true},
		{Name: "chat-b", Model: "qwen-max", BaseURL: "https://api.example.com/v1", APIKey: "sk-test-bbbb2222", Enabled: true},
		{Name: "chat-off", Model: "gpt-3.5", BaseURL: "https://api.example.com/v1", APIKey: "sk-test-cccc3333", Enabled: false},
	} {
		if err := models.Create(ctx, m); err != nil {
			t.Fatalf("创建测试模型 %s: %v", m.Name, err)
		}
	}
	return NewModelsPublicHandler(models, logger.NewNop())
}

// modelsListWire 是测试侧独立定义的响应形状，刻意不复用 handler 内部的 modelsListResponse。
// 对 OpenAI 兼容端点而言，JSON 的 key 就是唯一契约（真实 OpenAI SDK 按这些 key 取值）；
// 若编码与解码共用同一结构体，tag 写错时编码与解码会犯同一个错误，测试仍然通过，
// 等于完全没有校验契约。这里的 tag 必须与官方格式一致，才能让 tag 写错时测试变红。
type modelsListWire struct {
	Object string           `json:"object"`
	Data   []modelsCardWire `json:"data"`
}

type modelsCardWire struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// decodeModelsListWire 先按原始 JSON 校验顶层 key 是否存在，再做结构体反解。
// 结构体反解对「缺字段」和「key 拼写不同」都是静默留空，不先查原始 key 的话，
// 把 data 写成 models 这类错误只会表现为空列表，难以一眼定位。
func decodeModelsListWire(t *testing.T, body []byte) modelsListWire {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if _, ok := raw["object"]; !ok {
		t.Fatalf("响应体缺少顶层 key %q: %s", "object", body)
	}
	dataRaw, ok := raw["data"]
	if !ok {
		t.Fatalf("响应体缺少顶层 key %q: %s", "data", body)
	}
	var rawCards []map[string]json.RawMessage
	if err := json.Unmarshal(dataRaw, &rawCards); err != nil {
		t.Fatalf("data 不是数组: %v (%s)", err, body)
	}
	for i, card := range rawCards {
		for _, key := range []string{"id", "object", "created", "owned_by"} {
			if _, ok := card[key]; !ok {
				t.Fatalf("data[%d] 缺少 key %q: %s", i, key, body)
			}
		}
	}

	var resp modelsListWire
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

func TestModelsPublicHandler_ListsEnabledOnly(t *testing.T) {
	h := newModelsPublicHandlerForTest(t)
	rc := callJSON(h.Serve, consts.MethodGet, "", nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}

	resp := decodeModelsListWire(t, rc.Response.Body())
	if len(resp.Data) != 2 {
		t.Fatalf("只应返回启用的 2 个模型, got %d: %s", len(resp.Data), rc.Response.Body())
	}
	ids := map[string]bool{}
	for _, c := range resp.Data {
		ids[c.ID] = true
	}
	if !ids["chat-a"] || !ids["chat-b"] || ids["chat-off"] {
		t.Errorf("返回的模型集合不对: %v", ids)
	}
}

func TestModelsPublicHandler_CardShape(t *testing.T) {
	h := newModelsPublicHandlerForTest(t)
	rc := callJSON(h.Serve, consts.MethodGet, "", nil)

	resp := decodeModelsListWire(t, rc.Response.Body())
	if resp.Object != "list" {
		t.Errorf("object = %q, want list", resp.Object)
	}
	// 先断言数量非零：否则下面的逐元素断言只是一个空循环，会在 data 意外为空时静默通过
	if len(resp.Data) != 2 {
		t.Fatalf("data 应有 2 个元素, got %d: %s", len(resp.Data), rc.Response.Body())
	}
	for _, c := range resp.Data {
		if c.Object != "model" {
			t.Errorf("%s object = %q, want model", c.ID, c.Object)
		}
		if c.OwnedBy != "groot" {
			t.Errorf("%s owned_by = %q, want groot", c.ID, c.OwnedBy)
		}
		if c.Created <= 0 {
			t.Errorf("%s created = %d, want >0", c.ID, c.Created)
		}
	}
	// 上游地址与密钥属于服务端配置，不得出现在对外响应中
	body := string(rc.Response.Body())
	for _, leak := range []string{"api.openai.com", "api.example.com", "sk-test", "base_url", "api_key"} {
		if strings.Contains(body, leak) {
			t.Errorf("响应体不应包含 %q: %s", leak, body)
		}
	}
}

func TestModelsPublicHandler_ListError(t *testing.T) {
	// 模型服务缺失（如装配失败）时不能 panic，要按内部错误返回
	h := NewModelsPublicHandler(nil, logger.NewNop())
	rc := callJSON(h.Serve, consts.MethodGet, "", nil)
	if rc.Response.StatusCode() != 500 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	body := string(rc.Response.Body())
	if !strings.Contains(body, `"type":"api_error"`) || !strings.Contains(body, `"code":"internal_error"`) {
		t.Errorf("应是 api_error/internal_error 信封: %s", body)
	}
}
