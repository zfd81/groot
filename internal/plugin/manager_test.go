package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"

	"github.com/zfd81/groot/internal/api/errbody"
	"go.uber.org/zap/zapcore"

	"github.com/zfd81/groot/internal/logger"
)

const (
	managerReadySingle = `GROOT_PLUGIN_READY {"port":53124,"pid":123,"sdk":"1.0.0","plugins":{"asr-router":"ok"}}`
	managerReadyMulti  = `GROOT_PLUGIN_READY {"port":53124,"pid":123,"sdk":"1.0.0","plugins":{"asr-router":"ok","ocr":"error: No module named 'torch'"}}`
)

// validPluginFiles 是一个通过目录校验的插件所需的文件。
var validPluginFiles = []string{"app/__init__.py", "app/main.py", "requirements.txt"}

// newTestHome 创建临时 home 并在 plugins 下按 files 创建插件目录，返回 home 与 plugins 目录。
func newTestHome(t *testing.T, files map[string][]string) (home, pluginsDir string) {
	t.Helper()
	home = t.TempDir()
	pluginsDir = PluginsDir(home)
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatalf("创建 plugins 目录失败: %v", err)
	}
	for name, rels := range files {
		mkFiles(t, pluginsDir, name, rels...)
	}
	return home, pluginsDir
}

// writePluginYAML 写入 plugins/plugin.yaml。
func writePluginYAML(t *testing.T, pluginsDir, content string) {
	t.Helper()
	path := filepath.Join(pluginsDir, "plugin.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入 plugin.yaml 失败: %v", err)
	}
}

// writeFakeInterpreter 在共享虚拟环境中放置一个假解释器文件。
func writeFakeInterpreter(t *testing.T, pluginsDir string) {
	t.Helper()
	path := InterpreterPath(pluginsDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建虚拟环境目录失败: %v", err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("写入假解释器失败: %v", err)
	}
}

// managerOptions 返回指向 home 的 Options。
func managerOptions(home string) Options {
	return Options{HomeDir: home, Log: logger.NewNop(), LogLevel: "info"}
}

// statusOf 在 Status.Plugins 中按名字查找插件状态。
func statusOf(t *testing.T, status Status, name string) PluginStatus {
	t.Helper()
	for _, p := range status.Plugins {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("状态中未找到插件 %q: %+v", name, status.Plugins)
	return PluginStatus{}
}

// assertNoSDKInstalled 断言 home 下没有解压 SDK。
func assertNoSDKInstalled(t *testing.T, home string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(PluginsDir(home), ".sdk")); !os.IsNotExist(err) {
		t.Errorf("不应安装 SDK, stat 错误 = %v", err)
	}
}

func TestManager_DiscoversBeforeStart(t *testing.T) {
	home, pluginsDir := newTestHome(t, map[string][]string{
		"asr-router": validPluginFiles,
	})
	writePluginYAML(t, pluginsDir, "permission:\n  asr-router: chat\n")

	m := NewManager(managerOptions(home))

	plugins := m.Plugins()
	if len(plugins) != 1 {
		t.Fatalf("Plugins() 长度 = %d, want 1: %+v", len(plugins), plugins)
	}
	if plugins[0].Name != "asr-router" || plugins[0].Invalid != "" {
		t.Errorf("Plugins()[0] = %+v, want 有效的 asr-router", plugins[0])
	}

	perm, ok := m.PluginPermission("asr-router")
	if !ok || perm != "chat" {
		t.Errorf("PluginPermission(asr-router) = (%q, %v), want (chat, true)", perm, ok)
	}
	if _, ok := m.PluginPermission("missing"); ok {
		t.Error("未知插件不应解析出权限")
	}
}

func TestManager_StatusPendingBeforeStart(t *testing.T) {
	t.Run("有有效插件", func(t *testing.T) {
		home, _ := newTestHome(t, map[string][]string{"asr-router": validPluginFiles})
		m := NewManager(managerOptions(home))

		status := m.Status()
		if status.Process.Status != "starting" {
			t.Errorf("Process.Status = %q, want starting", status.Process.Status)
		}
		if got := statusOf(t, status, "asr-router").Status; got != "pending" {
			t.Errorf("asr-router.Status = %q, want pending", got)
		}
	})

	t.Run("没有插件", func(t *testing.T) {
		home, _ := newTestHome(t, nil)
		m := NewManager(managerOptions(home))

		status := m.Status()
		if status.Process.Status != "disabled" {
			t.Errorf("Process.Status = %q, want disabled", status.Process.Status)
		}
		if status.Plugins == nil {
			t.Fatal("Plugins 不能为 nil, 必须序列化为 []")
		}
		if len(status.Plugins) != 0 {
			t.Errorf("Plugins = %+v, want 空", status.Plugins)
		}
		body, err := json.Marshal(status)
		if err != nil {
			t.Fatalf("序列化 Status 失败: %v", err)
		}
		if !strings.Contains(string(body), `"plugins":[]`) {
			t.Errorf("Status JSON = %s, want 含 \"plugins\":[]", body)
		}
	})
}

