package pysvc

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// serviceNamePattern 是子服务目录名必须匹配的规则，原样展示在错误原因中。
const serviceNamePattern = `^[a-z0-9][a-z0-9_-]*$`

var serviceNameRe = regexp.MustCompile(serviceNamePattern)

// requiredFiles 是子服务目录中必须具备的相对路径，使用正斜杠书写以便跨平台展示。
// app/__init__.py 让启动器能把 app/ 注册为包并支持相对导入。
var requiredFiles = []string{"app/__init__.py", "app/main.py", "requirements.txt"}

// reservedServiceNames 是 SDK 保留的路径名，子服务不能使用，避免遮蔽 SDK 自己
// 提供的端点（如 /healthz）。
var reservedServiceNames = map[string]struct{}{
	"healthz": {},
}

// Discover 扫描 servicesDir 的直接子目录，为每个候选目录返回一项 Service，结果按
// Name 升序。以 . 或 _ 开头的目录不参与扫描；符号链接按目标是否为目录处理，断链
// 记为无效项。目录名不符合命名规则、使用保留名称、缺少必需文件、或与前面的目录
// 在 -/_ 换算后重名时，该 Service 的 Invalid 字段写入中文原因。
// Permission 取全局配置中的映射，缺失或值非法时回退为 all。
func Discover(servicesDir string, cfg GlobalConfig) []Service {
	entries, err := os.ReadDir(servicesDir)
	if err != nil {
		return nil
	}

	var services []Service
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		path := filepath.Join(servicesDir, name)
		// entry.IsDir 对符号链接返回 false；用 os.Stat 跟随链接，允许把子服务目录
		// 软链到别处（如开发中的仓库或共享目录）。
		info, err := os.Stat(path)
		if err != nil {
			// 断链或不可访问：保留为无效项并说明原因，而不是静默忽略。
			services = append(services, Service{
				Name:    name,
				Dir:     path,
				Invalid: fmt.Sprintf("无法访问子服务目录（可能是指向不存在目标的符号链接）: %v", err),
			})
			continue
		}
		if !info.IsDir() {
			continue
		}
		services = append(services, Service{Name: name, Dir: path})
	}

	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })

	// 进程内模块名把 - 换为 _，换算后相同的目录名视为重名，保留字典序靠前的一个。
	seen := make(map[string]string)
	for i := range services {
		s := &services[i]
		s.Permission = permissionFor(cfg, s.Name)

		// 断链等访问失败的原因已写入 Invalid，保留原原因。
		if s.Invalid != "" {
			continue
		}
		if !serviceNameRe.MatchString(s.Name) {
			s.Invalid = fmt.Sprintf("目录名 %q 不符合命名规则 %s", s.Name, serviceNamePattern)
			continue
		}
		if _, reserved := reservedServiceNames[s.Name]; reserved {
			s.Invalid = fmt.Sprintf("目录名 %q 是保留名称（SDK 健康端点 /healthz）", s.Name)
			continue
		}

		normalized := strings.ReplaceAll(s.Name, "-", "_")
		if kept, ok := seen[normalized]; ok {
			s.Invalid = fmt.Sprintf("目录名 %q 与 %q 重名（- 与 _ 换算后相同）", s.Name, kept)
			continue
		}
		seen[normalized] = s.Name

		if missing := missingFiles(s.Dir); len(missing) > 0 {
			s.Invalid = fmt.Sprintf("缺少 %s", strings.Join(missing, "、"))
		}
	}

	return services
}

// permissionFor 返回 name 对应的权限点：配置中存在且为已知值时用该值，否则用 all。
func permissionFor(cfg GlobalConfig, name string) string {
	if p, ok := cfg.Permission[name]; ok && ValidPermission(p) {
		return p
	}
	return "all"
}

// missingFiles 返回 dir 下缺失的必需文件（按 requiredFiles 顺序）。
func missingFiles(dir string) []string {
	var missing []string
	for _, rel := range requiredFiles {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			missing = append(missing, rel)
		}
	}
	return missing
}

// InterpreterPath 返回共享虚拟环境中的 Python 解释器路径。
func InterpreterPath(servicesDir string) string {
	return interpreterPath(servicesDir, runtime.GOOS)
}
