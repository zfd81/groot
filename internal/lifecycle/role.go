package lifecycle

// EnvSupervised 是监督进程拉起工作进程时设置的环境变量；值为 "1" 表示工作进程。
const EnvSupervised = "GROOT_SUPERVISED"

// Role 是进程在双进程模型中的角色。
type Role int

const (
	// RoleSupervisor 监督进程：不加载业务，只拉起并看护工作进程。
	RoleSupervisor Role = iota
	// RoleWorker 工作进程：由监督进程拉起，承担全部业务，支持重启。
	RoleWorker
	// RoleSingle 单进程：直接承担全部业务，没有监督进程，不支持重启。
	RoleSingle
)

// DetectRole 按优先级判定角色：环境变量 GROOT_SUPERVISED=1 → Worker；
// 否则 --single-process → Single；否则 Supervisor。
func DetectRole(supervisedEnv string, singleFlag bool) Role {
	if supervisedEnv == "1" {
		return RoleWorker
	}
	if singleFlag {
		return RoleSingle
	}
	return RoleSupervisor
}

// ProcessMode 返回健康检查与 status 命令展示用的模式字符串。
func (r Role) ProcessMode() string {
	switch r {
	case RoleWorker:
		return "supervised"
	case RoleSingle:
		return "single"
	default:
		return "supervisor"
	}
}

// RestartSupported 只有工作进程支持重启：以退出码 3 退出后由监督进程拉起。
func (r Role) RestartSupported() bool {
	return r == RoleWorker
}
