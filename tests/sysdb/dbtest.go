// Package main runs system tests against MySQL/PostgreSQL/SQLite.
// Run with:
//   go run tests/sysdb/dbtest.go sqlite
//   go run tests/sysdb/dbtest.go mysql
//   go run tests/sysdb/dbtest.go postgres
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/repofactory"
	"github.com/zfd81/groot/internal/schedule"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: go run dbtest.go [sqlite|mysql|postgres]")
		os.Exit(1)
	}

	driver := os.Args[1]
	var cfg *config.DatabaseConfig
	homeDir := "/tmp/groot-sysdb-test"

	switch driver {
	case "sqlite":
		cfg = nil
		os.RemoveAll(homeDir)
		os.MkdirAll(homeDir, 0755)
	case "mysql":
		cfg = &config.DatabaseConfig{
			Driver: "mysql",
			DSN:    "zfd:12345678@tcp(localhost:3306)/groot?charset=utf8mb4&parseTime=True&loc=UTC",
		}
	case "postgres":
		cfg = &config.DatabaseConfig{
			Driver: "postgres",
			DSN:    "host=localhost port=5432 user=zfd password=12345678 dbname=groot sslmode=disable",
		}
	default:
		fmt.Printf("unknown driver: %s\n", driver)
		os.Exit(1)
	}

	fmt.Printf("=== Testing %s ===\n", driver)

	sqlxDB, dialect, err := db.Open(cfg, homeDir)
	if err != nil {
		fail("Open DB", err)
	}
	defer sqlxDB.Close()
	pass("Open + Migrate (dialect=%d)", dialect)

	if driver == "mysql" || driver == "postgres" {
		tables := []string{
			"shared_resources", "memory_chats", "memory_sessions",
			"schedule_executions", "schedule_tasks", "cluster_members",
		}
		for _, t := range tables {
			sqlxDB.Exec("DELETE FROM " + t)
		}
		pass("Cleanup pre-existing data")
	}

	repos := repofactory.NewRepos(sqlxDB, dialect, homeDir)

	testMember(repos.Member)
	testSchedule(repos.Schedule)
	testMemory(repos.Memory)
	testResource(repos.Resource, driver)

	fmt.Printf("=== %s: ALL PASSED ===\n", driver)
}

func testMember(r repo.MemberRepo) {
	ctx := context.Background()

	regID := fmt.Sprintf("99991231%09d", os.Getpid())
	m := &repo.Member{
		RegID: regID, Role: "follower",
		Host: "127.0.0.1", Port: 8080, Pid: os.Getpid(),
		HeartbeatAt: time.Now(), CreatedAt: time.Now(),
	}

	if err := r.Register(ctx, m); err != nil {
		fail("Member.Register", err)
	}
	pass("Member.Register")

	got, err := r.Get(ctx, regID)
	if err != nil || got.Role != "follower" {
		fail("Member.Get", fmt.Errorf("got=%v err=%v", got, err))
	}
	pass("Member.Get")

	if err := r.Heartbeat(ctx, regID); err != nil {
		fail("Member.Heartbeat", err)
	}
	pass("Member.Heartbeat")

	if err := r.UpdateRole(ctx, regID, "leader"); err != nil {
		fail("Member.UpdateRole", err)
	}
	got2, _ := r.Get(ctx, regID)
	if got2.Role != "leader" {
		fail("Member.UpdateRole verify", fmt.Errorf("expected leader, got %s", got2.Role))
	}
	pass("Member.UpdateRole")

	all, err := r.ListAll(ctx)
	if err != nil || len(all) < 1 {
		fail("Member.ListAll", fmt.Errorf("got %d members err=%v", len(all), err))
	}
	pass("Member.ListAll (%d members)", len(all))

	if err := r.Remove(ctx, regID); err != nil {
		fail("Member.Remove", err)
	}
	pass("Member.Remove")
}

