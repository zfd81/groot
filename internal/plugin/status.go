package plugin

// Status 是 Python 插件的整体状态快照，用于健康检查与状态展示。
type Status struct {
	Process ProcessStatus  `json:"process"`
	Plugins []PluginStatus `json:"plugins"`
}

// PluginStatus 是单个插件的状态。
type PluginStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// HealthStatus 返回 Python 插件整体的健康状态：healthy 或 degraded。
// 没有任何插件、或进程与全部插件都在运行时为 healthy，其余为 degraded。
func (s Status) HealthStatus() string {
	if len(s.Plugins) == 0 {
		return "healthy"
	}
	if s.Process.Status != "running" {
		return "degraded"
	}
	for _, p := range s.Plugins {
		if p.Status != "running" {
			return "degraded"
		}
	}
	return "healthy"
}
