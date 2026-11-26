package handler

import (
	"reflect"
	"testing"

	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/pysvc"
)

// fakeServiceStatusProvider 是 ServiceStatusProvider 的测试替身。
type fakeServiceStatusProvider struct {
	status pysvc.Status
}

func (f fakeServiceStatusProvider) Status() pysvc.Status {
	return f.status
}

// TestHealth_ServicesCheck 状态含一个 running 与一个 failed 时：
// 检查项状态为 degraded，info.services 有两项，失败项带 error。
func TestHealth_ServicesCheck(t *testing.T) {
	h := &HealthHandler{}
	h.SetServiceStatusProvider(fakeServiceStatusProvider{status: pysvc.Status{
		Process: pysvc.ProcessStatus{Status: "running", PID: 1234},
		Services: []pysvc.ServiceStatus{
			{Name: "asr-router", Status: "running"},
			{Name: "ocr", Status: "failed", Error: "No module named 'torch'"},
		},
	}})

	check, ok := h.servicesCheck()
	if !ok {
		t.Fatal("servicesCheck ok = false, want true")
	}
	if check.Status != "degraded" {
		t.Errorf("check.Status = %q, want degraded", check.Status)
	}
	st, ok := check.Info.(pysvc.Status)
	if !ok {
		t.Fatalf("check.Info 类型 = %T, want pysvc.Status", check.Info)
	}
	if len(st.Services) != 2 {
		t.Fatalf("len(info.services) = %d, want 2", len(st.Services))
	}
	if st.Services[1].Name != "ocr" || st.Services[1].Status != "failed" {
		t.Errorf("失败项 = %+v", st.Services[1])
	}
	if st.Services[1].Error != "No module named 'torch'" {
		t.Errorf("失败项 error = %q", st.Services[1].Error)
	}
}

// TestHealth_ServicesHealthy 全部 running 时检查项为 healthy。
func TestHealth_ServicesHealthy(t *testing.T) {
	h := &HealthHandler{}
	h.SetServiceStatusProvider(fakeServiceStatusProvider{status: pysvc.Status{
		Process: pysvc.ProcessStatus{Status: "running", PID: 1234},
		Services: []pysvc.ServiceStatus{
			{Name: "asr-router", Status: "running"},
			{Name: "ocr", Status: "running"},
		},
	}})

	check, ok := h.servicesCheck()
	if !ok {
		t.Fatal("servicesCheck ok = false, want true")
	}
	if check.Status != "healthy" {
		t.Errorf("check.Status = %q, want healthy", check.Status)
	}
}

// TestHealth_NoProviderOmitsServices 未设置 provider 时不做 services 检查。
func TestHealth_NoProviderOmitsServices(t *testing.T) {
	h := &HealthHandler{}
	if _, ok := h.servicesCheck(); ok {
		t.Error("servicesCheck ok = true, want false（未设置 provider）")
	}
}

// TestAddServicesCheck_Healthy 健康状态下写入 services 项，Info 为 Status 值本身。
func TestAddServicesCheck_Healthy(t *testing.T) {
	st := pysvc.Status{
		Process:  pysvc.ProcessStatus{Status: "running", PID: 1234},
		Services: []pysvc.ServiceStatus{{Name: "asr-router", Status: "running"}},
	}
	h := &HealthHandler{}
	h.SetServiceStatusProvider(fakeServiceStatusProvider{status: st})

	checks := map[string]types.CheckInfo{}
	h.addServicesCheck(checks)

	check, ok := checks["services"]
	if !ok {
		t.Fatal("checks 缺少 services 项")
	}
	if check.Status != "healthy" {
		t.Errorf("check.Status = %q, want healthy", check.Status)
	}
	if !reflect.DeepEqual(check.Info, st) {
		t.Errorf("check.Info = %#v, want %#v", check.Info, st)
	}
}

// TestAddServicesCheck_Degraded 存在失败子服务时写入 degraded。
func TestAddServicesCheck_Degraded(t *testing.T) {
	h := &HealthHandler{}
	h.SetServiceStatusProvider(fakeServiceStatusProvider{status: pysvc.Status{
		Process: pysvc.ProcessStatus{Status: "running"},
		Services: []pysvc.ServiceStatus{
			{Name: "ocr", Status: "failed", Error: "No module named 'torch'"},
		},
	}})

	checks := map[string]types.CheckInfo{}
	h.addServicesCheck(checks)

	check, ok := checks["services"]
	if !ok {
		t.Fatal("checks 缺少 services 项")
	}
	if check.Status != "degraded" {
		t.Errorf("check.Status = %q, want degraded", check.Status)
	}
}

// TestAddServicesCheck_NilProvider 未设置 provider 时 map 保持为空，不写入 services。
func TestAddServicesCheck_NilProvider(t *testing.T) {
	h := &HealthHandler{}

	checks := map[string]types.CheckInfo{}
	h.addServicesCheck(checks)

	if _, ok := checks["services"]; ok {
		t.Error("checks 含 services 项, want 不含（未设置 provider）")
	}
	if len(checks) != 0 {
		t.Errorf("checks = %#v, want empty", checks)
	}
}
