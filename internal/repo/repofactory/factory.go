package repofactory

import (
	"github.com/jmoiron/sqlx"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/apikeydb"
	"github.com/zfd81/groot/internal/repo/memberdb"
	"github.com/zfd81/groot/internal/repo/memorydb"
	"github.com/zfd81/groot/internal/repo/messagedb"
	"github.com/zfd81/groot/internal/repo/modeldb"
	"github.com/zfd81/groot/internal/repo/resourcedb"
	"github.com/zfd81/groot/internal/repo/resourcelocal"
	"github.com/zfd81/groot/internal/repo/scheduledb"
	"github.com/zfd81/groot/internal/repo/userdb"
	"github.com/zfd81/groot/internal/schedule"
)

// Repos holds all domain repositories constructed from one DB connection.
type Repos struct {
	Member   repo.MemberRepo
	Schedule schedule.ScheduleRepo
	Memory   repo.MemoryRepo
	Resource repo.ResourceRepo
	User     repo.UserRepo
	Model    repo.ModelRepo
	APIKey   repo.APIKeyRepo
	Message  repo.MessageRepo

	// SyncResource 是配置同步(push/pull/diff)要比较的远端仓储。
	// SQLite 单机模式下没有可比较的远端(Resource 指向本地文件系统,
	// 与同步源是同一份文件),此时为 nil,交由 sync.NewSyncManager 返回
	// disabledSyncManager。
	SyncResource repo.ResourceRepo
}

// NewRepos constructs all Repository implementations.
// For SQLite dialect, Resource uses the local-fs implementation and SyncResource
// is nil (sync disabled). For MySQL/PG, both use the DB implementation
// (sync enabled).
func NewRepos(sqlxDB *sqlx.DB, dialect db.Dialect, homeDir string) *Repos {
	var resourceRepo, syncResourceRepo repo.ResourceRepo
	if dialect == db.DialectSQLite {
		resourceRepo = resourcelocal.New(homeDir)
	} else {
		resourceRepo = resourcedb.New(sqlxDB, dialect)
		syncResourceRepo = resourceRepo
	}
	return &Repos{
		Member:   memberdb.New(sqlxDB, dialect),
		Schedule: scheduledb.New(sqlxDB, dialect),
		Memory:   memorydb.New(sqlxDB, dialect),
		Resource: resourceRepo,
		User:     userdb.New(sqlxDB, dialect),
		Model:    modeldb.New(sqlxDB, dialect),
		APIKey:   apikeydb.New(sqlxDB, dialect),
		Message:  messagedb.New(sqlxDB, dialect),

		SyncResource: syncResourceRepo,
	}
}
