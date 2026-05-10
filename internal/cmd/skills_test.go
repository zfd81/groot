package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSkillsFlags(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantSub   string
		wantPath  string
		wantName  string
		wantError bool
		errMsg    string
	}{
		{
			name:     "list subcommand",
			args:     []string{"list"},
			wantSub:  "list",
		},
		{
			name:     "install subcommand with path",
			args:     []string{"install", "/tmp/my-skill"},
			wantSub:  "install",
			wantPath: "/tmp/my-skill",
		},
		{
			name:     "uninstall subcommand with name",
			args:     []string{"uninstall", "my-skill"},
			wantSub:  "uninstall",
			wantName: "my-skill",
		},
		{
			name:      "no arguments",
			args:      []string{},
			wantError: true,
			errMsg:    "缺少子命令: list, install, uninstall",
		},
		{
			name:      "unknown subcommand",
			args:      []string{"delete"},
			wantError: true,
			errMsg:    "未知子命令: delete (可用: list, install, uninstall)",
		},
		{
			name:      "install without path",
			args:      []string{"install"},
			wantError: true,
			errMsg:    "install 子命令需要指定 Skill 路径",
		},
		{
			name:      "uninstall without name",
			args:      []string{"uninstall"},
			wantError: true,
			errMsg:    "uninstall 子命令需要指定 Skill 名称",
		},
		{
			name:      "list with unexpected arg",
			args:      []string{"list", "extra"},
			wantError: true,
			errMsg:    "unexpected argument: extra",
		},
		{
			name:      "install with too many args",
			args:      []string{"install", "/path1", "/path2"},
			wantError: true,
			errMsg:    "install 子命令只接受一个路径参数",
		},
		{
			name:      "unknown flag",
			args:      []string{"list", "--invalid"},
			wantError: true,
			errMsg:    "unknown flag: --invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags, err := ParseSkillsFlags(tt.args)

			if tt.wantError {
				if err == nil {
					t.Errorf("expected error but got nil")
				} else if err.Error() != tt.errMsg {
					t.Errorf("expected error '%s' but got '%s'", tt.errMsg, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if flags.Subcommand != tt.wantSub {
				t.Errorf("expected subcommand '%s' but got '%s'", tt.wantSub, flags.Subcommand)
			}
			if flags.Path != tt.wantPath {
				t.Errorf("expected path '%s' but got '%s'", tt.wantPath, flags.Path)
			}
			if flags.Name != tt.wantName {
				t.Errorf("expected name '%s' but got '%s'", tt.wantName, flags.Name)
			}
		})
	}
}

func TestReadSkillDescription(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "with description",
			content: `---
name: test-skill
description: "A test skill"
---
Some content here`,
			want: "A test skill",
		},
		{
			name: "with unquoted description",
			content: `---
name: test-skill
description: A simple description
---
Content`,
			want: "A simple description",
		},
		{
			name: "no description field",
			content: `---
name: test-skill
---
Content`,
			want: "",
		},
		{
			name:    "no frontmatter",
			content: `Just some content`,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			path := filepath.Join(tmpDir, "SKILL.md")
			os.WriteFile(path, []byte(tt.content), 0644)

			got, err := readSkillDescription(path)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("expected '%s' but got '%s'", tt.want, got)
			}
		})
	}
}

