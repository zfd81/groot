package repo

import "testing"

func TestDefaultFlag_StringParse(t *testing.T) {
	cases := []struct {
		f   DefaultFlag
		s   string
		val int
	}{
		{DefaultChat, "chat", 1},
		{DefaultASR, "asr", 2},
		{DefaultVision, "vision", 4},
		{DefaultTTS, "tts", 8},
	}
	for _, c := range cases {
		if int(c.f) != c.val {
			t.Errorf("%s 位值 = %d, want %d", c.s, int(c.f), c.val)
		}
		if c.f.String() != c.s {
			t.Errorf("%d.String()=%q want %q", c.f, c.f.String(), c.s)
		}
		got, ok := ParseDefaultFlag(c.s)
		if !ok || got != c.f {
			t.Errorf("Parse(%q)=%d,%v want %d", c.s, got, ok, c.f)
		}
	}
	for _, bad := range []string{"", "CHAT", "audio", "chat,asr", "voice"} {
		if _, ok := ParseDefaultFlag(bad); ok {
			t.Errorf("ParseDefaultFlag(%q) 应返回 false", bad)
		}
	}
	if DefaultTTS != 8 {
		t.Errorf("DefaultTTS=%d want 8", DefaultTTS)
	}
	if len(AllDefaultFlags) != 4 || AllDefaultFlags[0] != DefaultChat ||
		AllDefaultFlags[1] != DefaultASR || AllDefaultFlags[2] != DefaultVision ||
		AllDefaultFlags[3] != DefaultTTS {
		t.Errorf("AllDefaultFlags 顺序应为 chat、asr、vision、tts: %v", AllDefaultFlags)
	}
}

func TestModel_Has(t *testing.T) {
	m := &Model{DefaultFlags: DefaultChat | DefaultVision}
	if !m.Has(DefaultChat) || !m.Has(DefaultVision) {
		t.Error("应持有 chat 与 vision")
	}
	if m.Has(DefaultASR) || m.Has(DefaultTTS) {
		t.Error("不应持有 asr 或 tts")
	}
	if (&Model{}).Has(DefaultChat) {
		t.Error("零值模型不应持有任何默认类型")
	}
}
