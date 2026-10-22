package handler

import (
	"testing"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/memorydb"
)

// newOwnerTestContext 构造带身份上下文的 RequestContext；caller 为空时不注入 caller 键。
func newOwnerTestContext(caller, tenant, user string) *app.RequestContext {
	rc := app.NewContext(0)
	if caller != "" {
		rc.Set("caller", caller)
	}
	rc.Set("tenant_id", tenant)
	rc.Set("user_id", user)
	return rc
}

func TestRequireOwner(t *testing.T) {
	tests := []struct {
		name       string
		caller     string
		tenant     string
		user       string
		wantOK     bool
		wantOwner  *repo.SessionOwner
		wantStatus int    // 0 表示不应写响应
		wantBody   string // 为空表示不校验
	}{
		{
			name:   "web caller passes with nil owner",
			caller: "web", tenant: "", user: "",
			wantOK: true, wantOwner: nil,
		},
		{
			name:   "empty tenant → 403",
			caller: "svc", tenant: "", user: "u1",
			wantOK: false, wantStatus: 403,
			wantBody: `{"status":"forbidden","message":"API Key 未绑定租户"}`,
		},
		{
			name:   "empty user → 400",
			caller: "svc", tenant: "crm", user: "",
			wantOK: false, wantStatus: 400,
			wantBody: `{"status":"invalid_request","message":"缺少或非法的 X-User-ID"}`,
		},
		{
			name:   "full identity → owner",
			caller: "svc", tenant: "crm", user: "u1",
			wantOK: true, wantOwner: &repo.SessionOwner{TenantID: "crm", UserID: "u1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := newOwnerTestContext(tt.caller, tt.tenant, tt.user)
			owner, ok := requireOwner(rc)

			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if tt.wantOwner == nil {
				if owner != nil {
					t.Errorf("owner = %+v, want nil", owner)
				}
			} else {
				if owner == nil {
					t.Fatalf("owner = nil, want %+v", tt.wantOwner)
				}
				if *owner != *tt.wantOwner {
					t.Errorf("owner = %+v, want %+v", *owner, *tt.wantOwner)
				}
			}

			if tt.wantStatus == 0 {
				if len(rc.Response.Body()) != 0 {
					t.Errorf("expected no response body, got %s", rc.Response.Body())
				}
				return
			}
			if got := rc.Response.StatusCode(); got != tt.wantStatus {
				t.Errorf("status = %d, want %d", got, tt.wantStatus)
			}
			if ct := string(rc.Response.Header.ContentType()); ct != "application/json" {
				t.Errorf("content-type = %q, want application/json", ct)
			}
			if got := string(rc.Response.Body()); got != tt.wantBody {
				t.Errorf("body = %s, want %s", got, tt.wantBody)
			}
		})
	}
}

// newOwnerTestManager 构造挂了临时 SQLite 库的 memory.Manager。
func newOwnerTestManager(t *testing.T) *memory.Manager {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return memory.NewManager(logger.NewNop(), memorydb.New(sqlxDB, dialect))
}

func TestOwnsOrAdmin(t *testing.T) {
	mem := newOwnerTestManager(t)
	if err := mem.CreateSession("own-1", "crm", "u1", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	tests := []struct {
		name  string
		owner *repo.SessionOwner
		sid   string
		want  bool
	}{
		{"nil owner, existing session", nil, "own-1", true},
		{"nil owner, missing session", nil, "own-missing", false},
		{"owner matches", &repo.SessionOwner{TenantID: "crm", UserID: "u1"}, "own-1", true},
		{"owner tenant mismatch", &repo.SessionOwner{TenantID: "erp", UserID: "u1"}, "own-1", false},
		{"owner user mismatch", &repo.SessionOwner{TenantID: "crm", UserID: "u2"}, "own-1", false},
		{"owner matches, missing session", &repo.SessionOwner{TenantID: "crm", UserID: "u1"}, "own-missing", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ownsOrAdmin(mem, tt.owner, tt.sid); got != tt.want {
				t.Errorf("ownsOrAdmin(%+v, %q) = %v, want %v", tt.owner, tt.sid, got, tt.want)
			}
		})
	}
}
