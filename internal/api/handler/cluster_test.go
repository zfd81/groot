package handler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route/param"

	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/cluster"
	"github.com/zfd81/groot/internal/lifecycle"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
)

// fakeMemberRepo 是 repo.MemberRepo 的最小实现：ClusterHandler 只消费 ListAll 与 Get，
// 其余方法给出未实现桩——若有调用会返回错误暴露问题。
type fakeMemberRepo struct {
	members []*repo.Member
	err     error
}

func (f *fakeMemberRepo) ListAll(ctx context.Context) ([]*repo.Member, error) {
	return f.members, f.err
}

func (f *fakeMemberRepo) Register(ctx context.Context, m *repo.Member) error { return errNotImpl }
func (f *fakeMemberRepo) Heartbeat(ctx context.Context, regID string) error  { return errNotImpl }
func (f *fakeMemberRepo) UpdateRole(ctx context.Context, regID, role string) error {
	return errNotImpl
}
func (f *fakeMemberRepo) Get(ctx context.Context, regID string) (*repo.Member, error) {
	if f.err != nil {
		return nil, f.err
	}
	for _, m := range f.members {
		if m.RegID == regID {
			return m, nil
		}
	}
	return nil, repo.ErrNotFound
}
func (f *fakeMemberRepo) Remove(ctx context.Context, regID string) error { return errNotImpl }
func (f *fakeMemberRepo) RemoveExpired(ctx context.Context, before time.Time) (int, error) {
	return 0, errNotImpl
}

var errNotImpl = errors.New("not implemented")

// serveCluster 构造 GET 请求并执行 handler，返回响应上下文。
func serveCluster(h *ClusterHandler) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	h.Serve(context.Background(), rc)
	return rc
}

// decodeCluster 解析 200 响应体，非 200 直接失败。
func decodeCluster(t *testing.T, rc *app.RequestContext) types.ClusterResponse {
	t.Helper()
	if got := rc.Response.StatusCode(); got != 200 {
		t.Fatalf("expected 200, got %d body=%s", got, rc.Response.Body())
	}
	var resp types.ClusterResponse
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rc.Response.Body())
	}
	return resp
}

// TestClusterHandler_ListsLeaderFirst 验证 /web/cluster 响应：
//   - leader 排在首位，follower 按 reg_id 升序
//   - address 为 Host:Port 形式
//   - 时间字段是毫秒时间戳
func TestClusterHandler_ListsLeaderFirst(t *testing.T) {
	joined := time.Date(2026, 9, 4, 10, 0, 0, 0, time.Local)
	beat := joined.Add(2 * time.Minute)

	fake := &fakeMemberRepo{members: []*repo.Member{
		{RegID: "n-003", Role: cluster.RoleFollower, Host: "10.0.0.3", Port: 8080, Pid: 33, HeartbeatAt: beat, CreatedAt: joined},
		{RegID: "n-001", Role: cluster.RoleFollower, Host: "10.0.0.1", Port: 8080, Pid: 11, HeartbeatAt: beat, CreatedAt: joined},
		{RegID: "n-002", Role: cluster.RoleLeader, Host: "10.0.0.2", Port: 9090, Pid: 22, HeartbeatAt: beat, CreatedAt: joined},
	}}

	resp := decodeCluster(t, serveCluster(NewClusterHandler(fake, nil, nil, false, logger.NewNop())))

	if len(resp.Members) != 3 {
		t.Fatalf("expected 3 members, got %d: %+v", len(resp.Members), resp.Members)
	}
	wantOrder := []string{"n-002", "n-001", "n-003"} // leader 优先，其余按 reg_id 升序
	for i, want := range wantOrder {
		if resp.Members[i].RegID != want {
			t.Errorf("Members[%d].RegID = %s, want %s", i, resp.Members[i].RegID, want)
		}
	}

	leader := resp.Members[0]
	if leader.Role != cluster.RoleLeader {
		t.Errorf("Members[0].Role = %s, want %s", leader.Role, cluster.RoleLeader)
	}
	if leader.Address != "10.0.0.2:9090" {
		t.Errorf("Address = %s, want 10.0.0.2:9090", leader.Address)
	}
	if leader.Pid != 22 {
		t.Errorf("Pid = %d, want 22", leader.Pid)
	}
	if leader.CreatedAt != joined.UnixMilli() {
		t.Errorf("CreatedAt = %d, want %d", leader.CreatedAt, joined.UnixMilli())
	}
	if leader.HeartbeatAt != beat.UnixMilli() {
		t.Errorf("HeartbeatAt = %d, want %d", leader.HeartbeatAt, beat.UnixMilli())
	}
}

