package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ANSI color codes
const (
	ColorReset  = "\x1b[0m"
	ColorRed    = "\x1b[31m" // ERROR
	ColorGreen  = "\x1b[32m" // INFO
	ColorYellow = "\x1b[33m" // WARN
	ColorGray   = "\x1b[90m" // DEBUG
)

// LogEntry represents a parsed log entry
type LogEntry struct {
	Timestamp string
	Level     string
	Caller    string
	Message   string
	Event     string
	Extra     string
}

// Formatter formats log lines with colors and structured output
type Formatter struct{}

// NewFormatter creates a new Formatter instance
func NewFormatter() *Formatter {
	return &Formatter{}
}

// Format parses a JSON log line and returns a formatted, colored string
func (f *Formatter) Format(line string) string {
	entry, err := parseLogJSON(line)
	if err != nil {
		// If parsing fails, return the original line
		return line
	}

	output := buildOutput(entry)
	return applyColor(output, entry.Level)
}

// parseLogJSON parses a JSON log line into a LogEntry
func parseLogJSON(line string) (*LogEntry, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return nil, err
	}

	entry := &LogEntry{}

	// Extract timestamp
	if v, ok := raw["timestamp"]; ok {
		if s, ok := v.(string); ok {
			entry.Timestamp = s
		}
		delete(raw, "timestamp")
	}

	// Extract and normalize level
	if v, ok := raw["level"]; ok {
		if s, ok := v.(string); ok {
			entry.Level = strings.ToLower(s)
		}
		delete(raw, "level")
	}

	// Extract caller
	if v, ok := raw["caller"]; ok {
		if s, ok := v.(string); ok {
			entry.Caller = s
		}
		delete(raw, "caller")
	}

	// Extract message
	if v, ok := raw["message"]; ok {
		if s, ok := v.(string); ok {
			entry.Message = s
		}
		delete(raw, "message")
	}

	// Extract event
	if v, ok := raw["event"]; ok {
		if s, ok := v.(string); ok {
			entry.Event = s
		}
		delete(raw, "event")
	}

	// Build extra fields from remaining keys
	entry.Extra = buildExtraFields(raw)

	return entry, nil
}

// buildExtraFields formats remaining fields as "key=value" pairs
func buildExtraFields(raw map[string]interface{}) string {
	if len(raw) == 0 {
		return ""
	}

	var parts []string
	for k, v := range raw {
		var valueStr string
		switch val := v.(type) {
		case string:
			valueStr = val
		case float64:
			// JSON numbers are parsed as float64
			if val == float64(int64(val)) {
				valueStr = fmt.Sprintf("%d", int64(val))
			} else {
				valueStr = fmt.Sprintf("%v", val)
			}
		case bool:
			valueStr = fmt.Sprintf("%v", val)
		case nil:
			valueStr = "null"
		default:
			// For complex types (arrays, objects), marshal to JSON
			if b, err := json.Marshal(val); err == nil {
				valueStr = string(b)
			} else {
				valueStr = fmt.Sprintf("%v", val)
			}
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, valueStr))
	}

	return strings.Join(parts, "  ")
}

// buildOutput builds the formatted output string
func buildOutput(entry *LogEntry) string {
	var parts []string

	// Timestamp
	if entry.Timestamp != "" {
		parts = append(parts, entry.Timestamp)
	}

	// Level (5 chars: INFO, WARN, ERROR, DEBUG)
	if entry.Level != "" {
		level := strings.ToUpper(entry.Level)
		// Pad to 5 characters
		switch level {
		case "INFO":
			level = "INFO "
		case "WARN":
			level = "WARN "
		case "ERROR":
			level = "ERROR"
		case "DEBUG":
			level = "DEBUG"
		default:
			// For unknown levels, pad to 5 chars
			for len(level) < 5 {
				level += " "
			}
		}
		parts = append(parts, level)
	}

	// Caller
	if entry.Caller != "" {
		parts = append(parts, entry.Caller)
	}

	// Message
	if entry.Message != "" {
		parts = append(parts, entry.Message)
	}

	// Event
	if entry.Event != "" {
		parts = append(parts, fmt.Sprintf("event=%s", entry.Event))
	}

	// Extra fields
	if entry.Extra != "" {
		parts = append(parts, entry.Extra)
	}

	return strings.Join(parts, "  ")
}

// applyColor applies ANSI color based on log level
func applyColor(text, level string) string {
	var color string
	switch strings.ToLower(level) {
	case "error":
		color = ColorRed
	case "warn":
		color = ColorYellow
	case "info":
		color = ColorGreen
	case "debug":
		color = ColorGray
	default:
		// No color for unknown levels
		return text
	}

	return color + text + ColorReset
}
