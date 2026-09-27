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

	v, err := s.Voice(context.Background())
	if err != nil {
		t.Fatalf("Voice: %v", err)
	}
	want := defaultVoice()
	if v != want {
		t.Errorf("Voice = %+v, want %+v（表为空时应返回默认值）", v, want)
	}
}

func TestSettings_VoicePartialOverride(t *testing.T) {
	f := newFakeRepo()
	f.data[key(repo.ScopeGlobal, "", KeyVoiceModel)] = "whisper-1"

	s := New(config.Bootstrap{}, f)
	v, err := s.Voice(context.Background())
	if err != nil {
		t.Fatalf("Voice: %v", err)
	}
	if v.Model != "whisper-1" {
		t.Errorf("Model = %q, want whisper-1", v.Model)
	}
	if v.Enabled != false {
		t.Error("Enabled 未在表中，应回落到默认值 false")
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
		f.data[key(repo.ScopeGlobal, "", KeyVoiceEnabled)] = c.raw
		s := New(config.Bootstrap{}, f)
		v, err := s.Voice(context.Background())
		if err != nil {
			t.Fatalf("raw=%q Voice: %v", c.raw, err)
		}
		if v.Enabled != c.want {
			t.Errorf("raw=%q Enabled = %v, want %v", c.raw, v.Enabled, c.want)
		}
	}
}

func TestSettings_SetVoiceRoundTrip(t *testing.T) {
	s := New(config.Bootstrap{}, newFakeRepo())
	ctx := context.Background()

	in := VoiceSettings{Enabled: true, Model: "whisper-1", AutoSend: true}
	if err := s.SetVoice(ctx, in); err != nil {
		t.Fatalf("SetVoice: %v", err)
	}
	out, err := s.Voice(ctx)
	if err != nil {
		t.Fatalf("Voice: %v", err)
	}
	if out != in {
		t.Errorf("回读 = %+v, want %+v", out, in)
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
	v, err := s.Voice(context.Background())
	if err != nil {
		t.Fatalf("Voice: %v", err)
	}
	if v != defaultVoice() {
		t.Errorf("Voice = %+v, want 默认值", v)
	}
}

func TestSettings_VoiceRepoError(t *testing.T) {
	f := newFakeRepo()
	f.err = errors.New("boom")
	_, err := New(config.Bootstrap{}, f).Voice(context.Background())
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
