package pysvc

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// SDKVersion 是嵌入的 Python SDK 版本，就绪行中的 sdk 字段必须与之一致。
const SDKVersion = "1.0.0"

// assets 是随二进制发布的 Python SDK 与 services init 模板。
// 两棵目录树都含以下划线开头的路径（sdk 的 __init__.py、template 的 _template），
// 必须用 all: 前缀才会被嵌入。
//
//go:embed all:sdk all:template
var assets embed.FS

// layoutDirs 是 groot services init 需要保证存在的目录，按父目录在前的顺序排列。
var layoutDirs = []string{
	"services",
	"services/.sdk",
	"services/_template",
	"services/_template/app",
}

// layoutFiles 是 groot services init 需要写出的模板文件：嵌入中的路径与相对 {GROOT_HOME} 的目标路径。
var layoutFiles = []struct {
	src string
	dst string
}{
	{"template/service.yaml", "services/service.yaml"},
	{"template/requirements.txt", "services/requirements.txt"},
	{"template/_template/requirements.txt", "services/_template/requirements.txt"},
	{"template/_template/app/__init__.py", "services/_template/app/__init__.py"},
	{"template/_template/app/main.py", "services/_template/app/main.py"},
}

// ServicesDir 返回 {GROOT_HOME} 下 Python 服务的根目录。
func ServicesDir(homeDir string) string {
	return filepath.Join(homeDir, "services")
}

// EnsureLayout 创建 {GROOT_HOME}/services 的固定布局，返回本次新建的路径。
// 已存在的目录和文件一律跳过，不覆盖使用者内容。
func EnsureLayout(homeDir string) ([]string, error) {
	created := []string{}

	for _, rel := range layoutDirs {
		dir := filepath.Join(homeDir, filepath.FromSlash(rel))
		exists, err := pathExists(dir)
		if err != nil {
			return created, err
		}
		if exists {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return created, fmt.Errorf("创建目录 %s 失败: %w", dir, err)
		}
		created = append(created, dir)
	}

	for _, f := range layoutFiles {
		dst := filepath.Join(homeDir, filepath.FromSlash(f.dst))
		exists, err := pathExists(dst)
		if err != nil {
			return created, err
		}
		if exists {
			continue
		}
		data, err := assets.ReadFile(f.src)
		if err != nil {
			return created, fmt.Errorf("读取嵌入模板 %s 失败: %w", f.src, err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return created, fmt.Errorf("写入 %s 失败: %w", dst, err)
		}
		created = append(created, dst)
	}

	return created, nil
}

// ExtractSDK 把嵌入的 SDK 解压到 {GROOT_HOME}/services/.sdk/<版本>/，返回 SDK 根目录
// （供 PYTHONPATH 使用）。先写入带随机后缀的临时目录再原子改名；目标已存在时直接复用。
func ExtractSDK(homeDir string) (string, error) {
	sdkParent := filepath.Join(ServicesDir(homeDir), ".sdk")
	root := filepath.Join(sdkParent, SDKVersion)

	exists, err := pathExists(root)
	if err != nil {
		return "", err
	}
	if exists {
		return root, nil
	}

	if err := os.MkdirAll(sdkParent, 0o755); err != nil {
		return "", fmt.Errorf("创建目录 %s 失败: %w", sdkParent, err)
	}

	tmp, err := os.MkdirTemp(sdkParent, SDKVersion+".tmp-")
	if err != nil {
		return "", fmt.Errorf("创建 SDK 临时目录失败: %w", err)
	}
	defer os.RemoveAll(tmp)
	// MkdirTemp 创建的目录是 0700；同一 GROOT_HOME 被多个系统用户共享时其他用户也要能 import。
	if err := os.Chmod(tmp, 0o755); err != nil {
		return "", fmt.Errorf("设置 SDK 目录权限失败: %w", err)
	}

	if err := copyEmbedDir("sdk", tmp); err != nil {
		return "", err
	}

	if err := os.Rename(tmp, root); err != nil {
		// 并发场景下另一个调用可能已完成解压，直接复用其结果。
		if exists, statErr := pathExists(root); statErr == nil && exists {
			return root, nil
		}
		return "", fmt.Errorf("安装 SDK 到 %s 失败: %w", root, err)
	}

	return root, nil
}

// copyEmbedDir 把嵌入文件系统中 src 子树的内容复制到 dst 目录。
// Python 字节码缓存（__pycache__ 目录、.pyc/.pyo 文件）不复制：它们与具体
// Python 版本绑定，混入使用者目录既无意义，也可能与新解释器不兼容。
func copyEmbedDir(src, dst string) error {
	return fs.WalkDir(assets, src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("遍历嵌入目录 %s 失败: %w", src, err)
		}
		if d.IsDir() && d.Name() == "__pycache__" {
			return fs.SkipDir
		}
		if !d.IsDir() && isBytecodeFile(d.Name()) {
			return nil
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, src), "/")
		target := filepath.Join(dst, filepath.FromSlash(rel))
		if d.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("创建目录 %s 失败: %w", target, err)
			}
			return nil
		}
		data, err := assets.ReadFile(p)
		if err != nil {
			return fmt.Errorf("读取嵌入文件 %s 失败: %w", p, err)
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return fmt.Errorf("写入 %s 失败: %w", target, err)
		}
		return nil
	})
}

// isBytecodeFile 报告文件名是否为 Python 字节码文件。
func isBytecodeFile(name string) bool {
	return strings.HasSuffix(name, ".pyc") || strings.HasSuffix(name, ".pyo")
}

// pathExists 报告路径是否存在；只把“不存在”视为不存在，其余错误原样返回。
func pathExists(path string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("检查路径 %s 失败: %w", path, err)
	}
	return true, nil
}
