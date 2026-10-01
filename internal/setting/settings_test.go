// internal/setting/settings_test.go
package setting

import (
	"context"
	"errors"
	"testing"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/repo"
)

// fakeSettingRepo 内存实现，键为 scope|scope_id|name
type fakeSettingRepo struct {
	data map[string]string
	err  error
}

func newFakeRepo() *fakeSettingRepo {
	return &fakeSettingRepo{data: map[string]string{}}
}

func key(scope repo.Scope, scopeID, name string) string {
	return string(scope) + "|" + scopeID + "|" + name
}

func (f *fakeSettingRepo) Get(ctx context.Context, scope repo.Scope, scopeID, name string) (*repo.Setting, error) {
	if f.err != nil {
		return nil, f.err
	}
	v, ok := f.data[key(scope, scopeID, name)]
	if !ok {
		return nil, repo.ErrNotFound
	}
	return &repo.Setting{Scope: scope, ScopeID: scopeID, Name: name, Value: v}, nil
}

// ListByScope 遍历 map 返回，不保证顺序（真实仓库按 name 升序）。
func (f *fakeSettingRepo) ListByScope(ctx context.Context, scope repo.Scope, scopeID string) ([]*repo.Setting, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []*repo.Setting
	prefix := key(scope, scopeID, "")
	for k, v := range f.data {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			out = append(out, &repo.Setting{
				Scope: scope, ScopeID: scopeID, Name: k[len(prefix):], Value: v,
			})
		}
	}
	return out, nil
}

func (f *fakeSettingRepo) Upsert(ctx context.Context, items ...*repo.Setting) error {
	if f.err != nil {
		return f.err
	}
	for _, s := range items {
		f.data[key(s.Scope, s.ScopeID, s.Name)] = s.Value
	}
	return nil
}

func (f *fakeSettingRepo) Delete(ctx context.Context, scope repo.Scope, scopeID, name string) error {
	delete(f.data, key(scope, scopeID, name))
	return nil
}

func TestSettings_VoiceDefaults(t *testing.T) {
	s := New(config.Bootstrap{}, newFakeRepo())

	v, modelSet, err := s.Voice(context.Background())
	if err != nil {
		t.Fatalf("Voice: %v", err)
	}
	if want := defaultVoice(); v != want {
		t.Errorf("Voice = %+v, want %+v（表为空时应返回默认值）", v, want)
	}
	if modelSet {
		t.Error("表为空时 modelSet 应为 false")
	}
}

func TestSettings_VoicePartialOverride(t *testing.T) {
	f := newFakeRepo()
	f.data[key(repo.ScopeGlobal, "", KeyVoiceModel)] = "whisper-1"

	s := New(config.Bootstrap{}, f)
	v, modelSet, err := s.Voice(context.Background())
	if err != nil {
		t.Fatalf("Voice: %v", err)
	}
	if v.Model != "whisper-1" {
		t.Errorf("Model = %q, want whisper-1", v.Model)
	}
	if !modelSet {
		t.Error("voice.model 行存在时 modelSet 应为 true")
	}
	if v.AutoSend {
		t.Error("AutoSend 未在表中，应回落到默认值 false")
	}
}

// TestSettings_VoiceEmptyModelRowIsSet voice.model 行存在但为空串，同样视为已确定。
func TestSettings_VoiceEmptyModelRowIsSet(t *testing.T) {
	f := newFakeRepo()
	f.data[key(repo.ScopeGlobal, "", KeyVoiceModel)] = ""

	v, modelSet, err := New(config.Bootstrap{}, f).Voice(context.Background())
	if err != nil {
		t.Fatalf("Voice: %v", err)
	}
	if v.Model != "" || !modelSet {
		t.Errorf("Model=%q modelSet=%v, want 空串且 modelSet=true", v.Model, modelSet)
	}
}

func TestSettings_VoiceBoolParsing(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"true", true},
		{"false", false},
		{"1", true},
		{"0", false},
		{"", false},
		{"garbage", false}, // 无法解析时按默认值处理，不让脏数据导致取值失败
	}
	for _, c := range cases {
		f := newFakeRepo()
		f.data[key(repo.ScopeGlobal, "", KeyVoiceAutoSend)] = c.raw
		s := New(config.Bootstrap{}, f)
		v, _, err := s.Voice(context.Background())
		if err != nil {
			t.Fatalf("raw=%q Voice: %v", c.raw, err)
		}
		if v.AutoSend != c.want {
			t.Errorf("raw=%q AutoSend = %v, want %v", c.raw, v.AutoSend, c.want)
		}
	}
}