// TestClusterHandler_NilRepo 验证未启用集群（members 为 nil）时返回 200 空列表，
// 且 members 字段是 [] 而非 null，前端可直接遍历。
func TestClusterHandler_NilRepo(t *testing.T) {
	rc := serveCluster(NewClusterHandler(nil, nil, nil, false, logger.NewNop()))
	resp := decodeCluster(t, rc)

	if len(resp.Members) != 0 {
		t.Errorf("expected empty members, got %+v", resp.Members)
	}
	if body := string(rc.Response.Body()); !contains(body, `"members":[]`) {
		t.Errorf("expected members:[] in body, got %s", body)
	}
}

// TestClusterHandler_EmptyList 验证仓库返回空切片时同样输出 [] 而非 null。
func TestClusterHandler_EmptyList(t *testing.T) {
	rc := serveCluster(NewClusterHandler(&fakeMemberRepo{}, nil, nil, false, logger.NewNop()))
	decodeCluster(t, rc)

	if body := string(rc.Response.Body()); !contains(body, `"members":[]`) {
		t.Errorf("expected members:[] in body, got %s", body)
	}
}

// TestClusterHandler_RepoError 验证查询失败返回 500，且不泄漏底层错误细节。
func TestClusterHandler_RepoError(t *testing.T) {
	fake := &fakeMemberRepo{err: errors.New("db connection refused")}
	rc := serveCluster(NewClusterHandler(fake, nil, nil, false, logger.NewNop()))

	if got := rc.Response.StatusCode(); got != 500 {
		t.Fatalf("expected 500, got %d body=%s", got, rc.Response.Body())
	}
	if body := string(rc.Response.Body()); contains(body, "db connection refused") {
		t.Errorf("响应体不应泄漏底层错误: %s", body)
	}
}

// TestClusterHandler_NilLogger 验证 log 为 nil 时用 NewNop() 兜底，不 panic。
func TestClusterHandler_NilLogger(t *testing.T) {
	fake := &fakeMemberRepo{members: []*repo.Member{
		{RegID: "n-001", Role: cluster.RoleLeader, Host: "127.0.0.1", Port: 8080},
	}}
	resp := decodeCluster(t, serveCluster(NewClusterHandler(fake, nil, nil, false, nil)))

	if len(resp.Members) != 1 || resp.Members[0].Address != "127.0.0.1:8080" {
		t.Errorf("unexpected members: %+v", resp.Members)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ---- 以下为 Self 字段与重启端点测试 ----

// fakeSender 记录 SendMessage 收到的消息，可注入错误。
type fakeSender struct {
	sent []cluster.Message
	err  error
}

func (f *fakeSender) SendMessage(ctx context.Context, msg cluster.Message) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, msg)
	return nil
}

func fixedSelfID(id string) func() string { return func() string { return id } }

// serveRestart 构造带 reg_id 路径参数与 web_user_id 的 POST 请求并执行 Restart。
func serveRestart(h *ClusterHandler, regID string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodPost)
	rc.Params = param.Params{{Key: "reg_id", Value: regID}}
	rc.Set("web_user_id", "u-42")
	h.Restart(context.Background(), rc)
	return rc
}

