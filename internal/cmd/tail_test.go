package cmd

import (
	"os"
	"testing"
)

func TestValidateLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		hasError bool
	}{
		{"error", "error", false},
		{"err", "error", false},
		{"ERROR", "error", false},
		{"Err", "error", false},
		{"warn", "warn", false},
		{"warning", "warn", false},
		{"WARN", "warn", false},
		{"Warning", "warn", false},
		{"info", "info", false},
		{"INFO", "info", false},
		{"debug", "debug", false},
		{"DEBUG", "debug", false},
		{"invalid", "", true},
		{"trace", "", true},
		{"", "", true},
	}

	for _, tt := range tests {
		result, err := validateLevel(tt.input)
		if tt.hasError {
			if err == nil {
				t.Errorf("validateLevel(%q) expected error, got none", tt.input)
			}
		} else {
			if err != nil {
				t.Errorf("validateLevel(%q) unexpected error: %v", tt.input, err)
			}
			if result != tt.expected {
				t.Errorf("validateLevel(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		}
	}
}

func TestGetDefaultHome(t *testing.T) {
	// Test with GROOT_HOME set
	os.Setenv("GROOT_HOME", "/custom/groot")
	home := getDefaultHome()
	if home != "/custom/groot" {
		t.Errorf("getDefaultHome() = %q, want %q", home, "/custom/groot")
	}

	// Test without GROOT_HOME
	os.Unsetenv("GROOT_HOME")
	home = getDefaultHome()
	homeEnv := os.Getenv("HOME")
	expected := homeEnv + "/.groot"
	if homeEnv != "" && home != expected {
		t.Errorf("getDefaultHome() = %q, want %q", home, expected)
	}
}

func TestParseTailFlags(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		expected  *TailFlags
		hasError  bool
		errorMsg  string
	}{
		{
			name: "default values",
			args: []string{},
			expected: &TailFlags{
				NLines:  100,
				Level:   "",
				Keyword: "",
			},
			hasError: false,
		},
		{
			name: "set nlines",
			args: []string{"-n", "50"},
			expected: &TailFlags{
				NLines:  50,
				Level:   "",
				Keyword: "",
			},
			hasError: false,
		},
		{
			name:     "nlines missing value",
			args:     []string{"-n"},
			hasError: true,
			errorMsg: "-n requires a value",
		},
		{
			name:     "nlines invalid value",
			args:     []string{"-n", "abc"},
			hasError: true,
		},
		{
			name:     "nlines negative value",
			args:     []string{"-n", "-10"},
			hasError: true,
			errorMsg: "-n must be a positive integer",
		},
		{
			name: "set level error",
			args: []string{"-l", "error"},
			expected: &TailFlags{
				NLines:  100,
				Level:   "error",
				Keyword: "",
			},
			hasError: false,
		},
		{
			name: "set level err alias",
			args: []string{"-l", "err"},
			expected: &TailFlags{
				NLines:  100,
				Level:   "error",
				Keyword: "",
			},
			hasError: false,
		},
		{
			name: "set level warn",
			args: []string{"-l", "warning"},
			expected: &TailFlags{
				NLines:  100,
				Level:   "warn",
				Keyword: "",
			},
			hasError: false,
		},
		{
			name:     "invalid level",
			args:     []string{"-l", "invalid"},
			hasError: true,
		},
		{
			name: "set keyword",
			args: []string{"-k", "timeout"},
			expected: &TailFlags{
				NLines:  100,
				Level:   "",
				Keyword: "timeout",
			},
			hasError: false,
		},
		{
			name:     "keyword missing value",
			args:     []string{"-k"},
			hasError: true,
			errorMsg: "-k requires a value",
		},
		{
			name: "set home with -H",
			args: []string{"-H", "/custom/path"},
			expected: &TailFlags{
				NLines:  100,
				Level:   "",
				Keyword: "",
				HomeDir: "/custom/path",
			},
			hasError: false,
		},
		{
			name: "set home with --home",
			args: []string{"--home", "/custom/path"},
			expected: &TailFlags{
				NLines:  100,
				Level:   "",
				Keyword: "",
				HomeDir: "/custom/path",
			},
			hasError: false,
		},
		{
			name: "multiple flags",
			args: []string{"-n", "200", "-l", "warn", "-k", "timeout"},
			expected: &TailFlags{
				NLines:  200,
				Level:   "warn",
				Keyword: "timeout",
			},
			hasError: false,
		},
		{
			name:     "unknown flag",
			args:     []string{"-x"},
			hasError: true,
		},
		{
			name:     "unexpected argument",
			args:     []string{"something"},
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear GROOT_HOME for consistent testing
			os.Unsetenv("GROOT_HOME")

			result, err := ParseTailFlags(tt.args)
			if tt.hasError {
				if err == nil {
					t.Errorf("ParseTailFlags() expected error, got none")
				}
				if tt.errorMsg != "" && err.Error() != tt.errorMsg {
					t.Errorf("ParseTailFlags() error = %q, want %q", err.Error(), tt.errorMsg)
				}
			} else {
				if err != nil {
					t.Errorf("ParseTailFlags() unexpected error: %v", err)
				}
				if tt.expected != nil {
					if result.NLines != tt.expected.NLines {
						t.Errorf("NLines = %d, want %d", result.NLines, tt.expected.NLines)
					}
					if result.Level != tt.expected.Level {
						t.Errorf("Level = %q, want %q", result.Level, tt.expected.Level)
					}
					if result.Keyword != tt.expected.Keyword {
						t.Errorf("Keyword = %q, want %q", result.Keyword, tt.expected.Keyword)
					}
					if tt.expected.HomeDir != "" && result.HomeDir != tt.expected.HomeDir {
						t.Errorf("HomeDir = %q, want %q", result.HomeDir, tt.expected.HomeDir)
					}
				}
			}
		})
	}
}