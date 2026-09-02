package repofactory

import (
	"github.com/jmoiron/sqlx"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/apikeydb"
	"github.com/zfd81/groot/internal/repo/memberdb"
	"github.com/zfd81/groot/internal/repo/memorydb"
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
}

// NewRepos constructs all Repository implementations.
// For SQLite dialect, Resource uses the local-fs implementation (sync disabled).
// For MySQL/PG, Resource uses the DB implementation (sync enabled).
func NewRepos(sqlxDB *sqlx.DB, dialect db.Dialect, homeDir string) *Repos {
	var resourceRepo repo.ResourceRepo
	if dialect == db.DialectSQLite {
		resourceRepo = resourcelocal.New(homeDir)
	} else {
		resourceRepo = resourcedb.New(sqlxDB, dialect)
	}
	return &Repos{
		Member:   memberdb.New(sqlxDB, dialect),
		Schedule: scheduledb.New(sqlxDB, dialect),
		Memory:   memorydb.New(sqlxDB, dialect),
		Resource: resourceRepo,
		User:     userdb.New(sqlxDB, dialect),
		Model:    modeldb.New(sqlxDB, dialect),
		APIKey:   apikeydb.New(sqlxDB, dialect),
	}
}
