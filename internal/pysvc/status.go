package pysvc

// Status 是 Python 服务的整体状态快照，用于健康检查与状态展示。
type Status struct {
	Process  ProcessStatus   `json:"process"`
	Services []ServiceStatus `json:"services"`
}

// ServiceStatus 是单个子服务的状态。
type ServiceStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// HealthStatus 返回 Python 服务整体的健康状态：healthy 或 degraded。
// 没有任何子服务、或进程与全部子服务都在运行时为 healthy，其余为 degraded。
func (s Status) HealthStatus() string {
	if len(s.Services) == 0 {
		return "healthy"
	}
	if s.Process.Status != "running" {
		return "degraded"
	}
	for _, svc := range s.Services {
		if svc.Status != "running" {
			return "degraded"
		}
	}
	return "healthy"
}