func TestSkillsList(t *testing.T) {
	// Setup: create skills dir with some valid and invalid skills
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "skills")
	os.MkdirAll(skillsDir, 0755)

	// Valid skill with description
	os.MkdirAll(filepath.Join(skillsDir, "valid-skill"), 0755)
	os.WriteFile(filepath.Join(skillsDir, "valid-skill", "SKILL.md"), []byte(`---
name: valid-skill
description: "A valid skill"
---
Content`), 0644)

	// Valid skill without description
	os.MkdirAll(filepath.Join(skillsDir, "no-desc-skill"), 0755)
	os.WriteFile(filepath.Join(skillsDir, "no-desc-skill", "SKILL.md"), []byte(`---
name: no-desc-skill
---
Content`), 0644)

	// Invalid skill (no SKILL.md)
	os.MkdirAll(filepath.Join(skillsDir, "broken-skill"), 0755)

	// A file (not a directory) should be ignored
	os.WriteFile(filepath.Join(skillsDir, "readme.txt"), []byte("hello"), 0644)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := skillsList(skillsDir)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	checks := []string{
		"NAME",
		"LAST_UPDATED",
		"DESCRIPTION",
		"valid-skill",
		"A valid skill",
		"no-desc-skill",
		"broken-skill",
		"缺少 SKILL.md",
		"共 3 个 Skill",
		"2 个有效",
		"1 个异常",
	}

	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output should contain '%s', got:\n%s", check, output)
		}
	}
}

func TestSkillsList_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "skills")
	os.MkdirAll(skillsDir, 0755)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := skillsList(skillsDir)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, "未安装任何 Skill") {
		t.Errorf("output should contain '未安装任何 Skill', got:\n%s", output)
	}
}

func TestSkillsList_NonexistentDir(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := skillsList("/nonexistent/path/skills")

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, "未安装任何 Skill") {
		t.Errorf("output should contain '未安装任何 Skill', got:\n%s", output)
	}
}

func TestSkillsInstall(t *testing.T) {
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "skills")
	os.MkdirAll(skillsDir, 0755)

	// Create source skill
	srcDir := filepath.Join(tmpDir, "my-skill")
	os.MkdirAll(srcDir, 0755)
	os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte("---\nname: my-skill\n---\nContent"), 0644)
	os.WriteFile(filepath.Join(srcDir, "helper.py"), []byte("print('hello')"), 0644)
	os.MkdirAll(filepath.Join(srcDir, "data"), 0755)
	os.WriteFile(filepath.Join(srcDir, "data", "config.json"), []byte("{}"), 0644)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := skillsInstall(skillsDir, srcDir)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, "安装成功") {
		t.Errorf("output should contain '安装成功', got:\n%s", output)
	}

	// Verify files were copied
	if _, err := os.Stat(filepath.Join(skillsDir, "my-skill", "SKILL.md")); os.IsNotExist(err) {
		t.Error("SKILL.md was not copied")
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "my-skill", "helper.py")); os.IsNotExist(err) {
		t.Error("helper.py was not copied")
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "my-skill", "data", "config.json")); os.IsNotExist(err) {
		t.Error("data/config.json was not copied")
	}
}

func TestSkillsInstall_Overwrite(t *testing.T) {
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "skills")
	os.MkdirAll(skillsDir, 0755)

	// Pre-create existing skill
	existingDir := filepath.Join(skillsDir, "my-skill")
	os.MkdirAll(existingDir, 0755)
	os.WriteFile(filepath.Join(existingDir, "old_file.txt"), []byte("old"), 0644)

	// Create source skill (newer version)
	srcDir := filepath.Join(tmpDir, "my-skill")
	os.MkdirAll(srcDir, 0755)
	os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte("---\nname: my-skill\n---\nNew content"), 0644)
	os.WriteFile(filepath.Join(srcDir, "new_file.txt"), []byte("new"), 0644)

	err := skillsInstall(skillsDir, srcDir)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Old file should be gone
	if _, err := os.Stat(filepath.Join(skillsDir, "my-skill", "old_file.txt")); !os.IsNotExist(err) {
		t.Error("old_file.txt should have been removed during overwrite")
	}
	// New files should exist
	if _, err := os.Stat(filepath.Join(skillsDir, "my-skill", "SKILL.md")); os.IsNotExist(err) {
		t.Error("SKILL.md was not copied")
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "my-skill", "new_file.txt")); os.IsNotExist(err) {
		t.Error("new_file.txt was not copied")
	}
}

