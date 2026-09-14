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
	// 一级目录与 GROOT.md 是运行时结构性条目，禁止改名（与 Delete 规则一致）
	if !strings.Contains(fromN, "/") && (info.IsDir() || strings.EqualFold(fromN, "GROOT.md")) {
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
		// home 下的一级目录（skills/mcp/logs/subagents 等）是运行时的
		// 结构性目录，即使为空也不允许通过 Web 面板删除。
		if !strings.Contains(n, "/") {
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

// UploadTarget 校验上传请求（白名单、大小、文件名、重名），
// 返回可直接写入的目标绝对路径；实际落盘由 handler 完成。
func (s *Service) UploadTarget(dirRel, filename string, size int64) (string, error) {
	if size > MaxUploadSize {
		return "", ErrTooLarge
	}
	if !validName(filename) {
		return "", ErrInvalid // 含路径分隔符或非法字符的文件名直接拒绝
	}
	_, dirN, err := s.res.Resolve(dirRel)
	if err != nil {
		return "", err
	}
	if !s.res.CanUpload(dirN) {
		return "", ErrForbidden
	}
	abs, n, err := s.res.Resolve(joinRel(dirN, filename))
	if err != nil {
		return "", err
	}
	if s.res.ReadOnly(n) {
		return "", ErrReadOnly
	}
	if info, err := os.Stat(filepath.Dir(abs)); err != nil || !info.IsDir() {
		return "", ErrNotFound
	}
	if _, err := os.Lstat(abs); err == nil {
		return "", ErrExists
	}
	return abs, nil
}
