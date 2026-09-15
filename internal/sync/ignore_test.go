package sync

import "testing"

// TestIsIgnoredPath 覆盖被忽略与不被忽略两类路径。
// 重点是「文件名恰好包含但不等于被忽略名」的情况(如 my.DS_Store.md)
// 不应被误伤,以及多层目录下的判断与顶层一致。
func TestIsIgnoredPath(t *testing.T) {
	ignored := []string{
		"subagents/.DS_Store",
		"subagents/weather/skills/.DS_Store",
		".DS_Store",
		"skills/Thumbs.db",
		"skills/weather/._SKILL.md",
		"skills/weather/SKILL.md.tmp",
		"probe.tmp",
	}
	for _, p := range ignored {
		if !IsIgnoredPath(p) {
			t.Errorf("IsIgnoredPath(%q) = false, want true", p)
		}
	}

	kept := []string{
		"config.yaml",
		"GROOT.md",
		"skills/weather/SKILL.md",
		"subagents/weather/agent.md",
		"skills/my.DS_Store.md",      // 含被忽略名但不等于它
		"skills/DS_Store",            // 无前导点
		"skills/weather/tmp/note.md", // 目录叫 tmp,文件本身不是 .tmp
		"skills/_private.md",         // 单下划线,不是 AppleDouble 的 "._"
	}
	for _, p := range kept {
		if IsIgnoredPath(p) {
			t.Errorf("IsIgnoredPath(%q) = true, want false", p)
		}
	}
}
