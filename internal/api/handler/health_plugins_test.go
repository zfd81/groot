package handler

import (
	"reflect"
	"testing"

	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/plugin"
)

// fakePluginStatusProvider 是 PluginStatusProvider 的测试替身。
type fakePluginStatusProvider struct {
	status plugin.Status
}

func (f fakePluginStatusProvider) Status() plugin.Status {
	return f.status
}

// TestHealth_PluginsCheck 状态含一个 running 与一个 failed 时：
// 检查项状态为 degraded，info.plugins 有两项，失败项带 error。
func TestHealth_PluginsCheck(t *testing.T) {
	h := &HealthHandler{}
	h.SetPluginStatusProvider(fakePluginStatusProvider{status: plugin.Status{
		Process: plugin.ProcessStatus{Status: "running", PID: 1234},
		Plugins: []plugin.PluginStatus{
			{Name: "asr-router", Status: "running"},
			{Name: "ocr", Status: "failed", Error: "No module named 'torch'"},
		},
	}})

	check, ok := h.pluginsCheck()
	if !ok {
		t.Fatal("pluginsCheck ok = false, want true")
	}
	if check.Status != "degraded" {
		t.Errorf("check.Status = %q, want degraded", check.Status)
	}
	st, ok := check.Info.(plugin.Status)
	if !ok {
		t.Fatalf("check.Info 类型 = %T, want plugin.Status", check.Info)
	}
	if len(st.Plugins) != 2 {
		t.Fatalf("len(info.plugins) = %d, want 2", len(st.Plugins))
	}
	if st.Plugins[1].Name != "ocr" || st.Plugins[1].Status != "failed" {
		t.Errorf("失败项 = %+v", st.Plugins[1])
	}
	if st.Plugins[1].Error != "No module named 'torch'" {
		t.Errorf("失败项 error = %q", st.Plugins[1].Error)
	}
}

// TestHealth_PluginsHealthy 全部 running 时检查项为 healthy。
func TestHealth_PluginsHealthy(t *testing.T) {
	h := &HealthHandler{}
	h.SetPluginStatusProvider(fakePluginStatusProvider{status: plugin.Status{
		Process: plugin.ProcessStatus{Status: "running", PID: 1234},
		Plugins: []plugin.PluginStatus{
			{Name: "asr-router", Status: "running"},
			{Name: "ocr", Status: "running"},
		},
	}})

	check, ok := h.pluginsCheck()
	if !ok {
		t.Fatal("pluginsCheck ok = false, want true")
	}
	if check.Status != "healthy" {
		t.Errorf("check.Status = %q, want healthy", check.Status)
	}
}

// TestHealth_NoProviderOmitsPlugins 未设置 provider 时不做 plugins 检查。
func TestHealth_NoProviderOmitsPlugins(t *testing.T) {
	h := &HealthHandler{}
	if _, ok := h.pluginsCheck(); ok {
		t.Error("pluginsCheck ok = true, want false（未设置 provider）")
	}
}

// TestAddPluginsCheck_Healthy 健康状态下写入 plugins 项，Info 为 Status 值本身。
func TestAddPluginsCheck_Healthy(t *testing.T) {
	st := plugin.Status{
		Process: plugin.ProcessStatus{Status: "running", PID: 1234},
		Plugins: []plugin.PluginStatus{{Name: "asr-router", Status: "running"}},
	}
	h := &HealthHandler{}
	h.SetPluginStatusProvider(fakePluginStatusProvider{status: st})

	checks := map[string]types.CheckInfo{}
	h.addPluginsCheck(checks)

	check, ok := checks["plugins"]
	if !ok {
		t.Fatal("checks 缺少 plugins 项")
	}
	if check.Status != "healthy" {
		t.Errorf("check.Status = %q, want healthy", check.Status)
	}
	if !reflect.DeepEqual(check.Info, st) {
		t.Errorf("check.Info = %#v, want %#v", check.Info, st)
	}
}

// TestAddPluginsCheck_Degraded 存在失败插件时写入 degraded。
func TestAddPluginsCheck_Degraded(t *testing.T) {
	h := &HealthHandler{}
	h.SetPluginStatusProvider(fakePluginStatusProvider{status: plugin.Status{
		Process: plugin.ProcessStatus{Status: "running"},
		Plugins: []plugin.PluginStatus{
			{Name: "ocr", Status: "failed", Error: "No module named 'torch'"},
		},
	}})

	checks := map[string]types.CheckInfo{}
	h.addPluginsCheck(checks)

	check, ok := checks["plugins"]
	if !ok {
		t.Fatal("checks 缺少 plugins 项")
	}
	if check.Status != "degraded" {
		t.Errorf("check.Status = %q, want degraded", check.Status)
	}
}

// TestAddPluginsCheck_NilProvider 未设置 provider 时 map 保持为空，不写入 plugins。
func TestAddPluginsCheck_NilProvider(t *testing.T) {
	h := &HealthHandler{}

	checks := map[string]types.CheckInfo{}
	h.addPluginsCheck(checks)

	if _, ok := checks["plugins"]; ok {
		t.Error("checks 含 plugins 项, want 不含（未设置 provider）")
	}
	if len(checks) != 0 {
		t.Errorf("checks = %#v, want empty", checks)
	}
}