func TestSettings_SetVoiceRoundTrip(t *testing.T) {
	s := New(config.Bootstrap{}, newFakeRepo())
	ctx := context.Background()

	in := VoiceSettings{Model: "whisper-1", AutoSend: true}
	if err := s.SetVoice(ctx, in); err != nil {
		t.Fatalf("SetVoice: %v", err)
	}
	out, modelSet, err := s.Voice(ctx)
	if err != nil {
		t.Fatalf("Voice: %v", err)
	}
	if out != in || !modelSet {
		t.Errorf("回读 = %+v modelSet=%v, want %+v 且 modelSet=true", out, modelSet, in)
	}
}

// TestSettings_SetVoiceModelOnlyWritesModel SetVoiceModel 只写 voice.model，不碰 auto_send。
func TestSettings_SetVoiceModelOnlyWritesModel(t *testing.T) {
	f := newFakeRepo()
	s := New(config.Bootstrap{}, f)
	ctx := context.Background()

	if err := s.SetVoiceModel(ctx, "whisper-1"); err != nil {
		t.Fatalf("SetVoiceModel: %v", err)
	}
	if got := f.data[key(repo.ScopeGlobal, "", KeyVoiceModel)]; got != "whisper-1" {
		t.Errorf("voice.model = %q, want whisper-1", got)
	}
	if _, ok := f.data[key(repo.ScopeGlobal, "", KeyVoiceAutoSend)]; ok {
		t.Error("SetVoiceModel 不应写入 voice.auto_send")
	}
}

func TestSettings_BootstrapCategories(t *testing.T) {
	cfg := config.Bootstrap{}
	cfg.Server.Port = 8080

	s := New(cfg, newFakeRepo())
	ctx := context.Background()

	srv, err := s.Server(ctx)
	if err != nil {
		t.Fatalf("Server: %v", err)
	}
	if srv.Port != 8080 {
		t.Errorf("Port = %d, want 8080", srv.Port)
	}

	// 附件属于配置表分类：表为空时返回代码默认值，与 bootstrap 无关
	att, err := s.Attachment(ctx)
	if err != nil {
		t.Fatalf("Attachment: %v", err)
	}
	if want := defaultAttachment(); att.MaxSize != want.MaxSize {
		t.Errorf("MaxSize = %d, want 默认值 %d", att.MaxSize, want.MaxSize)
	}
}

func TestSettings_NilRepoUsesDefaults(t *testing.T) {
	// 配置表不可用时（如仓库未装配），来自表的分类回落到默认值而非 panic
	s := New(config.Bootstrap{}, nil)
	v, modelSet, err := s.Voice(context.Background())
	if err != nil {
		t.Fatalf("Voice: %v", err)
	}
	if v != defaultVoice() || modelSet {
		t.Errorf("Voice = %+v modelSet=%v, want 默认值且 modelSet=false", v, modelSet)
	}
}

func TestSettings_VoiceRepoError(t *testing.T) {
	f := newFakeRepo()
	f.err = errors.New("boom")
	_, _, err := New(config.Bootstrap{}, f).Voice(context.Background())
	if !errors.Is(err, f.err) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestSettings_SetVoiceNilRepo(t *testing.T) {
	err := New(config.Bootstrap{}, nil).SetVoice(context.Background(), VoiceSettings{})
	if !errors.Is(err, ErrNoSettingStore) {
		t.Fatalf("err = %v, want ErrNoSettingStore", err)
	}
}

func TestSettings_SetVoiceRepoError(t *testing.T) {
	f := newFakeRepo()
	f.err = errors.New("boom")
	err := New(config.Bootstrap{}, f).SetVoice(context.Background(), VoiceSettings{Model: "x"})
	if !errors.Is(err, f.err) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestSettings_SetVoiceModelNilRepo(t *testing.T) {
	err := New(config.Bootstrap{}, nil).SetVoiceModel(context.Background(), "x")
	if !errors.Is(err, ErrNoSettingStore) {
		t.Fatalf("err = %v, want ErrNoSettingStore", err)
	}
}

func TestSettings_SetVoiceModelRepoError(t *testing.T) {
	f := newFakeRepo()
	f.err = errors.New("boom")
	err := New(config.Bootstrap{}, f).SetVoiceModel(context.Background(), "x")
	if !errors.Is(err, f.err) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestParseBool(t *testing.T) {
	cases := []struct {
		raw      string
		fallback bool
		want     bool
	}{
		{"true", false, true},
		{"false", false, false},
		{"1", false, true},
		{"0", false, false},
		{"", false, false},
		{"garbage", false, false},
		{"garbage", true, true},
		{"", true, true},
		{"false", true, false},
	}
	for _, c := range cases {
		if got := parseBool(c.raw, c.fallback); got != c.want {
			t.Errorf("parseBool(%q, %v) = %v, want %v", c.raw, c.fallback, got, c.want)
		}
	}
}
