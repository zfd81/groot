package repo

import "testing"

func TestDefaultFlag_StringAndParse(t *testing.T) {
	cases := []struct {
		flag DefaultFlag
		str  string
		val  int
	}{
		{DefaultChat, "chat", 1},
		{DefaultVoice, "voice", 2},
		{DefaultVision, "vision", 4},
	}
	for _, c := range cases {
		if int(c.flag) != c.val {
			t.Errorf("%s 位值 = %d, want %d", c.str, int(c.flag), c.val)
		}
		if c.flag.String() != c.str {
			t.Errorf("String() = %q, want %q", c.flag.String(), c.str)
		}
		got, ok := ParseDefaultFlag(c.str)
		if !ok || got != c.flag {
			t.Errorf("ParseDefaultFlag(%q) = %v, %v", c.str, got, ok)
		}
	}
	for _, bad := range []string{"", "CHAT", "audio", "chat,voice"} {
		if _, ok := ParseDefaultFlag(bad); ok {
			t.Errorf("ParseDefaultFlag(%q) 应返回 false", bad)
		}
	}
	if len(AllDefaultFlags) != 3 || AllDefaultFlags[0] != DefaultChat ||
		AllDefaultFlags[1] != DefaultVoice || AllDefaultFlags[2] != DefaultVision {
		t.Errorf("AllDefaultFlags 顺序应为 chat、voice、vision: %v", AllDefaultFlags)
	}
}

func TestModel_Has(t *testing.T) {
	m := &Model{DefaultFlags: DefaultChat | DefaultVision}
	if !m.Has(DefaultChat) || !m.Has(DefaultVision) {
		t.Error("应持有 chat 与 vision")
	}
	if m.Has(DefaultVoice) {
		t.Error("不应持有 voice")
	}
	if (&Model{}).Has(DefaultChat) {
		t.Error("零值模型不应持有任何默认类型")
	}
}