func TestClusterHandler_SelfField(t *testing.T) {
	fake := &fakeMemberRepo{members: []*repo.Member{
		{RegID: "n-001", Role: cluster.RoleLeader, Host: "10.0.0.1", Port: 8080},
	}}
	resp := decodeCluster(t, serveCluster(NewClusterHandler(fake, fixedSelfID("n-001"), nil, true, logger.NewNop())))
	if resp.Self != "n-001" {
		t.Errorf("Self = %q, want n-001", resp.Self)
	}
	// 未注册集群（selfID 为 nil）时 self 为空串且字段仍存在
	rc := serveCluster(NewClusterHandler(nil, nil, nil, false, logger.NewNop()))
	if body := string(rc.Response.Body()); !contains(body, `"self":""`) {
		t.Errorf("expected self:\"\" in body, got %s", body)
	}
}

func TestRestart_UnknownMember404(t *testing.T) {
	fake := &fakeMemberRepo{members: []*repo.Member{{RegID: "n-001", Host: "10.0.0.1", Port: 8080}}}
	s := &fakeSender{}
	rc := serveRestart(NewClusterHandler(fake, fixedSelfID("n-001"), s, true, logger.NewNop()), "n-999")
	if got := rc.Response.StatusCode(); got != 404 {
		t.Fatalf("expected 404, got %d body=%s", got, rc.Response.Body())
	}
	if body := string(rc.Response.Body()); !contains(body, `"code":"member_not_found"`) {
		t.Errorf("expected member_not_found, got %s", body)
	}
	if len(s.sent) != 0 {
		t.Errorf("未知成员不应发送消息，sent=%+v", s.sent)
	}
}

func TestRestart_SelfUnsupported409(t *testing.T) {
	fake := &fakeMemberRepo{members: []*repo.Member{{RegID: "n-001", Host: "10.0.0.1", Port: 8080}}}
	s := &fakeSender{}
	rc := serveRestart(NewClusterHandler(fake, fixedSelfID("n-001"), s, false, logger.NewNop()), "n-001")
	if got := rc.Response.StatusCode(); got != 409 {
		t.Fatalf("expected 409, got %d body=%s", got, rc.Response.Body())
	}
	if body := string(rc.Response.Body()); !contains(body, `"code":"restart_unsupported"`) {
		t.Errorf("expected restart_unsupported, got %s", body)
	}
	if len(s.sent) != 0 {
		t.Errorf("单进程模式本机不应发送消息，sent=%+v", s.sent)
	}
}

func TestRestart_OtherMemberSendsPointToPoint(t *testing.T) {
	fake := &fakeMemberRepo{members: []*repo.Member{
		{RegID: "n-001", Host: "10.0.0.1", Port: 8080},
		{RegID: "n-002", Host: "10.0.0.2", Port: 8080},
	}}
	s := &fakeSender{}
	before := time.Now()
	rc := serveRestart(NewClusterHandler(fake, fixedSelfID("n-001"), s, true, logger.NewNop()), "n-002")
	if got := rc.Response.StatusCode(); got != 202 {
		t.Fatalf("expected 202, got %d body=%s", got, rc.Response.Body())
	}
	var resp types.RestartResponse
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "accepted" || resp.RegID != "n-002" || resp.Self {
		t.Errorf("unexpected resp: %+v", resp)
	}
	if len(s.sent) != 1 {
		t.Fatalf("expected 1 message, got %d", len(s.sent))
	}
	m := s.sent[0]
	if m.Type != lifecycle.MessageTypeRestart || m.TargetModule != lifecycle.ModuleName {
		t.Errorf("Type/Module = %q/%q", m.Type, m.TargetModule)
	}
	if m.TargetInstance != "n-002" {
		t.Errorf("TargetInstance = %q, want n-002（点对点）", m.TargetInstance)
	}
	if m.Priority != 1 {
		t.Errorf("Priority = %d, want 1", m.Priority)
	}
	if m.Payload["reason"] != "web" || m.Payload["requested_by"] != "u-42" {
		t.Errorf("Payload = %+v", m.Payload)
	}
	if m.ExpiresAt.Before(before.Add(90*time.Second)) || m.ExpiresAt.After(before.Add(3*time.Minute)) {
		t.Errorf("ExpiresAt = %v，应为约 2 分钟后", m.ExpiresAt)
	}
}

