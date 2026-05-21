package chat

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractFileRefs(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"single file", "帮我分析 @/home/zfd/aa.txt", []string{"/home/zfd/aa.txt"}},
		{"multiple files", "对比 @/a.txt 和 @/b.png", []string{"/a.txt", "/b.png"}},
		{"directory", "分析 @/var/log/ 下的文件", []string{"/var/log/"}},
		{"no refs", "帮我分析这个", nil},
		{"duplicate refs", "@/a.txt 还有 @/a.txt", []string{"/a.txt"}},
		{"mixed text", "查看 @/etc/hosts 和 @/tmp/data", []string{"/etc/hosts", "/tmp/data"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractFileRefs(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("ExtractFileRefs(%q) = %v, want %v", tt.input, got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ExtractFileRefs(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestGuessFileType(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"photo.png", "image"},
		{"doc.jpg", "image"},
		{"music.mp3", "audio"},
		{"video.mp4", "video"},
		{"data.txt", "file"},
		{"README", "file"},
		{"archive.tar.gz", "file"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := guessFileType(tt.name)
			if got != tt.want {
				t.Errorf("guessFileType(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestExtractActiveFileRef(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"@/home/zfd/", "/home/zfd/"},
		{"帮我分析 @/var/lo", "/var/lo"},
		{"没有引用", ""},
		{"@", ""},
		{"@ ", ""},
		{"帮我 @/a", "/a"},
		{"帮我 @/a 还有别的", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := extractActiveFileRef(tt.input)
			if got != tt.want {
				t.Errorf("extractActiveFileRef(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStripFileRefs(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		pathToNames map[string][]string
		want        string
	}{
		{
			name:        "single file",
			text:        "帮我分析 @/home/zfd/aa.txt",
			pathToNames: map[string][]string{"/home/zfd/aa.txt": {"aa.txt"}},
			want:        "帮我分析 aa.txt",
		},
		{
			name:        "multiple files",
			text:        "对比 @/a.txt 和 @/b.png",
			pathToNames: map[string][]string{"/a.txt": {"a.txt"}, "/b.png": {"b.png"}},
			want:        "对比 a.txt 和 b.png",
		},
		{
			name:        "directory",
			text:        "分析 @/var/log/ 下的文件",
			pathToNames: map[string][]string{"/var/log/": {"app.log", "error.log"}},
			want:        "分析 app.log error.log 下的文件",
		},
		{
			name:        "no refs",
			text:        "帮我分析",
			pathToNames: map[string][]string{},
			want:        "帮我分析",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripFileRefs(tt.text, tt.pathToNames)
			if got != tt.want {
				t.Errorf("StripFileRefs(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

func TestReadAttachments_File(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(filePath, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	attachments, pathToNames, err := ReadAttachments([]string{filePath})
	if err != nil {
		t.Fatalf("ReadAttachments failed: %v", err)
	}

	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}

	if attachments[0].Name != "test.txt" {
		t.Errorf("Name = %q, want test.txt", attachments[0].Name)
	}

	if attachments[0].Type != "file" {
		t.Errorf("Type = %q, want file", attachments[0].Type)
	}

	if attachments[0].Content == "" {
		t.Error("Content should not be empty")
	}

	if pathToNames[filePath][0] != "test.txt" {
		t.Errorf("pathToNames[%q] = %v, want [test.txt]", filePath, pathToNames[filePath])
	}
}

func TestReadAttachments_Directory(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "b.png"), []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}
	// 子目录应该被跳过
	if err := os.Mkdir(filepath.Join(tmpDir, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}

	attachments, pathToNames, err := ReadAttachments([]string{tmpDir})
	if err != nil {
		t.Fatalf("ReadAttachments failed: %v", err)
	}

	if len(attachments) != 2 {
		t.Fatalf("expected 2 attachments, got %d", len(attachments))
	}

	names := make(map[string]bool)
	for _, att := range attachments {
		names[att.Name] = true
	}
	if !names["a.txt"] || !names["b.png"] {
		t.Errorf("expected a.txt and b.png, got %v", names)
	}

	if len(pathToNames[tmpDir]) != 2 {
		t.Errorf("expected 2 names for dir, got %v", pathToNames[tmpDir])
	}
}

func TestReadAttachments_NotFound(t *testing.T) {
	_, _, err := ReadAttachments([]string{"/nonexistent/path/file.txt"})
	if err == nil {
		t.Error("expected error for non-existent path")
	}
}

func TestReadAttachments_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	_, _, err := ReadAttachments([]string{tmpDir})
	if err == nil {
		t.Error("expected error for empty directory")
	}
}

func TestListPathItems(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "alpha.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "beta.png"), []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(tmpDir, "gamma"), 0755); err != nil {
		t.Fatal(err)
	}

	items := listPathItems(tmpDir + "/")
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d (items: %v)", len(items), items)
	}

	// 带过滤前缀
	items = listPathItems(tmpDir + "/a")
	if len(items) != 1 || !containsItem(items, "alpha.txt") {
		t.Errorf("expected alpha.txt, got %v", items)
	}
}

func containsItem(items []CompletionItem, name string) bool {
	for _, item := range items {
		if filepath.Base(item.Name) == name {
			return true
		}
	}
	return false
}

func TestExtractFileRefs_BarePath(t *testing.T) {
	// 创建临时文件用于裸路径检测
	tmpDir := t.TempDir()
	fileA := filepath.Join(tmpDir, "a.txt")
	if err := os.WriteFile(fileA, []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	fileB := filepath.Join(tmpDir, "b.png")
	if err := os.WriteFile(fileB, []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}

	// 拖拽进来的裸路径（文件存在）
	text := "帮我分析 " + fileA
	refs := ExtractFileRefs(text)
	if len(refs) != 1 || refs[0] != fileA {
		t.Errorf("bare path should be extracted: got %v", refs)
	}

	// @path 和裸路径混用
	text = "对比 " + fileA + " 和 @" + fileA
	refs = ExtractFileRefs(text)
	if len(refs) != 1 {
		t.Errorf("duplicate @path and bare path should be deduplicated: got %v", refs)
	}

	// 不存在的裸路径不能提取
	refs = ExtractFileRefs("查看 /nonexistent/file.txt")
	if len(refs) != 0 {
		t.Errorf("nonexistent bare path should not be extracted: got %v", refs)
	}
}

func TestStripFileRefs_BarePath(t *testing.T) {
	text := "帮我分析 /home/zfd/aa.txt 的内容"
	pathToNames := map[string][]string{"/home/zfd/aa.txt": {"aa.txt"}}
	got := StripFileRefs(text, pathToNames)
	want := "帮我分析 aa.txt 的内容"
	if got != want {
		t.Errorf("StripFileRefs(%q) = %q, want %q", text, got, want)
	}
}

func TestReadAttachments_ImageType(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "photo.png")
	if err := os.WriteFile(filePath, []byte("fake image data"), 0644); err != nil {
		t.Fatal(err)
	}

	attachments, _, err := ReadAttachments([]string{filePath})
	if err != nil {
		t.Fatal(err)
	}

	if attachments[0].Type != "image" {
		t.Errorf("Type = %q, want image", attachments[0].Type)
	}
}

func TestAutoPrefixBarePaths(t *testing.T) {
	tmpDir := t.TempDir()
	fileA := filepath.Join(tmpDir, "a.txt")
	fileB := filepath.Join(tmpDir, "b.txt")
	if err := os.WriteFile(fileA, []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		input   string
		want    string
		changed bool
	}{
		{
			name:    "single bare path at start",
			input:   fileA,
			want:    "@" + fileA,
			changed: true,
		},
		{
			name:    "bare path in middle of text",
			input:   "帮我分析 " + fileA + " 的内容",
			want:    "帮我分析 @" + fileA + " 的内容",
			changed: true,
		},
		{
			name:    "already has @ prefix",
			input:   "@" + fileA,
			want:    "@" + fileA,
			changed: false,
		},
		{
			name:    "no bare paths",
			input:   "帮我分析一下",
			want:   "帮我分析一下",
			changed: false,
		},
		{
			name:    "nonexistent bare path",
			input:   "分析 /nonexistent/path/file.txt",
			want:   "分析 /nonexistent/path/file.txt",
			changed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := autoPrefixBarePaths(tt.input)
			if got != tt.want {
				t.Errorf("autoPrefixBarePaths(%q) = %q, want %q", tt.input, got, tt.want)
			}
			if changed != tt.changed {
				t.Errorf("autoPrefixBarePaths(%q) changed = %v, want %v", tt.input, changed, tt.changed)
			}
		})
	}
}
