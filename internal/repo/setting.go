// internal/repo/setting.go
package repo

import "context"

// Scope 配置作用域类型。作用域与实体标识分为两个字段，
// 使「作用域类型」与「实体标识」各自占据独立值域：
// 实体标识不会与 ScopeGlobal 这类保留值冲突。
type Scope string

const (
	ScopeGlobal Scope = "global"
	ScopeUser   Scope = "user"
)

// scopePriority 定义取值时「由具体到通用」的回落顺序，数值越小越优先。
// 优先级由此表决定，不依赖数据库对 scope 字符串的排序结果
// （例如按字母序 'global' 会排在 'user' 之前，与优先级相反）。
var scopePriority = map[Scope]int{
	ScopeUser:   0,
	ScopeGlobal: 1,
}

// ScopePriority 返回作用域的优先级；未登记的作用域返回 (-1, false)，
// 返回 -1 而非 0 是为了让未检查 ok 的调用方也不会与最高优先级 0 混淆。
func ScopePriority(s Scope) (int, bool) {
	p, ok := scopePriority[s]
	if !ok {
		return -1, false
	}
	return p, true
}

// Setting 配置表中的一行。
type Setting struct {
	Scope     Scope
	ScopeID   string
	Name      string // 配置键，点号分层，镜像 YAML 的层级路径，如 voice.model
	Value     string // 统一以文本保存，类型转换由配置对象负责
	UpdatedAt int64  // Unix 毫秒
}

// SettingRepo 配置表仓储。表内只保存使用者明确修改过的值，
// 缺失的键由代码默认值提供，因此查询未命中不是错误。
type SettingRepo interface {
	// Get 查询单个键；未找到返回 ErrNotFound
	Get(ctx context.Context, scope Scope, scopeID, name string) (*Setting, error)
	// ListByScope 返回某个作用域实体下的全部配置，按 name 升序；
	// 无数据返回空切片而非错误
	ListByScope(ctx context.Context, scope Scope, scopeID string) ([]*Setting, error)
	// Upsert 按 (scope, scope_id, name) 写入或覆盖，并刷新 updated_at。
	// scope 为 ScopeGlobal 时 scopeID 必须为空串，否则返回错误。
	// 多条在同一事务内写入，任一失败整体回滚；updated_at 由实现按当前时间填充，忽略入参值。
	Upsert(ctx context.Context, items ...*Setting) error
	// Delete 删除单个键，等价于「恢复默认」；未找到返回 nil 而非 ErrNotFound
	Delete(ctx context.Context, scope Scope, scopeID, name string) error
}
