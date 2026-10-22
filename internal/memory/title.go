package memory

import (
	"strings"
	"unicode/utf8"
)

// MaxTitleRunes 会话标题的最大长度（按 rune 计数）。
const MaxTitleRunes = 100

// TruncateTitle 去掉首尾空白后，按 rune 截断到 MaxTitleRunes，不加省略号。
func TruncateTitle(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= MaxTitleRunes {
		return s
	}
	return string([]rune(s)[:MaxTitleRunes])
}