func TestSkillsInstall_NoSkillMd(t *testing.T) {
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "skills")
	os.MkdirAll(skillsDir, 0755)

	// Create source without SKILL.md
	srcDir := filepath.Join(tmpDir, "no-skillmd")
	os.MkdirAll(srcDir, 0755)
	os.WriteFile(filepath.Join(srcDir, "readme.txt"), []byte("no SKILL.md here"), 0644)

	err := skillsInstall(skillsDir, srcDir)
	if err == nil {
		t.Fatal("expected error for missing SKILL.md")
	}
	if !strings.Contains(err.Error(), "缺少 SKILL.md") {
		t.Errorf("error should mention missing SKILL.md, got: %s", err.Error())
	}
}

func TestSkillsInstall_NotExist(t *testing.T) {
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "skills")
	os.MkdirAll(skillsDir, 0755)

	err := skillsInstall(skillsDir, "/nonexistent/path")
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
	if !strings.Contains(err.Error(), "源路径不存在") {
		t.Errorf("error should mention path not exist, got: %s", err.Error())
	}
}

func TestSkillsUninstall(t *testing.T) {
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "skills")
	os.MkdirAll(skillsDir, 0755)

	// Create skill to uninstall
	skillDir := filepath.Join(skillsDir, "to-remove")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("content"), 0644)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := skillsUninstall(skillsDir, "to-remove")

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, "已卸载") {
		t.Errorf("output should contain '已卸载', got:\n%s", output)
	}

	// Verify directory was removed
	if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
		t.Error("skill directory should have been removed")
	}
}

func TestSkillsUninstall_NotExist(t *testing.T) {
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "skills")
	os.MkdirAll(skillsDir, 0755)

	err := skillsUninstall(skillsDir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent skill")
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Errorf("error should mention not exist, got: %s", err.Error())
	}
}

func TestRunSkills_Integration(t *testing.T) {
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "skills")
	os.MkdirAll(skillsDir, 0755)

	// Create source skill
	srcDir := filepath.Join(tmpDir, "src-skill")
	os.MkdirAll(srcDir, 0755)
	os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte("---\nname: src-skill\ndescription: \"Test skill\"\n---\nContent"), 0644)

	// Override home dir
	oldHome := os.Getenv("GROOT_HOME")
	os.Setenv("GROOT_HOME", tmpDir)
	defer os.Setenv("GROOT_HOME", oldHome)

	// Test install
	{
		old := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		err := RunSkills(&SkillsFlags{Subcommand: "install", Path: srcDir})

		w.Close()
		os.Stdout = old

		if err != nil {
			t.Fatalf("install failed: %v", err)
		}

		var buf bytes.Buffer
		io.Copy(&buf, r)
		if !strings.Contains(buf.String(), "安装成功") {
			t.Error("install should succeed")
		}
	}

	// Test list
	{
		old := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		err := RunSkills(&SkillsFlags{Subcommand: "list"})

		w.Close()
		os.Stdout = old

		if err != nil {
			t.Fatalf("list failed: %v", err)
		}

		var buf bytes.Buffer
		io.Copy(&buf, r)
		output := buf.String()
		if !strings.Contains(output, "src-skill") {
			t.Error("list should contain src-skill")
		}
		if !strings.Contains(output, "Test skill") {
			t.Error("list should contain description")
		}
	}

	// Test uninstall
	{
		old := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		err := RunSkills(&SkillsFlags{Subcommand: "uninstall", Name: "src-skill"})

		w.Close()
		os.Stdout = old

		if err != nil {
			t.Fatalf("uninstall failed: %v", err)
		}

		var buf bytes.Buffer
		io.Copy(&buf, r)
		if !strings.Contains(buf.String(), "已卸载") {
			t.Error("uninstall should succeed")
		}
	}

	// Verify gone after uninstall
	{
		old := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		err := RunSkills(&SkillsFlags{Subcommand: "list"})

		w.Close()
		os.Stdout = old

		if err != nil {
			t.Fatalf("list failed: %v", err)
		}

		var buf bytes.Buffer
		io.Copy(&buf, r)
		output := buf.String()
		if !strings.Contains(output, "未安装任何 Skill") {
			t.Error("list after uninstall should show empty")
		}
	}
}
