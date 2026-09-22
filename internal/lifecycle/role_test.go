package lifecycle

import "testing"

func TestDetectRole(t *testing.T) {
	cases := []struct {
		name          string
		supervisedEnv string
		singleFlag    bool
		want          Role
	}{
		{"默认为监督进程", "", false, RoleSupervisor},
		{"环境变量标记为工作进程", "1", false, RoleWorker},
		{"单进程标志", "", true, RoleSingle},
		{"环境变量优先于单进程标志", "1", true, RoleWorker},
		{"环境变量非 1 不算工作进程", "true", false, RoleSupervisor},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DetectRole(c.supervisedEnv, c.singleFlag); got != c.want {
				t.Errorf("DetectRole(%q, %v) = %v, want %v", c.supervisedEnv, c.singleFlag, got, c.want)
			}
		})
	}
}

func TestRole_ProcessMode(t *testing.T) {
	if got := RoleWorker.ProcessMode(); got != "supervised" {
		t.Errorf("RoleWorker.ProcessMode() = %q, want supervised", got)
	}
	if got := RoleSingle.ProcessMode(); got != "single" {
		t.Errorf("RoleSingle.ProcessMode() = %q, want single", got)
	}
	if got := RoleSupervisor.ProcessMode(); got != "supervisor" {
		t.Errorf("RoleSupervisor.ProcessMode() = %q, want supervisor", got)
	}
}

func TestRole_RestartSupported(t *testing.T) {
	if !RoleWorker.RestartSupported() {
		t.Error("RoleWorker 应支持重启")
	}
	if RoleSingle.RestartSupported() {
		t.Error("RoleSingle 不应支持重启")
	}
	if RoleSupervisor.RestartSupported() {
		t.Error("RoleSupervisor 不应支持重启")
	}
}