func testSchedule(r schedule.ScheduleRepo) {
	ctx := context.Background()

	taskID := fmt.Sprintf("sysdb-task-%d", os.Getpid())
	task := &schedule.Task{
		ID: taskID, Name: "system test", Schedule: "0 0 * * *",
		Status: "active",
	}
	if err := r.SaveTask(ctx, task); err != nil {
		fail("Schedule.SaveTask", err)
	}
	pass("Schedule.SaveTask")

	loaded, err := r.LoadTask(ctx, taskID)
	if err != nil || loaded.Name != "system test" {
		fail("Schedule.LoadTask", fmt.Errorf("got=%v err=%v", loaded, err))
	}
	pass("Schedule.LoadTask")

	tasks, err := r.ListByStatus(ctx, "active")
	if err != nil || len(tasks) < 1 {
		fail("Schedule.ListByStatus", err)
	}
	pass("Schedule.ListByStatus (%d)", len(tasks))

	if err := r.MoveStatus(ctx, taskID, "disabled", loaded.Version); err != nil {
		fail("Schedule.MoveStatus", err)
	}
	pass("Schedule.MoveStatus")

	now := time.Now()
	fin := now.Add(time.Second)
	rec := &schedule.ExecutionRecord{
		ExecutionID: fmt.Sprintf("exec-%d", os.Getpid()),
		TaskID:      taskID,
		StartedAt:   now,
		FinishedAt:  &fin,
		Status:      "running",
	}
	if err := r.SaveExecution(ctx, rec); err != nil {
		fail("Schedule.SaveExecution", err)
	}
	pass("Schedule.SaveExecution")

	loaded2, _ := r.LoadTask(ctx, taskID)
	rec.Status = "success"
	if err := r.CompleteExecution(ctx, rec, now.Add(time.Hour), now, loaded2.Version); err != nil {
		fail("Schedule.CompleteExecution", err)
	}
	pass("Schedule.CompleteExecution")

	execs, err := r.ListExecutions(ctx, taskID, 10)
	if err != nil || len(execs) != 1 {
		fail("Schedule.ListExecutions", fmt.Errorf("got %d err=%v", len(execs), err))
	}
	pass("Schedule.ListExecutions")

	if err := r.DeleteTask(ctx, taskID); err != nil {
		fail("Schedule.DeleteTask", err)
	}
	pass("Schedule.DeleteTask")
}

