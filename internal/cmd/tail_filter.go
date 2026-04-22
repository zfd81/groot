package cmd

import (
	"encoding/json"
	"strings"
)

// Filter filters log lines based on level and keyword
type Filter struct {
	level    string // normalized lowercase
	keyword  string
}

// NewFilter creates a new Filter instance
// level is normalized to lowercase
func NewFilter(level, keyword string) *Filter {
	return &Filter{
		level:   strings.ToLower(level),
		keyword: keyword,
	}
}

// Match checks if a log line matches the filter criteria
// Logic:
//   - No filter (level=="" and keyword=="") → match all (return true)
//   - Level only → match if log's level equals filter level
//   - Keyword only → match if line contains keyword
//   - Both → both conditions must be true
//   - If JSON parsing fails: check keyword only (if keyword filter set, line must contain keyword; if only level filter, not match)
func (f *Filter) Match(line string) bool {
	// No filters → match all
	if f.level == "" && f.keyword == "" {
		return true
	}

	// Try to parse JSON to get level field
	logLevel := f.parseLogLevel(line)

	// If JSON parsing failed
	if logLevel == "" {
		// If only level filter is set, line doesn't match
		if f.level != "" && f.keyword == "" {
			return false
		}
		// If keyword filter is set (with or without level), check keyword only
		if f.keyword != "" {
			return strings.Contains(line, f.keyword)
		}
		return false
	}

	// JSON parsed successfully
	levelMatch := f.level == "" || logLevel == f.level
	keywordMatch := f.keyword == "" || strings.Contains(line, f.keyword)

	return levelMatch && keywordMatch
}

// parseLogLevel extracts and normalizes the level field from a JSON log line
// Returns empty string if parsing fails or level field not found
func (f *Filter) parseLogLevel(line string) string {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return ""
	}

	if v, ok := raw["level"]; ok {
		if s, ok := v.(string); ok {
			return strings.ToLower(s)
		}
	}

	return ""
}