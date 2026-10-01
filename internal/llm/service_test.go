package llm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/modeldb"
)

func newTestService(t *testing.T) *ModelService {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return NewModelService(modeldb.New(sqlxDB, dialect))
}

func validModel(name string) *repo.Model {
	return &repo.Model{
		Name:        name,
		BaseURL:     "https://api.openai.com/v1",
		APIKey:      "sk-test-1234abcd",
		Model:       "gpt-4o",
		Temperature: 0.7,
		TopP:        1.0,
		Enabled:     true,
	}
}

func TestModelService_CreateFirstBecomesDefault(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	if err := s.Create(ctx, validModel("m1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	m, err := s.GetByName(ctx, "")
	if err != nil || m.Name != "m1" {
		t.Fatalf("首个模型应自动成为默认: %v, %+v", err, m)
	}

	// 第二个模型不抢默认
	if err := s.Create(ctx, validModel("m2")); err != nil {
		t.Fatalf("Create m2: %v", err)
	}
	m, _ = s.GetByName(ctx, "")
	if m.Name != "m1" {
		t.Errorf("默认模型应仍为 m1, got %s", m.Name)
	}
}

func TestModelService_CreateValidation(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	cases := []struct {
		mutate func(*repo.Model)
		desc   string
	}{
		{func(m *repo.Model) { m.Name = "" }, "空名称"},
		{func(m *repo.Model) { m.BaseURL = "" }, "空 base_url"},
		{func(m *repo.Model) { m.APIKey = "" }, "空 api_key"},
		{func(m *repo.Model) { m.Model = "" }, "空 model"},
		{func(m *repo.Model) { m.Temperature = 2.5 }, "temperature 超界"},
		{func(m *repo.Model) { m.TopP = 1.5 }, "top_p 超界"},
		{func(m *repo.Model) { m.FrequencyPenalty = -3 }, "frequency_penalty 超界"},
		{func(m *repo.Model) { m.PresencePenalty = 3 }, "presence_penalty 超界"},
	}
	for _, c := range cases {
		m := validModel("bad")
		c.mutate(m)
		if err := s.Create(ctx, m); !errors.Is(err, ErrInvalidModel) {
			t.Errorf("%s: want ErrInvalidModel, got %v", c.desc, err)
		}
	}
}

func TestModelService_CreateDuplicateName(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if err := s.Create(ctx, validModel("dup")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Create(ctx, validModel("dup")); !errors.Is(err, ErrNameExists) {
		t.Errorf("want ErrNameExists, got %v", err)
	}
}

func TestModelService_GetByName(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	// 空库、无默认
	if _, err := s.GetByName(ctx, ""); !errors.Is(err, ErrNoDefaultModel) {
		t.Errorf("want ErrNoDefaultModel, got %v", err)
	}
	if _, err := s.GetByName(ctx, "nope"); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("want ErrModelNotFound, got %v", err)
	}

	if err := s.Create(ctx, validModel("m1")); err != nil {
		t.Fatalf("Create m1: %v", err)
	}
	if err := s.Create(ctx, validModel("m2")); err != nil {
		t.Fatalf("Create m2: %v", err)
	}

	// 禁用后按名称获取报 ErrModelDisabled
	m2, err := s.GetByName(ctx, "m2")
	if err != nil {
		t.Fatalf("GetByName m2: %v", err)
	}
	m2.Enabled = false
	m2.APIKey = "" // 留空 = 不修改
	if err := s.Update(ctx, "m2", m2); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := s.GetByName(ctx, "m2"); !errors.Is(err, ErrModelDisabled) {
		t.Errorf("want ErrModelDisabled, got %v", err)
	}
}

func TestModelService_GetByNameExpandsEnv(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	t.Setenv("GROOT_TEST_KEY", "sk-from-env")

	m := validModel("env-model")
	m.APIKey = "${GROOT_TEST_KEY}"
	if err := s.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.GetByName(ctx, "env-model")
	if err != nil || got.APIKey != "sk-from-env" {
		t.Errorf("APIKey 应展开环境变量: %v, %q", err, got.APIKey)
	}
}

func TestModelService_UpdateKeepsAPIKeyWhenEmpty(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if err := s.Create(ctx, validModel("m1")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	upd := validModel("m1")
	upd.APIKey = ""
	upd.Temperature = 1.2
	if err := s.Update(ctx, "m1", upd); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := s.GetByName(ctx, "m1")
	if got.APIKey != "sk-test-1234abcd" {
		t.Errorf("api_key 留空应保持原值, got %q", got.APIKey)
	}
	if got.Temperature != 1.2 {
		t.Errorf("temperature 应更新为 1.2, got %v", got.Temperature)
	}
}

func TestModelService_UpdateRenameConflict(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if err := s.Create(ctx, validModel("m1")); err != nil {
		t.Fatalf("Create m1: %v", err)
	}
	if err := s.Create(ctx, validModel("m2")); err != nil {
		t.Fatalf("Create m2: %v", err)
	}

	upd := validModel("m2") // 把 m1 改名为已存在的 m2
	if err := s.Update(ctx, "m1", upd); !errors.Is(err, ErrNameExists) {
		t.Errorf("want ErrNameExists, got %v", err)
	}
}

func TestModelService_DefaultProtection(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if err := s.Create(ctx, validModel("m1")); err != nil { // 自动默认
		t.Fatalf("Create m1: %v", err)
	}
	if err := s.Create(ctx, validModel("m2")); err != nil {
		t.Fatalf("Create m2: %v", err)
	}

	// 默认模型禁止删除
	if err := s.Delete(ctx, "m1"); !errors.Is(err, ErrDefaultProtected) {
		t.Errorf("删除默认模型应被拒绝, got %v", err)
	}
	// 默认模型禁止禁用
	upd := validModel("m1")
	upd.APIKey = ""
	upd.Enabled = false
	if err := s.Update(ctx, "m1", upd); !errors.Is(err, ErrDefaultProtected) {
		t.Errorf("禁用默认模型应被拒绝, got %v", err)
	}
	// 切换默认后即可删除
	if err := s.SetDefault(ctx, "m2", repo.DefaultChat); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	if err := s.Delete(ctx, "m1"); err != nil {
		t.Errorf("非默认模型应可删除: %v", err)
	}
}

func TestModelService_SetDefaultRejectsDisabled(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if err := s.Create(ctx, validModel("m1")); err != nil {
		t.Fatalf("Create m1: %v", err)
	}
	if err := s.Create(ctx, validModel("m2")); err != nil {
		t.Fatalf("Create m2: %v", err)
	}

	upd := validModel("m2")
	upd.APIKey = ""
	upd.Enabled = false
	if err := s.Update(ctx, "m2", upd); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if err := s.SetDefault(ctx, "m2", repo.DefaultChat); !errors.Is(err, ErrModelDisabled) {
		t.Errorf("禁用模型不可设为默认, got %v", err)
	}
	if err := s.SetDefault(ctx, "ghost", repo.DefaultChat); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("want ErrModelNotFound, got %v", err)
	}
}

func TestModelService_GetStored(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	t.Setenv("GROOT_STORED_KEY", "sk-stored-env")

	if _, err := s.GetStored(ctx, "nope"); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("want ErrModelNotFound, got %v", err)
	}

	m1 := validModel("m1")
	m1.APIKey = "${GROOT_STORED_KEY}"
	if err := s.Create(ctx, m1); err != nil {
		t.Fatalf("Create m1: %v", err)
	}
	if err := s.Create(ctx, validModel("m2")); err != nil {
		t.Fatalf("Create m2: %v", err)
	}

	// 禁用 m2 后 GetStored 仍能取到
	upd := validModel("m2")
	upd.APIKey = ""
	upd.Enabled = false
	if err := s.Update(ctx, "m2", upd); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := s.GetStored(ctx, "m2")
	if err != nil {
		t.Fatalf("GetStored 禁用模型应可取到: %v", err)
	}
	if got.Enabled {
		t.Errorf("m2 应为禁用状态")
	}

	// APIKey 展开环境变量
	got, err = s.GetStored(ctx, "m1")
	if err != nil || got.APIKey != "sk-stored-env" {
		t.Errorf("GetStored 应展开 APIKey 环境变量: %v, %q", err, got.APIKey)
	}
}

func TestModelService_UpdateRenameSuccess(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if err := s.Create(ctx, validModel("m1")); err != nil { // 自动默认
		t.Fatalf("Create m1: %v", err)
	}

	upd := validModel("m1b")
	upd.APIKey = "" // 留空 = 保持原值
	if err := s.Update(ctx, "m1", upd); err != nil {
		t.Fatalf("Update 重命名: %v", err)
	}

	if _, err := s.GetByName(ctx, "m1"); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("旧名应不存在, got %v", err)
	}
	got, err := s.GetByName(ctx, "m1b")
	if err != nil {
		t.Fatalf("新名应可取到: %v", err)
	}
	if got.APIKey != "sk-test-1234abcd" {
		t.Errorf("api_key 留空应保持原值, got %q", got.APIKey)
	}
	def, err := s.GetByName(ctx, "")
	if err != nil || def.Name != "m1b" {
		t.Errorf("默认模型应随重命名保留为 m1b: %v, %+v", err, def)
	}
}

func TestMaskAPIKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"sk-abcdefgh1234", "****1234"},
		{"short", "****"},
		{"", ""},
		{"${OPENAI_API_KEY}", "${OPENAI_API_KEY}"}, // 环境变量引用原样展示
	}
	for _, c := range cases {
		if got := MaskAPIKey(c.in); got != c.want {
			t.Errorf("MaskAPIKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestModelService_ResolveByFlag(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	for _, n := range []string{"m1", "m2"} {
		if err := s.Create(ctx, validModel(n)); err != nil {
			t.Fatalf("Create %s: %v", n, err)
		}
	}

	// 名称优先，与类型无关
	if m, err := s.Resolve(ctx, "m2", repo.DefaultVoice); err != nil || m.Name != "m2" {
		t.Errorf("按名称解析: %v, %+v", err, m)
	}

	// 无该类型默认：可被识别为 ErrNoDefaultModel，信息区分类型
	_, err := s.Resolve(ctx, "", repo.DefaultVoice)
	if !errors.Is(err, ErrNoDefaultModel) || !strings.Contains(err.Error(), "默认语音模型") {
		t.Errorf("无语音默认应返回语音类错误, got %v", err)
	}
	_, err = s.Resolve(ctx, "", repo.DefaultVision)
	if !errors.Is(err, ErrNoDefaultModel) || !strings.Contains(err.Error(), "默认视觉模型") {
		t.Errorf("无视觉默认应返回视觉类错误, got %v", err)
	}

	// 按类型回落
	if err := s.SetDefault(ctx, "m2", repo.DefaultVoice); err != nil {
		t.Fatalf("SetDefault m2 voice: %v", err)
	}
	if m, err := s.Resolve(ctx, "", repo.DefaultVoice); err != nil || m.Name != "m2" {
		t.Errorf("默认语音模型应为 m2: %v, %+v", err, m)
	}
	if m, err := s.Resolve(ctx, "", repo.DefaultChat); err != nil || m.Name != "m1" {
		t.Errorf("默认对话模型应仍为 m1: %v, %+v", err, m)
	}

	// 一模型多默认
	if err := s.SetDefault(ctx, "m1", repo.DefaultVision); err != nil {
		t.Fatalf("SetDefault m1 vision: %v", err)
	}
	if m, err := s.Resolve(ctx, "", repo.DefaultVision); err != nil || m.Name != "m1" {
		t.Errorf("默认视觉模型应为 m1: %v, %+v", err, m)
	}
}

// TestModelService_NoChatDefaultMessage 空库时 GetByName("") 的错误信息保持对话类文案。
func TestModelService_NoChatDefaultMessage(t *testing.T) {
	s := newTestService(t)
	_, err := s.GetByName(context.Background(), "")
	if !errors.Is(err, ErrNoDefaultModel) || err.Error() != ErrNoDefaultModel.Error() {
		t.Errorf("want %q, got %v", ErrNoDefaultModel, err)
	}
}

// TestModelService_CreateIgnoresDefaultFlags 非首个模型创建时忽略调用方传入的默认类型。
func TestModelService_CreateIgnoresDefaultFlags(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if err := s.Create(ctx, validModel("m1")); err != nil {
		t.Fatalf("Create m1: %v", err)
	}
	m2 := validModel("m2")
	m2.DefaultFlags = repo.DefaultChat | repo.DefaultVoice
	if err := s.Create(ctx, m2); err != nil {
		t.Fatalf("Create m2: %v", err)
	}
	got, _ := s.GetStored(ctx, "m2")
	if got.DefaultFlags != 0 {
		t.Errorf("非首个模型 default_flags 应为 0, got %d", got.DefaultFlags)
	}
	first, _ := s.GetStored(ctx, "m1")
	if first.DefaultFlags != repo.DefaultChat {
		t.Errorf("首个模型应只持有 chat, got %d", first.DefaultFlags)
	}
}

func TestModelService_VoiceDefaultProtection(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	for _, n := range []string{"m1", "m2"} {
		if err := s.Create(ctx, validModel(n)); err != nil {
			t.Fatalf("Create %s: %v", n, err)
		}
	}
	if err := s.SetDefault(ctx, "m2", repo.DefaultVoice); err != nil {
		t.Fatalf("SetDefault m2 voice: %v", err)
	}

	// 持有语音默认同样受保护
	if err := s.Delete(ctx, "m2"); !errors.Is(err, ErrDefaultProtected) {
		t.Errorf("删除默认语音模型应被拒绝, got %v", err)
	}
	upd := validModel("m2")
	upd.APIKey = ""
	upd.Enabled = false
	if err := s.Update(ctx, "m2", upd); !errors.Is(err, ErrDefaultProtected) {
		t.Errorf("禁用默认语音模型应被拒绝, got %v", err)
	}

	// 取消语音默认后可删除
	if err := s.ClearDefault(ctx, "m2", repo.DefaultVoice); err != nil {
		t.Fatalf("ClearDefault m2 voice: %v", err)
	}
	if err := s.Delete(ctx, "m2"); err != nil {
		t.Errorf("取消默认后应可删除: %v", err)
	}
}

func TestModelService_ClearDefault(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if err := s.Create(ctx, validModel("m1")); err != nil {
		t.Fatalf("Create m1: %v", err)
	}

	if err := s.ClearDefault(ctx, "m1", repo.DefaultChat); !errors.Is(err, ErrChatDefaultRequired) {
		t.Errorf("取消对话默认应被拒绝, got %v", err)
	}
	if m, err := s.GetByName(ctx, ""); err != nil || m.Name != "m1" {
		t.Errorf("拒绝后对话默认应仍为 m1: %v, %+v", err, m)
	}
	// 未持有该类型：直接成功
	if err := s.ClearDefault(ctx, "m1", repo.DefaultVoice); err != nil {
		t.Errorf("取消未持有的默认应成功, got %v", err)
	}
	if err := s.ClearDefault(ctx, "ghost", repo.DefaultVoice); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("want ErrModelNotFound, got %v", err)
	}
}
