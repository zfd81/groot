// Package webfiles 实现 Web 文件面板的文件系统访问层：
// 路径安全解析（防穿越/防符号链接逃逸）、可见性与权限规则、文件操作。
package webfiles

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// 对外统一的错误，handler 依据它们映射 HTTP 状态码。
var (
	ErrNotFound  = errors.New("not found")           // 不存在 / 越界 / 隐藏
	ErrReadOnly  = errors.New("read only")           // 只读文件的写操作
	ErrForbidden = errors.New("forbidden")           // 白名单之外的新建/上传
	ErrExists    = errors.New("already exists")      // 目标已存在
	ErrNotEmpty  = errors.New("directory not empty") // 删除非空目录
	ErrTooLarge  = errors.New("too large")           // 超出大小限制
	ErrInvalid   = errors.New("invalid request")     // 参数非法
)

const (
	// MaxTextSize 在线读取/保存文本的大小上限。
	MaxTextSize = 2 * 1024 * 1024
	// MaxUploadSize 单文件上传大小上限。
	MaxUploadSize = 20 * 1024 * 1024
)

// Resolver 把请求中的相对路径安全解析为 home 内的绝对路径，
// 并承载隐藏、只读、写白名单三类规则。
type Resolver struct {
	home string // 已经过 EvalSymlinks 的绝对路径
}

// NewResolver 构造 Resolver；homeDir 必须存在。
func NewResolver(homeDir string) (*Resolver, error) {
	abs, err := filepath.Abs(homeDir)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(real)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("webfiles: home is not a directory: " + real)
	}
	return &Resolver{home: real}, nil
}

// Home 返回 home 目录的绝对路径（供前端路径栏展示）。
func (r *Resolver) Home() string { return r.home }

// Normalize 清洗请求路径：以 "/" 锚定后 Clean，任何 ".." 都无法越过根。
// 返回统一 "/" 分隔、无前导斜杠的相对路径（"" 表示 home 根）。
func Normalize(rel string) (string, error) {
	if strings.ContainsRune(rel, 0) {
		return "", ErrNotFound
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	cleaned := filepath.ToSlash(filepath.Clean("/" + rel))
	n := strings.TrimPrefix(cleaned, "/")
	// 防 NTFS ADS（"name:stream"）与 Windows 尾点/尾空格剥离
	// （"name." / "name " 会被文件系统折叠成 "name"），全平台统一拒绝。
	if n != "" {
		for _, seg := range strings.Split(n, "/") {
			if strings.Contains(seg, ":") || strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") {
				return "", ErrNotFound
			}
		}
	}
	return n, nil
}

// Resolve 返回 rel 的绝对路径与归一化相对路径。
// 目标无需已存在（供创建类操作使用），但已存在的祖先若经符号链接
// 逃出 home，或路径命中隐藏规则，返回 ErrNotFound。
func (r *Resolver) Resolve(rel string) (abs string, normalized string, err error) {
	n, err := Normalize(rel)
	if err != nil {
		return "", "", err
	}
	if r.Hidden(n) {
		return "", "", ErrNotFound
	}
	abs = filepath.Join(r.home, filepath.FromSlash(n))
	if err := r.verifyReal(abs); err != nil {
		return "", "", err
	}
	// 隐藏文件的任何符号链接别名一律不可见。
	if r.Hidden(r.RealRel(abs, n)) {
		return "", "", ErrNotFound
	}
	return abs, n, nil
}

// RealRel 返回 abs 解析符号链接后的 home 相对路径（"/"分隔，根为 ""）；
// abs 不存在或解析失败时返回请求路径 n 本身。
// 用于对"别名"复查 Hidden/ReadOnly 规则。
func (r *Resolver) RealRel(abs, n string) string {
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return n
	}
	rel, err := filepath.Rel(r.home, real)
	if err != nil || rel == "." {
		if rel == "." {
			return ""
		}
		return n
	}
	return filepath.ToSlash(rel)
}

// verifyReal 自 abs 起向上找到第一个已存在的路径，解析符号链接后
// 校验其真实位置仍在 home 内。
func (r *Resolver) verifyReal(abs string) error {
	p := abs
	for {
		real, err := filepath.EvalSymlinks(p)
		if err == nil {
			if real != r.home && !strings.HasPrefix(real, r.home+string(filepath.Separator)) {
				return ErrNotFound
			}
			return nil
		}
		if !os.IsNotExist(err) {
			// 权限错误、ENOTDIR 等一律按不存在处理（fail-closed），不向外泄露细节。
			return ErrNotFound
		}
		parent := filepath.Dir(p)
		if parent == p {
			return ErrNotFound
		}
		p = parent
	}
}

// Hidden 判定归一化路径 n 是否对外不可见（n 必须是 Normalize/Resolve 的返回值）：
// home 根下基名以 groot.db 开头的条目，大小写不敏感，
// 前缀匹配（fail-safe，覆盖 -wal/-shm 及任何备份副本）。
func (r *Resolver) Hidden(n string) bool {
	return n != "" && !strings.Contains(n, "/") && strings.HasPrefix(strings.ToLower(n), "groot.db")
}

// ReadOnly 判定 n 是否只读（n 必须是 Normalize/Resolve 的返回值）：
// home 根下的 env.yaml 与 config.yaml，大小写不敏感。
func (r *Resolver) ReadOnly(n string) bool {
	return strings.EqualFold(n, "env.yaml") || strings.EqualFold(n, "config.yaml")
}

// CanUpload 判定能否向目录 n 上传（n 必须是 Normalize/Resolve 的返回值）：
// home 内除根以外的任意目录均允许，上传目标必须是某个已存在的子目录。
// home 根不接受上传：根下的一级目录被 isStructuralDir 视为结构性目录，
// 面板既不能删除也不能改名，允许在根上传会造出用户自己清理不掉的条目。
// 对含 "." / ".." 段的未归一化输入防御性返回 false。
// 目标文件本身仍受只读与隐藏规则约束（见 UploadTarget）。
func (r *Resolver) CanUpload(n string) bool {
	if n == "" {
		return false
	}
	for _, p := range strings.Split(n, "/") {
		if p == "." || p == ".." {
			return false
		}
	}
	return true
}
