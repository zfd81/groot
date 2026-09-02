package llm

import (
	"context"
	"errors"
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
	if err := s.SetDefault(ctx, "m2"); err != nil {
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

	if err := s.SetDefault(ctx, "m2"); !errors.Is(err, ErrModelDisabled) {
		t.Errorf("禁用模型不可设为默认, got %v", err)
	}
	if err := s.SetDefault(ctx, "ghost"); !errors.Is(err, ErrModelNotFound) {
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
