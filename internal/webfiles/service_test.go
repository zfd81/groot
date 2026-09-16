package webfiles

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newServiceForTest 构造带典型 home 结构的 Service。
func newServiceForTest(t *testing.T) (*Service, string) {
	t.Helper()
	home := t.TempDir()
	mustMkdir := func(p string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(home, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite := func(p, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(home, p), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustMkdir("skills/my-skill")
	mustMkdir("mcp")
	mustMkdir("logs")
	mustWrite("GROOT.md", "# memo\n")
	mustWrite("config.yaml", "server:\n  port: 8080\n")
	mustWrite("env.yaml", "database:\n")
	mustWrite("groot.db", "SQLite format 3\x00")
	mustWrite("groot.db-wal", "\x00")
	mustWrite(".DS_Store", "junk")
	mustWrite("skills/my-skill/SKILL.md", "---\nname: my-skill\n---\n")
	svc, err := NewService(home)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, home
}

// entryNames 提取条目名列表。
func entryNames(entries []Entry) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name
	}
	return names
}

// TestService_List_Root 验证根目录列表：过滤隐藏项、目录在前、只读标记。
func TestService_List_Root(t *testing.T) {
	svc, _ := newServiceForTest(t)
	entries, err := svc.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	names := entryNames(entries)
	for _, banned := range []string{"groot.db", "groot.db-wal", ".DS_Store"} {
		for _, n := range names {
			if n == banned {
				t.Errorf("列表不应包含 %s", banned)
			}
		}
	}
	// 目录在前且按名排序：logs, mcp, skills 之后才是文件
	if len(names) < 6 {
		t.Fatalf("条目数不足: %v", names)
	}
	if names[0] != "logs" || names[1] != "mcp" || names[2] != "skills" {
		t.Errorf("目录排序不对: %v", names)
	}
	for _, e := range entries {
		wantRO := e.Name == "config.yaml" || e.Name == "env.yaml"
		if e.Readonly != wantRO {
			t.Errorf("%s readonly = %v, want %v", e.Name, e.Readonly, wantRO)
		}
	}
}

// TestService_List_NotFound 验证不存在的目录返回 ErrNotFound。
func TestService_List_NotFound(t *testing.T) {
	svc, _ := newServiceForTest(t)
	if _, err := svc.List("no-such-dir"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

// TestService_List_File 验证 List 作用于文件路径返回 ErrInvalid。
func TestService_List_File(t *testing.T) {
	svc, _ := newServiceForTest(t)
	if _, err := svc.List("GROOT.md"); !errors.Is(err, ErrInvalid) {
		t.Errorf("List 文件应 ErrInvalid, got %v", err)
	}
}

// TestService_Read 验证文本读取、只读标记、二进制与超限。
func TestService_Read(t *testing.T) {
	svc, home := newServiceForTest(t)

	fc, err := svc.Read("GROOT.md")
	if err != nil || fc.Content != "# memo\n" || fc.Readonly || fc.Binary {
		t.Errorf("Read(GROOT.md) = %+v, %v", fc, err)
	}

	fc, err = svc.Read("config.yaml")
	if err != nil || !fc.Readonly {
		t.Errorf("config.yaml 应可读且 readonly=true, got %+v, %v", fc, err)
	}

	if _, err = svc.Read("groot.db"); !errors.Is(err, ErrNotFound) {
		t.Errorf("读隐藏文件应 ErrNotFound, got %v", err)
	}

	// 二进制探测：含 NUL 字节
	if err := os.WriteFile(filepath.Join(home, "bin.dat"), []byte("ab\x00cd"), 0o644); err != nil {
		t.Fatal(err)
	}
	fc, err = svc.Read("bin.dat")
	if err != nil || !fc.Binary || fc.Content != "" {
		t.Errorf("二进制文件应 Binary=true 且不返回内容, got %+v, %v", fc, err)
	}

	// 超限
	big := strings.Repeat("x", MaxTextSize+1)
	if err := os.WriteFile(filepath.Join(home, "big.txt"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Read("big.txt"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("超限读取应 ErrTooLarge, got %v", err)
	}

	// 读目录
	if _, err = svc.Read("skills"); !errors.Is(err, ErrInvalid) {
		t.Errorf("读目录应 ErrInvalid, got %v", err)
	}
}

// TestService_DownloadPath 验证下载路径解析。
func TestService_DownloadPath(t *testing.T) {
	svc, _ := newServiceForTest(t)
	abs, name, err := svc.DownloadPath("skills/my-skill/SKILL.md")
	if err != nil || name != "SKILL.md" || abs == "" {
		t.Errorf("DownloadPath = %q, %q, %v", abs, name, err)
	}
	if _, _, err = svc.DownloadPath("groot.db"); !errors.Is(err, ErrNotFound) {
		t.Errorf("下载隐藏文件应 ErrNotFound, got %v", err)
	}
	if _, _, err = svc.DownloadPath("skills"); !errors.Is(err, ErrNotFound) {
		t.Errorf("下载目录应 ErrNotFound, got %v", err)
	}
}

// TestService_Write 验证保存：正常写入、只读拒绝、不存在拒绝、超限拒绝。
func TestService_Write(t *testing.T) {
	svc, home := newServiceForTest(t)

	if err := svc.Write("GROOT.md", "updated\n"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(home, "GROOT.md"))
	if string(data) != "updated\n" {
		t.Errorf("内容未写入: %q", data)
	}

	if err := svc.Write("config.yaml", "hack"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("写只读文件应 ErrReadOnly, got %v", err)
	}
	if err := svc.Write("env.yaml", "hack"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("写只读文件应 ErrReadOnly, got %v", err)
	}
	if err := svc.Write("not-exist.md", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("写不存在文件应 ErrNotFound, got %v", err)
	}
	if err := svc.Write("GROOT.md", strings.Repeat("x", MaxTextSize+1)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("超限保存应 ErrTooLarge, got %v", err)
	}
	if err := svc.Write("skills", "x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("写目录应 ErrInvalid, got %v", err)
	}
}

// TestService_Rename 验证同目录改名、只读保护、结构性条目保护、重名保护。
func TestService_Rename(t *testing.T) {
	svc, home := newServiceForTest(t)

	// 普通文件（非一级结构性条目）可正常改名
	if err := os.WriteFile(filepath.Join(home, "note.md"), []byte("n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.Rename("note.md", "note2.md"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "note2.md")); err != nil {
		t.Errorf("改名未生效: %v", err)
	}
	// 一级目录与 GROOT.md 禁止改名
	if err := svc.Rename("mcp", "mcp2"); !errors.Is(err, ErrForbidden) {
		t.Errorf("改名一级目录应 ErrForbidden, got %v", err)
	}
	if err := svc.Rename("GROOT.md", "G2.md"); !errors.Is(err, ErrForbidden) {
		t.Errorf("改名 GROOT.md 应 ErrForbidden, got %v", err)
	}
	// 二级目录可以改名
	if err := svc.Rename("skills/my-skill", "skills/my-skill2"); err != nil {
		t.Errorf("改名二级目录应成功, got %v", err)
	}
	if err := svc.Rename("config.yaml", "c2.yaml"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("改名只读文件应 ErrReadOnly, got %v", err)
	}
	if err := svc.Rename("note2.md", "env.yaml"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("改名为只读文件名应 ErrReadOnly, got %v", err)
	}
	if err := svc.Rename("note2.md", "skills/note2.md"); !errors.Is(err, ErrInvalid) {
		t.Errorf("跨目录改名应 ErrInvalid, got %v", err)
	}
	if err := svc.Rename("no.md", "yes.md"); !errors.Is(err, ErrNotFound) {
		t.Errorf("改名不存在文件应 ErrNotFound, got %v", err)
	}
	if err := svc.Rename("", "x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("改名 home 根应 ErrInvalid, got %v", err)
	}
}

// TestService_StructuralDirsInSubagent 验证 subagent 内的 mcp/skills 目录同样受
// 结构性保护：加载器按固定名称查找它们，改名或删除会让其中资源静默失效。
// 同层的其他目录（如 subagents/{name}/notes）与 skill 目录本身不受此限制。
func TestService_StructuralDirsInSubagent(t *testing.T) {
	svc, home := newServiceForTest(t)

	saDir := filepath.Join(home, "subagents", "weather")
	for _, d := range []string{"mcp", "skills", "notes"} {
		if err := os.MkdirAll(filepath.Join(saDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// 改名：mcp 与 skills 拒绝，同层普通目录放行
	if err := svc.Rename("subagents/weather/mcp", "subagents/weather/mcp2"); !errors.Is(err, ErrForbidden) {
		t.Errorf("改名 subagent mcp 应 ErrForbidden, got %v", err)
	}
	if err := svc.Rename("subagents/weather/skills", "subagents/weather/skills2"); !errors.Is(err, ErrForbidden) {
		t.Errorf("改名 subagent skills 应 ErrForbidden, got %v", err)
	}
	if err := svc.Rename("subagents/weather/notes", "subagents/weather/notes2"); err != nil {
		t.Errorf("改名 subagent 内普通目录应成功, got %v", err)
	}

	// 删除：空的 mcp/skills 也拒绝（与一级目录同理）
	if err := svc.Delete("subagents/weather/mcp"); !errors.Is(err, ErrForbidden) {
		t.Errorf("删除 subagent mcp 应 ErrForbidden, got %v", err)
	}
	if err := svc.Delete("subagents/weather/skills"); !errors.Is(err, ErrForbidden) {
		t.Errorf("删除 subagent skills 应 ErrForbidden, got %v", err)
	}

	// subagent 目录本身与其下的 skill 目录不是结构性目录，可改名
	if err := os.MkdirAll(filepath.Join(saDir, "skills", "get-weather"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := svc.Rename("subagents/weather/skills/get-weather", "subagents/weather/skills/get-forecast"); err != nil {
		t.Errorf("改名 skill 目录应成功, got %v", err)
	}
	if err := svc.Rename("subagents/weather", "subagents/weather2"); err != nil {
		t.Errorf("改名 subagent 目录应成功, got %v", err)
	}
}

// TestService_Delete 验证删除文件、空子目录、一级目录拒绝、非空目录拒绝、只读拒绝。
func TestService_Delete(t *testing.T) {
	svc, home := newServiceForTest(t)

	// 普通一级文件可删除
	if err := os.WriteFile(filepath.Join(home, "note.md"), []byte("n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete("note.md"); err != nil {
		t.Fatalf("Delete file: %v", err)
	}
	// GROOT.md 禁止删除（仍可编辑内容）
	if err := svc.Delete("GROOT.md"); !errors.Is(err, ErrForbidden) {
		t.Errorf("删 GROOT.md 应 ErrForbidden, got %v", err)
	}
	// 一级目录即使为空也不允许删除
	if err := svc.Delete("mcp"); !errors.Is(err, ErrForbidden) {
		t.Errorf("删一级目录应 ErrForbidden, got %v", err)
	}
	// 空的二级目录可以删除
	if err := os.Mkdir(filepath.Join(home, "skills/my-skill/tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete("skills/my-skill/tmp"); err != nil {
		t.Fatalf("Delete empty subdir: %v", err)
	}
	if err := svc.Delete("skills"); !errors.Is(err, ErrForbidden) {
		t.Errorf("删一级目录（非空）也应 ErrForbidden, got %v", err)
	}
	// 非空的二级目录拒绝
	if err := svc.Delete("skills/my-skill"); !errors.Is(err, ErrNotEmpty) {
		t.Errorf("删非空目录应 ErrNotEmpty, got %v", err)
	}
	if err := svc.Delete("config.yaml"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("删只读文件应 ErrReadOnly, got %v", err)
	}
	if err := svc.Delete(""); !errors.Is(err, ErrInvalid) {
		t.Errorf("删 home 根应 ErrInvalid, got %v", err)
	}
	if err := svc.Delete("groot.db"); !errors.Is(err, ErrNotFound) {
		t.Errorf("删隐藏文件应 ErrNotFound, got %v", err)
	}
}

// TestService_UploadTarget 验证任意目录可上传及各项限制。
func TestService_UploadTarget(t *testing.T) {
	svc, _ := newServiceForTest(t)

	// home 根不在列表里：根目录不接受上传（见 TestService_UploadRootForbidden）。
	for _, dir := range []string{"mcp", "skills", "skills/my-skill", "logs"} {
		if _, err := svc.UploadTarget(dir, "server.json", 100); err != nil {
			t.Errorf("目录 %q 上传应允许, got %v", dir, err)
		}
	}
	if _, err := svc.UploadTarget("nope", "x.txt", 100); !errors.Is(err, ErrNotFound) {
		t.Errorf("目录不存在应 ErrNotFound, got %v", err)
	}
	if _, err := svc.UploadTarget("mcp", "big.bin", MaxUploadSize+1); !errors.Is(err, ErrTooLarge) {
		t.Errorf("超限上传应 ErrTooLarge, got %v", err)
	}
	if _, err := svc.UploadTarget("mcp", "../evil.sh", 100); !errors.Is(err, ErrInvalid) {
		t.Errorf("带路径的文件名应 ErrInvalid, got %v", err)
	}
	if _, err := svc.UploadTarget("skills/my-skill", "SKILL.md", 100); !errors.Is(err, ErrExists) {
		t.Errorf("上传重名应 ErrExists, got %v", err)
	}
	if _, err := svc.UploadTarget("skills/nope", "a.md", 100); !errors.Is(err, ErrNotFound) {
		t.Errorf("目标目录不存在应 ErrNotFound, got %v", err)
	}
	if _, err := svc.UploadTarget("mcp", "file.", 100); !errors.Is(err, ErrInvalid) {
		t.Errorf("尾点文件名应 ErrInvalid, got %v", err)
	}
}

// TestService_SymlinkAlias 验证 home 内符号链接别名不能绕过只读/隐藏规则，
// 同时合法内部链接仍可正常读取与删除（删链接不删目标）。
func TestService_SymlinkAlias(t *testing.T) {
	svc, home := newServiceForTest(t)

	// 只读文件的别名不可写
	if err := os.Symlink(filepath.Join(home, "config.yaml"), filepath.Join(home, "cfglink.md")); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}
	if err := svc.Write("cfglink.md", "hack"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("写只读文件的别名应 ErrReadOnly, got %v", err)
	}
	fc, err := svc.Read("cfglink.md")
	if err != nil || !fc.Readonly {
		t.Errorf("读只读文件的别名应 readonly=true, got %+v, %v", fc, err)
	}

	// 隐藏文件的别名不可见
	if err := os.Symlink(filepath.Join(home, "groot.db"), filepath.Join(home, "dblink.md")); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}
	if _, err := svc.Read("dblink.md"); !errors.Is(err, ErrNotFound) {
		t.Errorf("读隐藏文件的别名应 ErrNotFound, got %v", err)
	}

	// 合法内部链接仍可读
	if err := os.Symlink(filepath.Join(home, "GROOT.md"), filepath.Join(home, "ok.md")); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}
	fc, err = svc.Read("ok.md")
	if err != nil || fc.Content != "# memo\n" {
		t.Errorf("读合法内部链接应返回目标内容, got %+v, %v", fc, err)
	}

	// 删除链接本身，目标保留
	if err := svc.Delete("ok.md"); err != nil {
		t.Fatalf("Delete symlink: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, "ok.md")); !os.IsNotExist(err) {
		t.Errorf("链接应已删除, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "GROOT.md")); err != nil {
		t.Errorf("链接目标不应被删除: %v", err)
	}
}

// TestService_Mkdir 验证单层建目录：成功、重名、父目录缺失、非法名与只读名。
func TestService_Mkdir(t *testing.T) {
	svc, home := newServiceForTest(t)

	if err := svc.Mkdir("mcp", "bundle"); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	info, err := os.Stat(filepath.Join(home, "mcp", "bundle"))
	if err != nil || !info.IsDir() {
		t.Fatalf("mcp/bundle 应是目录, got %+v, %v", info, err)
	}

	if err := svc.Mkdir("mcp", "bundle"); !errors.Is(err, ErrExists) {
		t.Errorf("重复建目录应 ErrExists, got %v", err)
	}
	if err := svc.Mkdir("nope", "bundle"); !errors.Is(err, ErrNotFound) {
		t.Errorf("父目录不存在应 ErrNotFound, got %v", err)
	}
	for _, name := range []string{"../evil", ".hidden", "", strings.Repeat("a", 65)} {
		if err := svc.Mkdir("mcp", name); !errors.Is(err, ErrInvalid) {
			t.Errorf("非法名 %q 应 ErrInvalid, got %v", name, err)
		}
	}
}

// TestService_UploadRootForbidden 验证工作空间根目录不接受任何上传：
// 根下的一级目录是结构性目录，面板删不掉也改不了名。
func TestService_UploadRootForbidden(t *testing.T) {
	svc, home := newServiceForTest(t)

	if err := svc.Mkdir("", "anything"); !errors.Is(err, ErrForbidden) {
		t.Errorf("根目录建目录应 ErrForbidden, got %v", err)
	}
	if _, err := svc.UploadTarget("", "x.md", 1); !errors.Is(err, ErrForbidden) {
		t.Errorf("根目录上传文件应 ErrForbidden, got %v", err)
	}
	for _, name := range []string{"anything", "x.md"} {
		if _, err := os.Lstat(filepath.Join(home, name)); !os.IsNotExist(err) {
			t.Errorf("根目录不应残留 %q, got %v", name, err)
		}
	}
}

// TestService_UploadTarget_RelPath 验证上传路径可含多段：中间目录按需创建，
// 但基准目录不存在时不得被顺带造出。
func TestService_UploadTarget_RelPath(t *testing.T) {
	svc, _ := newServiceForTest(t)
	home := svc.Home() // 已解析符号链接，与 UploadTarget 返回的绝对路径同源

	got, err := svc.UploadTarget("mcp", "note.md", 1)
	if err != nil || got != filepath.Join(home, "mcp", "note.md") {
		t.Fatalf("单段上传 = %q, %v", got, err)
	}

	got, err = svc.UploadTarget("mcp", "A/sub/note.md", 1)
	if err != nil || got != filepath.Join(home, "mcp", "A", "sub", "note.md") {
		t.Fatalf("多段上传 = %q, %v", got, err)
	}
	if info, err := os.Stat(filepath.Join(home, "mcp", "A", "sub")); err != nil || !info.IsDir() {
		t.Errorf("中间目录 mcp/A/sub 应已创建, got %+v, %v", info, err)
	}

	for _, rel := range []string{"A/../x.md", "A/.hidden/x.md", "../x.md", ""} {
		if _, err := svc.UploadTarget("mcp", rel, 1); !errors.Is(err, ErrInvalid) {
			t.Errorf("非法 relpath %q 应 ErrInvalid, got %v", rel, err)
		}
	}

	if _, err := svc.UploadTarget("nope", "A/x.md", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("基准目录不存在应 ErrNotFound, got %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, "nope")); !os.IsNotExist(err) {
		t.Errorf("不存在的基准目录不应被创建, got %v", err)
	}

	if _, err := svc.UploadTarget("skills/my-skill", "SKILL.md", 1); !errors.Is(err, ErrExists) {
		t.Errorf("目标已存在应 ErrExists, got %v", err)
	}
	if _, err := svc.UploadTarget("mcp", "A/big.bin", MaxUploadSize+1); !errors.Is(err, ErrTooLarge) {
		t.Errorf("超限应 ErrTooLarge, got %v", err)
	}

	// 中间段命中已存在的普通文件：Resolve 的 fail-closed 逻辑先拦下（EvalSymlinks
	// 报 ENOTDIR 而非 NotExist），返回 ErrNotFound —— 语义上"该目录不存在"，
	// 且不会走到 MkdirAll。
	if err := os.WriteFile(filepath.Join(home, "mcp", "afile.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := svc.UploadTarget("mcp", "afile.md/x.md", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("中间段是文件应 ErrNotFound, got %v", err)
	}
}
