package webfiles

import (
	"bytes"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Entry 目录列表条目。
type Entry struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // "dir" | "file"
	Size     int64  `json:"size"`
	Mtime    int64  `json:"mtime"` // 毫秒时间戳
	Readonly bool   `json:"readonly"`
}

// FileContent 文本读取结果。二进制文件 Binary=true 且 Content 为空。
type FileContent struct {
	Path     string `json:"path"`
	Readonly bool   `json:"readonly"`
	Binary   bool   `json:"binary"`
	Content  string `json:"content"`
}

// Service 文件面板的文件操作层，所有路径都经 Resolver 安全解析。
type Service struct {
	res *Resolver
}

// NewService 构造 Service；homeDir 必须存在。
func NewService(homeDir string) (*Service, error) {
	res, err := NewResolver(homeDir)
	if err != nil {
		return nil, err
	}
	return &Service{res: res}, nil
}

// Home 返回 home 目录绝对路径（供前端路径栏展示）。
func (s *Service) Home() string { return s.res.Home() }

// joinRel 拼接归一化相对路径。
func joinRel(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// List 返回目录单层内容：过滤隐藏项与 .DS_Store，目录在前、按名排序。
// 目标不存在返回 ErrNotFound，非目录返回 ErrInvalid。
func (s *Service) List(rel string) ([]Entry, error) {
	abs, n, err := s.res.Resolve(rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, ErrNotFound
	}
	if !info.IsDir() {
		return nil, ErrInvalid
	}
	des, err := os.ReadDir(abs)
	if err != nil {
		return nil, ErrNotFound // fail-closed，不向外泄露细节
	}
	entries := make([]Entry, 0, len(des))
	for _, de := range des {
		child := joinRel(n, de.Name())
		if s.res.Hidden(child) || de.Name() == ".DS_Store" {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue // 读取途中被删的条目直接跳过
		}
		typ := "file"
		if de.IsDir() {
			typ = "dir"
		}
		entries = append(entries, Entry{
			Name:     de.Name(),
			Type:     typ,
			Size:     info.Size(),
			Mtime:    info.ModTime().UnixMilli(),
			Readonly: s.res.ReadOnly(child),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Type != entries[j].Type {
			return entries[i].Type == "dir"
		}
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

// Read 读取文本文件内容；目录返回 ErrInvalid，超限 ErrTooLarge，
// 二进制文件返回 Binary=true 且不含内容。
func (s *Service) Read(rel string) (*FileContent, error) {
	abs, n, err := s.res.Resolve(rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, ErrNotFound
	}
	if info.IsDir() {
		return nil, ErrInvalid
	}
	if info.Size() > MaxTextSize {
		return nil, ErrTooLarge
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	fc := &FileContent{Path: n, Readonly: s.res.ReadOnly(n) || s.res.ReadOnly(s.res.RealRel(abs, n))}
	if isBinary(data) {
		fc.Binary = true
		return fc, nil
	}
	fc.Content = string(data)
	return fc, nil
}

// isBinary 以前 8KB 是否含 NUL 字节判定二进制。
func isBinary(data []byte) bool {
	probe := data
	if len(probe) > 8192 {
		probe = probe[:8192]
	}
	return bytes.IndexByte(probe, 0) >= 0
}

// DownloadPath 解析下载目标，返回绝对路径与文件名；目录不可下载。
func (s *Service) DownloadPath(rel string) (abs string, name string, err error) {
	abs, n, err := s.res.Resolve(rel)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return "", "", ErrNotFound
	}
	return abs, path.Base(n), nil
}

// nameRe 合法名称：字母/数字开头，允许字母数字、空格、括号、点、下划线、连字符，≤64 字符。
var nameRe = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} ()._-]{0,63}$`)

// validName 校验单段名称：不允许路径分隔、点开头、尾点/尾空格、"."、".."。
func validName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, ".") {
		return false
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return false
	}
	return nameRe.MatchString(name)
}

// Write 保存已存在的文本文件；只读文件拒绝，超限拒绝。
func (s *Service) Write(rel, content string) error {
	if len(content) > MaxTextSize {
		return ErrTooLarge
	}
	abs, n, err := s.res.Resolve(rel)
	if err != nil {
		return err
	}
	if s.res.ReadOnly(n) || s.res.ReadOnly(s.res.RealRel(abs, n)) {
		return ErrReadOnly
	}
	info, err := os.Stat(abs)
	if err != nil {
		return ErrNotFound // 只允许保存已存在的文件（新建走 Create/Scaffold）
	}
	if info.IsDir() {
		return ErrInvalid
	}
	return os.WriteFile(abs, []byte(content), 0o644)
}

// Rename 同目录改名；只读文件（或改名为只读文件名）拒绝。
func (s *Service) Rename(fromRel, toRel string) error {
	fromAbs, fromN, err := s.res.Resolve(fromRel)
	if err != nil {
		return err
	}
	if fromN == "" {
		return ErrInvalid // home 根不可改名
	}
	if s.res.ReadOnly(fromN) {
		return ErrReadOnly
	}
	info, err := os.Lstat(fromAbs)
	if err != nil {
		return ErrNotFound
	}
	// 结构性目录与 GROOT.md 是运行时按固定名称查找的条目，禁止改名（与 Delete 规则一致）
	if info.IsDir() && isStructuralDir(fromN) {
		return ErrForbidden
	}
	if !strings.Contains(fromN, "/") && strings.EqualFold(fromN, "GROOT.md") {
		return ErrForbidden
	}
	toAbs, toN, err := s.res.Resolve(toRel)
	if err != nil {
		return err
	}
	if s.res.ReadOnly(toN) {
		return ErrReadOnly
	}
	if path.Dir(fromN) != path.Dir(toN) {
		return ErrInvalid // 仅支持同目录改名
	}
	if !validName(path.Base(toN)) {
		return ErrInvalid
	}
	if _, err := os.Lstat(toAbs); err == nil {
		return ErrExists
	}
	return os.Rename(fromAbs, toAbs)
}

// isStructuralDir 判断相对路径是否是运行时依赖名称的结构性目录：
//   - home 下的一级目录（skills、mcp、subagents、logs 等）
//   - subagent 内的 mcp 与 skills 目录（subagents/{name}/mcp、subagents/{name}/skills）
//
// 这些目录由加载器按固定名称查找，改名或删除会让其中的资源静默失效。
// 调用方需自行确认路径确实是目录。
func isStructuralDir(n string) bool {
	segs := strings.Split(n, "/")
	switch len(segs) {
	case 1:
		return true
	case 3:
		return segs[0] == "subagents" && (segs[2] == "mcp" || segs[2] == "skills")
	default:
		return false
	}
}

// Delete 删除文件或空目录；home 根、只读、隐藏、非空目录均拒绝。
func (s *Service) Delete(rel string) error {
	abs, n, err := s.res.Resolve(rel)
	if err != nil {
		return err
	}
	if n == "" {
		return ErrInvalid
	}
	if s.res.ReadOnly(n) {
		return ErrReadOnly
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return ErrNotFound
	}
	// GROOT.md（全局记忆）禁止删除，但内容仍可编辑
	if !strings.Contains(n, "/") && strings.EqualFold(n, "GROOT.md") {
		return ErrForbidden
	}
	if info.IsDir() {
		// 结构性目录即使为空也不允许通过 Web 面板删除。
		if isStructuralDir(n) {
			return ErrForbidden
		}
		des, err := os.ReadDir(abs)
		if err != nil {
			return ErrNotFound
		}
		if len(des) > 0 {
			return ErrNotEmpty
		}
		if err := os.Remove(abs); err != nil {
			return ErrNotEmpty // 检查与删除之间新增了内容等情况，fail-safe 映射
		}
		return nil
	}
	return os.Remove(abs)
}

// Mkdir 在 dirRel 下创建单层目录 name。它是目录上传流程的占位原语：
// 先建出目标根目录以探测冲突，再逐个上传文件。已存在时返回 ErrExists。
//
// 不要用它实现通用的"新建目录"功能：Web 面板按设计不提供该入口，
// 本方法只服务于目录上传时的冲突探测。
func (s *Service) Mkdir(dirRel, name string) error {
	if !validName(name) {
		return ErrInvalid
	}
	_, dirN, err := s.res.Resolve(dirRel)
	if err != nil {
		return err
	}
	if !s.res.CanUpload(dirN) {
		return ErrForbidden
	}
	abs, n, err := s.res.Resolve(joinRel(dirN, name))
	if err != nil {
		return err
	}
	// 只读判定先于重名判定：只读文件名不允许被目录占位。
	// 只读规则只命中 home 根下的 env.yaml/config.yaml，而根已被上面的
	// CanUpload 挡掉，因此这里目前不可达，仅作防御——只读名单若日后扩展到
	// 子目录，顺序仍然正确。
	if s.res.ReadOnly(n) {
		return ErrReadOnly
	}
	if info, err := os.Stat(filepath.Dir(abs)); err != nil || !info.IsDir() {
		return ErrNotFound
	}
	if _, err := os.Lstat(abs); err == nil {
		return ErrExists
	}
	if err := os.Mkdir(abs, 0o755); err != nil {
		if os.IsExist(err) {
			return ErrExists
		}
		return ErrInvalid
	}
	return nil
}

// UploadTarget 校验上传请求（白名单、大小、路径合法性、重名），
// 返回可直接写入的目标绝对路径；实际落盘由 handler 完成。
// relpath 是相对基准目录 dirRel 的路径，可含 "/" 以支持目录上传，
// 每一段都须是合法名称；不存在的中间目录会按需创建。
//
// 创建中间目录是不可回滚的副作用：调用方拿到路径后落盘失败（handler 返回 500），
// 或者只调用本函数取路径而根本没写文件，已建出的中间目录都会留在磁盘上，
// 没有任何清理逻辑。这是有意为之——同一目录树并发上传时清理反而危险。
func (s *Service) UploadTarget(dirRel, relpath string, size int64) (string, error) {
	if size > MaxUploadSize {
		return "", ErrTooLarge
	}
	if relpath == "" {
		return "", ErrInvalid
	}
	for _, seg := range strings.Split(relpath, "/") {
		if !validName(seg) {
			return "", ErrInvalid // 任一段含非法字符或为 "." / ".." 即拒绝
		}
	}
	dirAbs, dirN, err := s.res.Resolve(dirRel)
	if err != nil {
		return "", err
	}
	if !s.res.CanUpload(dirN) {
		return "", ErrForbidden
	}
	// 基准目录的存在性必须在 MkdirAll 之前单独确认：多段 relpath 下
	// filepath.Dir(abs) 指向的是中间目录，若顺序颠倒，MkdirAll 会把不存在的
	// 基准目录一并造出来，404 就丢了。
	if info, err := os.Stat(dirAbs); err != nil || !info.IsDir() {
		return "", ErrNotFound
	}
	abs, n, err := s.res.Resolve(joinRel(dirN, relpath))
	if err != nil {
		return "", err
	}
	if s.res.ReadOnly(n) {
		return "", ErrReadOnly
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		// 权限不足、磁盘写满、路径过长等系统层原因；中间段与已存在文件同名
		// 的情况走不到这里，Resolve 的 verifyReal 会先按 ErrNotFound 拦下。
		return "", ErrInvalid
	}
	if _, err := os.Lstat(abs); err == nil {
		return "", ErrExists
	}
	return abs, nil
}