func TestManager_StatusAggregatesInvalid(t *testing.T) {
	home, _ := newTestHome(t, map[string][]string{
		"asr-router": validPluginFiles,
		"broken":     {"requirements.txt"},
	})
	m := NewManager(managerOptions(home))

	if got := len(m.Plugins()); got != 2 {
		t.Fatalf("Plugins() 长度 = %d, want 2（含无效项）", got)
	}
	if _, ok := m.PluginPermission("broken"); ok {
		t.Error("无效插件不应解析出权限")
	}

	status := m.Status()
	broken := statusOf(t, status, "broken")
	if broken.Status != "invalid" {
		t.Errorf("broken.Status = %q, want invalid", broken.Status)
	}
	if !strings.Contains(broken.Error, "app/main.py") {
		t.Errorf("broken.Error = %q, want 含缺失文件 app/main.py", broken.Error)
	}
	if got := status.HealthStatus(); got != "degraded" {
		t.Errorf("HealthStatus() = %q, want degraded", got)
	}
}

func TestManager_HealthStatusHealthy(t *testing.T) {
	tests := []struct {
		name   string
		status Status
		want   string
	}{
		{
			name: "全部运行",
			status: Status{
				Process: ProcessStatus{Status: "running"},
				Plugins: []PluginStatus{{Name: "a", Status: "running"}},
			},
			want: "healthy",
		},
		{
			name:   "没有插件",
			status: Status{Process: ProcessStatus{Status: "disabled"}, Plugins: []PluginStatus{}},
			want:   "healthy",
		},
		{
			name: "存在失败项",
			status: Status{
				Process: ProcessStatus{Status: "running"},
				Plugins: []PluginStatus{
					{Name: "a", Status: "running"},
					{Name: "b", Status: "failed", Error: "boom"},
				},
			},
			want: "degraded",
		},
		{
			name: "进程未运行",
			status: Status{
				Process: ProcessStatus{Status: "starting"},
				Plugins: []PluginStatus{{Name: "a", Status: "pending"}},
			},
			want: "degraded",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.HealthStatus(); got != tt.want {
				t.Errorf("HealthStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestManager_NoPluginsDoesNotStartProcess(t *testing.T) {
	home, _ := newTestHome(t, nil)
	runner := &fakeRunner{spawn: func(int) (childProcess, error) {
		t.Error("没有插件时不应拉起进程")
		return nil, nil
	}}
	m := newManagerWithRunner(managerOptions(home), runner)

	m.Start(context.Background())

	status := m.Status()
	if status.Process.Status != "disabled" {
		t.Errorf("Process.Status = %q, want disabled", status.Process.Status)
	}
	if got := runner.startCount(); got != 0 {
		t.Errorf("Runner 启动次数 = %d, want 0", got)
	}
	assertNoSDKInstalled(t, home)
}

func TestManager_Stop(t *testing.T) {
	home, pluginsDir := newTestHome(t, map[string][]string{"asr-router": validPluginFiles})
	writeFakeInterpreter(t, pluginsDir)
	proc := readyHoldingProcess(200, managerReadySingle)
	runner := &fakeRunner{spawn: func(int) (childProcess, error) { return proc, nil }}
	m := newManagerWithRunner(managerOptions(home), runner)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	defer m.Stop(ctx)

	waitFor(t, 2*time.Second, "进程进入 running", func() bool {
		return m.Status().Process.Status == "running"
	})
	if got := statusOf(t, m.Status(), "asr-router").Status; got != "running" {
		t.Errorf("asr-router.Status = %q, want running", got)
	}

	m.Stop(ctx)

	select {
	case <-proc.stdinClosed:
	default:
		t.Error("Stop 后 supervisor 未关闭子进程 stdin")
	}
	status := m.Status()
	if status.Process.Status != "stopped" {
		t.Errorf("Process.Status = %q, want stopped", status.Process.Status)
	}
	// 进程已停止，残留的加载结果不能把插件标成 running。
	if got := statusOf(t, status, "asr-router").Status; got != "pending" {
		t.Errorf("停止后 asr-router.Status = %q, want pending", got)
	}

	// 未启动时 Stop 必须安全。
	notStarted := newManagerWithRunner(managerOptions(home), runner)
	notStarted.Stop(context.Background())
	if got := runner.startCount(); got != 1 {
		t.Errorf("未启动时 Stop 不应拉起进程, 启动次数 = %d", got)
	}
}

func TestManager_RunningMergesLoadResults(t *testing.T) {
	home, pluginsDir := newTestHome(t, map[string][]string{
		"asr-router": validPluginFiles,
		"ocr":        validPluginFiles,
		"quiet":      validPluginFiles,
	})
	writeFakeInterpreter(t, pluginsDir)
	runner := &fakeRunner{spawn: func(int) (childProcess, error) {
		return readyHoldingProcess(210, managerReadyMulti), nil
	}}
	m := newManagerWithRunner(managerOptions(home), runner)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	defer m.Stop(ctx)

	waitFor(t, 2*time.Second, "进程进入 running", func() bool {
		return m.Status().Process.Status == "running"
	})

	status := m.Status()
	if got := statusOf(t, status, "asr-router").Status; got != "running" {
		t.Errorf("asr-router.Status = %q, want running", got)
	}
	ocr := statusOf(t, status, "ocr")
	if ocr.Status != "failed" {
		t.Errorf("ocr.Status = %q, want failed", ocr.Status)
	}
	if ocr.Error != "No module named 'torch'" {
		t.Errorf("ocr.Error = %q, want 去掉 error: 前缀的原因", ocr.Error)
	}
	quiet := statusOf(t, status, "quiet")
	if quiet.Status != "failed" || quiet.Error == "" {
		t.Errorf("quiet = %+v, want failed 且带原因（就绪行未报告）", quiet)
	}
	if got := status.HealthStatus(); got != "degraded" {
		t.Errorf("HealthStatus() = %q, want degraded", got)
	}
}

func TestManager_UninitializedPluginsDisabled(t *testing.T) {
	// 直接用空的临时目录作为 home，不创建 plugins 目录，模拟尚未初始化。
	home := t.TempDir()
	runner := &fakeRunner{spawn: func(int) (childProcess, error) {
		t.Error("插件未初始化时不应拉起进程")
		return nil, nil
	}}
	m := newManagerWithRunner(managerOptions(home), runner)

	before := m.Status()
	if before.Process.Status != "disabled" {
		t.Errorf("Start 前 Process.Status = %q, want disabled", before.Process.Status)
	}
	if !strings.Contains(before.Process.Error, "未初始化") {
		t.Errorf("Start 前 Process.Error = %q, want 含 未初始化", before.Process.Error)
	}

	m.Start(context.Background())

	status := m.Status()
	if status.Process.Status != "disabled" {
		t.Errorf("Process.Status = %q, want disabled", status.Process.Status)
	}
	if !strings.Contains(status.Process.Error, "未初始化") {
		t.Errorf("Start 后 Process.Error = %q, want 含 未初始化", status.Process.Error)
	}
	if !strings.Contains(status.Process.Error, "groot plugins init") {
		t.Errorf("Process.Error = %q, want 含 groot plugins init", status.Process.Error)
	}
	if len(status.Plugins) != 0 {
		t.Errorf("Plugins = %+v, want 空", status.Plugins)
	}
	if got := runner.startCount(); got != 0 {
		t.Errorf("Runner 启动次数 = %d, want 0", got)
	}
}

func TestManager_PluginsDirNotDirectory(t *testing.T) {
	home := t.TempDir()
	// plugins 位置被一个普通文件占用，区别于“目录不存在”。
	if err := os.WriteFile(PluginsDir(home), []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("创建 plugins 文件失败: %v", err)
	}
	runner := &fakeRunner{spawn: func(int) (childProcess, error) {
		t.Error("plugins 不是目录时不应拉起进程")
		return nil, nil
	}}
	m := newManagerWithRunner(managerOptions(home), runner)

	m.Start(context.Background())

	status := m.Status()
	if status.Process.Status != "disabled" {
		t.Errorf("Process.Status = %q, want disabled", status.Process.Status)
	}
	if !strings.Contains(status.Process.Error, "不是目录") {
		t.Errorf("Process.Error = %q, want 含 不是目录", status.Process.Error)
	}
	if strings.Contains(status.Process.Error, "不存在") {
		t.Errorf("Process.Error = %q, want 不含 不存在", status.Process.Error)
	}
	if got := runner.startCount(); got != 0 {
		t.Errorf("Runner 启动次数 = %d, want 0", got)
	}
}

func TestManager_InterpreterMissingDisables(t *testing.T) {
	home, _ := newTestHome(t, map[string][]string{"asr-router": validPluginFiles})
	runner := &fakeRunner{spawn: func(int) (childProcess, error) {
		t.Error("解释器缺失时不应拉起进程")
		return nil, nil
	}}
	m := newManagerWithRunner(managerOptions(home), runner)

	m.Start(context.Background())

	status := m.Status()
	if status.Process.Status != "disabled" {
		t.Errorf("Process.Status = %q, want disabled", status.Process.Status)
	}
	if status.Process.Error == "" {
		t.Error("Process.Error 为空, want 解释器缺失原因")
	}
	if !strings.Contains(status.Process.Error, "groot plugins init") {
		t.Errorf("Process.Error = %q, want 含 groot plugins init", status.Process.Error)
	}
	p := statusOf(t, status, "asr-router")
	if p.Status != "failed" {
		t.Errorf("asr-router.Status = %q, want failed", p.Status)
	}
	if p.Error != status.Process.Error {
		t.Errorf("asr-router.Error = %q, want 与进程失败原因相同 %q", p.Error, status.Process.Error)
	}
	if got := runner.startCount(); got != 0 {
		t.Errorf("Runner 启动次数 = %d, want 0", got)
	}
	assertNoSDKInstalled(t, home)
}

func TestManager_ExtractSDKFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 不受目录权限位限制，无法用只读目录触发解压失败")
	}
	home, pluginsDir := newTestHome(t, map[string][]string{"asr-router": validPluginFiles})
	writeFakeInterpreter(t, pluginsDir)
	// plugins 目录只读，SDK 解压必然失败。
	if err := os.Chmod(pluginsDir, 0o555); err != nil {
		t.Fatalf("设置只读权限失败: %v", err)
	}
	defer func() {
		if err := os.Chmod(pluginsDir, 0o755); err != nil {
			t.Fatalf("恢复权限失败: %v", err)
		}
	}()

	runner := &fakeRunner{spawn: func(int) (childProcess, error) {
		t.Error("SDK 安装失败时不应拉起进程")
		return nil, nil
	}}
	m := newManagerWithRunner(managerOptions(home), runner)

	m.Start(context.Background())

	status := m.Status()
	if status.Process.Status != "failed" {
		t.Errorf("Process.Status = %q, want failed", status.Process.Status)
	}
	if status.Process.Error == "" {
		t.Error("Process.Error 为空, want SDK 安装失败原因")
	}
	p := statusOf(t, status, "asr-router")
	if p.Status != "failed" {
		t.Errorf("asr-router.Status = %q, want failed", p.Status)
	}
	if p.Error != status.Process.Error {
		t.Errorf("asr-router.Error = %q, want 与进程失败原因相同 %q", p.Error, status.Process.Error)
	}
	if got := runner.startCount(); got != 0 {
		t.Errorf("Runner 启动次数 = %d, want 0", got)
	}
}

func TestManager_ProxyRejectsUnknownAndInvalid(t *testing.T) {
	home, _ := newTestHome(t, map[string][]string{
		"asr-router": validPluginFiles,
		"broken":     {"requirements.txt"},
	})
	m := NewManager(managerOptions(home))

	h := server.Default()
	h.GET("/plugins/asr-router", m.Proxy("asr-router"))
	h.GET("/plugins/broken", m.Proxy("broken"))
	h.GET("/plugins/nope", m.Proxy("nope"))

	for _, path := range []string{"/plugins/broken", "/plugins/nope"} {
		w := ut.PerformRequest(h.Engine, http.MethodGet, path, nil)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s 状态码 = %d, want 404", path, w.Code)
			continue
		}
		var env errbody.Envelope
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Errorf("%s 响应不是错误信封: %v", path, err)
			continue
		}
		if env.Error.Code == nil || *env.Error.Code != errbody.CodeInvalidRequest {
			t.Errorf("%s 错误码 = %v, want %q", path, env.Error.Code, errbody.CodeInvalidRequest)
		}
	}

	// 有效插件走真实代理；进程未启动时为 503，而不是 404。
	w := ut.PerformRequest(h.Engine, http.MethodGet, "/plugins/asr-router", nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("有效插件状态码 = %d, want 503", w.Code)
	}
	var env errbody.Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("响应不是错误信封: %v", err)
	}
	if env.Error.Code == nil || *env.Error.Code != errbody.CodePluginUnavailable {
		t.Errorf("有效插件错误码 = %v, want %q", env.Error.Code, errbody.CodePluginUnavailable)
	}
}

func TestManager_WarnsOnUnknownPluginInPermission(t *testing.T) {
	// permission 中出现不存在的插件名（典型是拼写错误）时记录警告，使用者才知道配置没生效。
	home, pluginsDir := newTestHome(t, map[string][]string{
		"asr-router": validPluginFiles,
	})
	writePluginYAML(t, pluginsDir, "permission:\n  asr_router: chat\n")

	log, logs := logger.NewObserved()
	NewManager(Options{HomeDir: home, Log: log, LogLevel: "info"})

	found := false
	for _, e := range logs.All() {
		if e.Level == zapcore.WarnLevel && e.ContextMap()["plugin"] == "asr_router" {
			found = true
		}
	}
	if !found {
		var msgs []string
		for _, e := range logs.All() {
			msgs = append(msgs, e.Message)
		}
		t.Errorf("未对 permission 中未识别的插件名 asr_router 记录警告; 日志: %q", msgs)
	}
}
