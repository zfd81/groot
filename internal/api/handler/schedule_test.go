package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/jmoiron/sqlx"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo/scheduledb"
	"github.com/zfd81/groot/internal/repo/settingdb"
	"github.com/zfd81/groot/internal/schedule"
	"github.com/zfd81/groot/internal/setting"
)

// newScheduleHandlerForTest 用真实 SQLite 配置表建 handler。
// enabled 是 schedule.enabled 的初始状态（写入配置表；基准值是代码默认的 false）；
// mgr 为 nil 时模拟非 Leader 节点。
func newScheduleHandlerForTest(t *testing.T, mgr *schedule.Manager, enabled bool) (*ScheduleHandler, *setting.Settings) {
	t.Helper()
	sqlxDB, dialect := newScheduleTestDB(t)
	settings := newScheduleSettings(t, sqlxDB, dialect, enabled)
	return NewScheduleHandler(&mgr, settings, logger.NewNop()), settings
}

// newScheduleTestDB 打开一个已跑过迁移的临时 SQLite 库。
func newScheduleTestDB(t *testing.T) (*sqlx.DB, db.Dialect) {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return sqlxDB, dialect
}

// newScheduleSettings 用 bootstrap 静态层加真实配置表建 Settings。
// 调度开关的基准值是代码默认的 false，enabled 为 true 时写进配置表以模拟已开启的部署。
func newScheduleSettings(t *testing.T, sqlxDB *sqlx.DB, dialect db.Dialect, enabled bool) *setting.Settings {
	t.Helper()
	settings := setting.New(runtimeBoot(), settingdb.New(sqlxDB, dialect))
	if enabled {
		setScheduleSwitch(t, settings, true)
	}
	return settings
}

// setScheduleSwitch 把 schedule.enabled 写进配置表，其余分区沿用 YAML 值以通过整体校验。
func setScheduleSwitch(t *testing.T, settings *setting.Settings, enabled bool) {
	t.Helper()
	rt := settings.RuntimeStatic()
	rt.Schedule.Enabled = enabled
	if err := settings.SetRuntime(context.Background(), rt); err != nil {
		t.Fatalf("SetRuntime: %v", err)
	}
}

func TestScheduleHandler_NilManagerReturns503(t *testing.T) {
	h, _ := newScheduleHandlerForTest(t, nil, true)

	rc := callJSON(h.List, consts.MethodGet, "", nil)
	if rc.Response.StatusCode() != 503 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if got := bodyStatus(t, rc); got != "schedule_unavailable" {
		t.Fatalf("status=%q, want schedule_unavailable", got)
	}
	if !strings.Contains(string(rc.Response.Body()), "不可用") {
		t.Fatalf("nil 管理器应提示不可用, body=%s", rc.Response.Body())
	}
}

func TestScheduleHandler_DisabledSwitchReturns503(t *testing.T) {
	h, _ := newScheduleHandlerForTest(t, &schedule.Manager{}, false)

	rc := callJSON(h.List, consts.MethodGet, "", nil)
	if rc.Response.StatusCode() != 503 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if got := bodyStatus(t, rc); got != "schedule_unavailable" {
		t.Fatalf("status=%q, want schedule_unavailable", got)
	}
	if !strings.Contains(string(rc.Response.Body()), "未启用") {
		t.Fatalf("开关关闭应提示未启用, body=%s", rc.Response.Body())
	}
}

func TestScheduleHandler_SwitchReadFromTable(t *testing.T) {
	want := &schedule.Manager{}
	h, settings := newScheduleHandlerForTest(t, want, false)

	// 表里写 true 覆盖 YAML 的 false：放行，且返回的就是那个管理器。
	setScheduleSwitch(t, settings, true)
	rc := app.NewContext(0)
	mgr, ok := h.manager(context.Background(), rc)
	if !ok {
		t.Fatalf("表内开关为 true 时应放行, body=%s", rc.Response.Body())
	}
	if mgr != want {
		t.Fatalf("返回的管理器不是注入的那个")
	}

	// 表里改回 false：下一次请求即刻生效。
	setScheduleSwitch(t, settings, false)
	rc = app.NewContext(0)
	mgr, ok = h.manager(context.Background(), rc)
	if ok || mgr != nil {
		t.Fatalf("表内开关为 false 时应拒绝, ok=%v mgr=%v", ok, mgr)
	}
	if rc.Response.StatusCode() != 503 || bodyStatus(t, rc) != "schedule_unavailable" {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}

func TestScheduleHandler_AllEndpointsGated(t *testing.T) {
	h, _ := newScheduleHandlerForTest(t, &schedule.Manager{}, false)
	params := map[string]string{"id": "x"}

	cases := []struct {
		name   string
		fn     func(context.Context, *app.RequestContext)
		method string
		params map[string]string
	}{
		{"List", h.List, consts.MethodGet, nil},
		{"Get", h.Get, consts.MethodGet, params},
		{"Delete", h.Delete, consts.MethodDelete, params},
		{"Disable", h.Disable, consts.MethodPost, params},
		{"Enable", h.Enable, consts.MethodPost, params},
		{"Archive", h.Archive, consts.MethodPost, params},
		{"History", h.History, consts.MethodGet, params},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := callJSON(tc.fn, tc.method, "", tc.params)
			if rc.Response.StatusCode() != 503 {
				t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
			}
			if got := bodyStatus(t, rc); got != "schedule_unavailable" {
				t.Fatalf("status=%q, want schedule_unavailable", got)
			}
		})
	}
}

func TestScheduleHandler_EnabledListPassesThrough(t *testing.T) {
	sqlxDB, dialect := newScheduleTestDB(t)
	settings := newScheduleSettings(t, sqlxDB, dialect, false)
	// List 只经 storage，不碰 engine/runner，二者传 nil 即可。
	mgr := schedule.NewManager(
		schedule.NewStorage(scheduledb.New(sqlxDB, dialect), logger.NewNop()),
		nil, nil, logger.NewNop(),
	)
	h := NewScheduleHandler(&mgr, settings, logger.NewNop())

	setScheduleSwitch(t, settings, true)
	rc := callJSON(h.List, consts.MethodGet, "", nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if got := strings.TrimSpace(string(rc.Response.Body())); got != "[]" {
		t.Fatalf("空表应返回 [] 而非 %q", got)
	}
}