func TestRestart_SelfSupported202(t *testing.T) {
	fake := &fakeMemberRepo{members: []*repo.Member{{RegID: "n-001", Host: "10.0.0.1", Port: 8080}}}
	s := &fakeSender{}
	rc := serveRestart(NewClusterHandler(fake, fixedSelfID("n-001"), s, true, logger.NewNop()), "n-001")
	if got := rc.Response.StatusCode(); got != 202 {
		t.Fatalf("expected 202, got %d body=%s", got, rc.Response.Body())
	}
	var resp types.RestartResponse
	_ = json.Unmarshal(rc.Response.Body(), &resp)
	if !resp.Self {
		t.Errorf("重启本机应返回 self=true: %+v", resp)
	}
	if len(s.sent) != 1 || s.sent[0].TargetInstance != "n-001" {
		t.Errorf("本机重启同样走消息投递，sent=%+v", s.sent)
	}
}

func TestRestart_SenderError500(t *testing.T) {
	fake := &fakeMemberRepo{members: []*repo.Member{{RegID: "n-002", Host: "10.0.0.2", Port: 8080}}}
	s := &fakeSender{err: errors.New("db down")}
	rc := serveRestart(NewClusterHandler(fake, fixedSelfID("n-001"), s, true, logger.NewNop()), "n-002")
	if got := rc.Response.StatusCode(); got != 500 {
		t.Fatalf("expected 500, got %d", got)
	}
	if body := string(rc.Response.Body()); contains(body, "db down") {
		t.Errorf("不应泄漏底层错误: %s", body)
	}
}

func TestRestart_NoSender500(t *testing.T) {
	fake := &fakeMemberRepo{members: []*repo.Member{{RegID: "n-002", Host: "10.0.0.2", Port: 8080}}}
	rc := serveRestart(NewClusterHandler(fake, fixedSelfID("n-001"), nil, true, logger.NewNop()), "n-002")
	if got := rc.Response.StatusCode(); got != 500 {
		t.Fatalf("expected 500, got %d", got)
	}
}

func TestRestart_NilRepo404(t *testing.T) {
	rc := serveRestart(NewClusterHandler(nil, nil, &fakeSender{}, true, logger.NewNop()), "n-001")
	if got := rc.Response.StatusCode(); got != 404 {
		t.Fatalf("expected 404, got %d", got)
	}
}

func TestRestart_RepoError500(t *testing.T) {
	fake := &fakeMemberRepo{err: errors.New("db connection refused")}
	s := &fakeSender{}
	rc := serveRestart(NewClusterHandler(fake, fixedSelfID("n-001"), s, true, logger.NewNop()), "n-002")
	if got := rc.Response.StatusCode(); got != 500 {
		t.Fatalf("expected 500, got %d body=%s", got, rc.Response.Body())
	}
	if body := string(rc.Response.Body()); contains(body, "db connection refused") {
		t.Errorf("不应泄漏底层错误: %s", body)
	}
	if len(s.sent) != 0 {
		t.Errorf("查询失败不应发送消息，sent=%+v", s.sent)
	}
}

func TestRestart_NoWebUserIDYieldsEmptyRequestedBy(t *testing.T) {
	fake := &fakeMemberRepo{members: []*repo.Member{{RegID: "n-002", Host: "10.0.0.2", Port: 8080}}}
	s := &fakeSender{}
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodPost)
	rc.Params = param.Params{{Key: "reg_id", Value: "n-002"}}
	NewClusterHandler(fake, fixedSelfID("n-001"), s, true, logger.NewNop()).Restart(context.Background(), rc)
	if got := rc.Response.StatusCode(); got != 202 {
		t.Fatalf("expected 202, got %d", got)
	}
	if len(s.sent) != 1 || s.sent[0].Payload["requested_by"] != "" {
		t.Errorf("未注入 web_user_id 时 requested_by 应为空串，sent=%+v", s.sent)
	}
}