func testMemory(r repo.MemoryRepo) {
	ctx := context.Background()

	sessID := fmt.Sprintf("sysdb-sess-%d", os.Getpid())
	s := &repo.Session{SessionID: sessID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := r.CreateSession(ctx, s); err != nil {
		fail("Memory.CreateSession", err)
	}
	pass("Memory.CreateSession")

	exists, err := r.ExistsSession(ctx, sessID)
	if err != nil || !exists {
		fail("Memory.ExistsSession", err)
	}
	pass("Memory.ExistsSession")

	chatID1 := fmt.Sprintf("sysdb-chat-%d-1", os.Getpid())
	rec1 := &memory.ChatRecord{
		ChatID: chatID1, SessionID: sessID,
		Instruction: "Hello, what's 2+2?",
		Result:      "2+2=4",
		Status:      "success",
		StartedAt:   time.Now(),
	}
	if err := r.SaveChat(ctx, rec1); err != nil {
		fail("Memory.SaveChat (1st)", err)
	}
	pass("Memory.SaveChat (1st)")

	chatID2 := fmt.Sprintf("sysdb-chat-%d-2", os.Getpid())
	rec2 := &memory.ChatRecord{
		ChatID: chatID2, SessionID: sessID,
		Instruction: "What about 3+3?",
		Result:      "3+3=6",
		Status:      "success",
		StartedAt:   time.Now(),
	}
	if err := r.SaveChat(ctx, rec2); err != nil {
		fail("Memory.SaveChat (2nd)", err)
	}
	pass("Memory.SaveChat (2nd)")

	sess, _ := r.GetSession(ctx, sessID)
	if sess.Round != 2 {
		fail("Memory.SaveChat round increment", fmt.Errorf("expected round=2, got %d", sess.Round))
	}
	pass("Memory round increment")

	history, err := r.LoadHistory(ctx, sessID)
	if err != nil || len(history) != 2 {
		fail("Memory.LoadHistory", fmt.Errorf("got %d err=%v", len(history), err))
	}
	if history[0].Round != 1 || history[1].Round != 2 {
		fail("Memory.LoadHistory order", fmt.Errorf("rounds: %d, %d", history[0].Round, history[1].Round))
	}
	pass("Memory.LoadHistory (ordered)")

	if err := r.DeleteSession(ctx, sessID); err != nil {
		fail("Memory.DeleteSession", err)
	}
	exists2, _ := r.ExistsSession(ctx, sessID)
	if exists2 {
		fail("Memory.DeleteSession verify", fmt.Errorf("session still exists"))
	}
	pass("Memory.DeleteSession (atomic)")
}

func testResource(r repo.ResourceRepo, driver string) {
	if driver == "sqlite" {
		fmt.Println("[skip] Resource: SQLite mode uses local FS (sync disabled)")
		return
	}
	ctx := context.Background()

	path := fmt.Sprintf("skills/sysdb-%d/SKILL.md", os.Getpid())
	content := []byte("# System Test Skill\nMulti-line\nUTF-8 中文测试")
	res := &repo.Resource{
		Path: path, Content: content,
		ContentType: "text/markdown",
		Size:        int64(len(content)),
		ContentHash: "sha1placeholder",
		UpdatedAt:   time.Now(),
	}
	if err := r.Put(ctx, res); err != nil {
		fail("Resource.Put", err)
	}
	pass("Resource.Put")

	got, err := r.Get(ctx, path)
	if err != nil || string(got.Content) != string(content) {
		fail("Resource.Get", fmt.Errorf("err=%v content match=%v", err, string(got.Content) == string(content)))
	}
	pass("Resource.Get (UTF-8 preserved)")

	st, err := r.Stat(ctx, path)
	if err != nil || st.Size != int64(len(content)) {
		fail("Resource.Stat", err)
	}
	pass("Resource.Stat")

	entries, err := r.List(ctx, "skills/")
	if err != nil || len(entries) < 1 {
		fail("Resource.List", err)
	}
	pass("Resource.List (%d entries)", len(entries))

	binPath := fmt.Sprintf("skills/sysdb-%d/run.bin", os.Getpid())
	binContent := []byte{0x7f, 0x45, 0x4c, 0x46, 0x02, 0x01, 0x01, 0x00, 0xff, 0xfe, 0xfd}
	binRes := &repo.Resource{
		Path: binPath, Content: binContent,
		Size: int64(len(binContent)), ContentHash: "binhash",
		UpdatedAt: time.Now(),
	}
	if err := r.Put(ctx, binRes); err != nil {
		fail("Resource.Put binary", err)
	}
	binGot, _ := r.Get(ctx, binPath)
	if len(binGot.Content) != len(binContent) {
		fail("Resource.Get binary", fmt.Errorf("length mismatch: %d vs %d", len(binGot.Content), len(binContent)))
	}
	for i, b := range binContent {
		if binGot.Content[i] != b {
			fail("Resource binary preservation", fmt.Errorf("byte %d differs: %x vs %x", i, binGot.Content[i], b))
		}
	}
	pass("Resource binary content preserved")

	if err := r.Delete(ctx, path); err != nil {
		fail("Resource.Delete", err)
	}
	if err := r.Delete(ctx, binPath); err != nil {
		fail("Resource.Delete bin", err)
	}
	if err := r.Delete(ctx, "nonexistent"); err != nil {
		fail("Resource.Delete idempotent", err)
	}
	pass("Resource.Delete (+idempotent)")
}

func pass(format string, args ...interface{}) {
	fmt.Printf("  PASS: "+format+"\n", args...)
}

func fail(stage string, err error) {
	fmt.Printf("  FAIL: %s: %v\n", stage, err)
	os.Exit(1)
}
