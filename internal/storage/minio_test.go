// internal/storage/minio_test.go
package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
)

// fakeMinioClient 实现 minioAPI 接口，用于单元测试。
type fakeMinioClient struct {
	objects map[string][]byte // key -> body
	stats   map[string]minio.ObjectInfo

	bucketExists    bool
	bucketExistsErr error

	putErr           error
	getErr           error
	statErr          error
	removeErr        error
	listErr          error
	recursiveListErr error
	copyErr          error
}

func newFakeClient() *fakeMinioClient {
	return &fakeMinioClient{
		objects:      map[string][]byte{},
		stats:        map[string]minio.ObjectInfo{},
		bucketExists: true,
	}
}

func (f *fakeMinioClient) BucketExists(ctx context.Context, bucket string) (bool, error) {
	if f.bucketExistsErr != nil {
		return false, f.bucketExistsErr
	}
	return f.bucketExists, nil
}

func (f *fakeMinioClient) PutObject(ctx context.Context, bucket, key string, r io.Reader, size int64, opts minio.PutObjectOptions) error {
	if f.putErr != nil {
		return f.putErr
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.objects[key] = body
	f.stats[key] = minio.ObjectInfo{
		Key:          key,
		Size:         int64(len(body)),
		ContentType:  opts.ContentType,
		LastModified: time.Unix(1700000000, 0),
	}
	return nil
}

func (f *fakeMinioClient) GetObject(ctx context.Context, bucket, key string, opts minio.GetObjectOptions) (io.ReadCloser, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	body, ok := f.objects[key]
	if !ok {
		return nil, minio.ErrorResponse{Code: "NoSuchKey"}
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

func (f *fakeMinioClient) StatObject(ctx context.Context, bucket, key string, opts minio.StatObjectOptions) (minio.ObjectInfo, error) {
	if f.statErr != nil {
		return minio.ObjectInfo{}, f.statErr
	}
	info, ok := f.stats[key]
	if !ok {
		return minio.ObjectInfo{}, minio.ErrorResponse{Code: "NoSuchKey"}
	}
	return info, nil
}

func (f *fakeMinioClient) RemoveObject(ctx context.Context, bucket, key string, opts minio.RemoveObjectOptions) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	if _, ok := f.objects[key]; !ok {
		return minio.ErrorResponse{Code: "NoSuchKey"}
	}
	delete(f.objects, key)
	delete(f.stats, key)
	return nil
}

func (f *fakeMinioClient) ListObjects(ctx context.Context, bucket string, opts minio.ListObjectsOptions) <-chan minio.ObjectInfo {
	ch := make(chan minio.ObjectInfo, len(f.objects)+1)
	if f.listErr != nil {
		ch <- minio.ObjectInfo{Err: f.listErr}
		close(ch)
		return ch
	}

	// 模拟 delimiter "/" 行为：Recursive=false 时把 prefix 之下的对象按第一段切分
	seen := map[string]struct{}{}
	count := 0
	for key, info := range f.stats {
		if !strings.HasPrefix(key, opts.Prefix) {
			continue
		}
		if opts.MaxKeys > 0 && count >= opts.MaxKeys {
			break
		}

		out := info
		if !opts.Recursive {
			// 把 prefix 之后的部分按 "/" 切分，第一段如果还有 "/" 说明是子目录
			rest := key[len(opts.Prefix):]
			if idx := strings.Index(rest, "/"); idx >= 0 {
				// 子目录：返回 CommonPrefix 形式（key=prefix+seg+"/"，无元数据）
				dirKey := opts.Prefix + rest[:idx+1]
				if _, dup := seen[dirKey]; dup {
					continue
				}
				seen[dirKey] = struct{}{}
				out = minio.ObjectInfo{Key: dirKey}
			}
		}
		ch <- out
		count++
	}
	close(ch)
	return ch
}

func (f *fakeMinioClient) RemoveObjectsRecursive(ctx context.Context, bucket, prefix string) error {
	if f.recursiveListErr != nil {
		return f.recursiveListErr
	}
	if f.removeErr != nil {
		return f.removeErr
	}
	for key := range f.objects {
		if strings.HasPrefix(key, prefix) {
			delete(f.objects, key)
			delete(f.stats, key)
		}
	}
	return nil
}

func (f *fakeMinioClient) CopyObject(ctx context.Context, dstBucket, dstKey, srcBucket, srcKey string) error {
	if f.copyErr != nil {
		return f.copyErr
	}
	body, ok := f.objects[srcKey]
	if !ok {
		return minio.ErrorResponse{Code: "NoSuchKey"}
	}
	info := f.stats[srcKey]
	info.Key = dstKey
	f.objects[dstKey] = append([]byte(nil), body...)
	f.stats[dstKey] = info
	return nil
}

// fakeRollbackClient 专门用于测试 Rename 的 step-2 / step-4 中 RemoveObject 失败的场景。
// failKeys 中列出的 key 调用 RemoveObject 时会返回错误。
type fakeRollbackClient struct {
	*fakeMinioClient
	failKeys map[string]bool
}

func (f *fakeRollbackClient) RemoveObject(ctx context.Context, bucket, key string, opts minio.RemoveObjectOptions) error {
	if f.failKeys[key] {
		return errors.New("simulated remove failure")
	}
	return f.fakeMinioClient.RemoveObject(ctx, bucket, key, opts)
}

func newMinioForTest(c minioAPI) *Minio {
	return &Minio{client: c, bucket: "groot"}
}

func TestMinio_WriteReadCycle(t *testing.T) {
	fc := newFakeClient()
	ms := newMinioForTest(fc)
	ctx := context.Background()
	want := []byte("payload")

	if err := ms.Write(ctx, "sessions/abc/x.txt", bytes.NewReader(want), int64(len(want)), "text/plain"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	rc, err := ms.Read(ctx, "sessions/abc/x.txt")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestMinio_StatNotFound(t *testing.T) {
	ms := newMinioForTest(newFakeClient())
	_, err := ms.Stat(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMinio_ReadNotFound(t *testing.T) {
	ms := newMinioForTest(newFakeClient())
	_, err := ms.Read(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMinio_DeleteNotFound(t *testing.T) {
	ms := newMinioForTest(newFakeClient())
	err := ms.Delete(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMinio_DeleteDirRemovesPrefix(t *testing.T) {
	fc := newFakeClient()
	ms := newMinioForTest(fc)
	ctx := context.Background()

	for _, k := range []string{"sessions/abc/a", "sessions/abc/sub/b", "sessions/other/c"} {
		_ = ms.Write(ctx, k, strings.NewReader("x"), 1, "")
	}

	if err := ms.DeleteDir(ctx, "sessions/abc"); err != nil {
		t.Fatalf("DeleteDir: %v", err)
	}
	if _, ok := fc.objects["sessions/abc/a"]; ok {
		t.Fatal("sessions/abc/a should have been removed")
	}
	if _, ok := fc.objects["sessions/other/c"]; !ok {
		t.Fatal("sessions/other/c should remain")
	}
}

func TestMinio_ListByPrefix(t *testing.T) {
	fc := newFakeClient()
	ms := newMinioForTest(fc)
	ctx := context.Background()

	for _, k := range []string{"sessions/abc/a", "sessions/abc/b", "sessions/other/c"} {
		_ = ms.Write(ctx, k, strings.NewReader("x"), 1, "")
	}

	infos, err := ms.List(ctx, "sessions/abc")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(infos))
	}
}

func TestMinio_DeleteDirReturnsListError(t *testing.T) {
	fc := newFakeClient()
	fc.recursiveListErr = errors.New("simulated list failure")
	ms := newMinioForTest(fc)
	ctx := context.Background()
	err := ms.DeleteDir(ctx, "sessions/abc")
	if err == nil {
		t.Fatal("expected DeleteDir to return error when list fails, got nil")
	}
	if !strings.Contains(err.Error(), "simulated list failure") {
		t.Fatalf("expected error to wrap list failure, got: %v", err)
	}
}

func TestMinio_StatReturnsIsDirForPrefix(t *testing.T) {
	fc := newFakeClient()
	ms := newMinioForTest(fc)
	ctx := context.Background()

	// 没有名为 "sessions/abc" 的对象，但有 "sessions/abc/x"
	if err := ms.Write(ctx, "sessions/abc/x", strings.NewReader("y"), 1, ""); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, err := ms.Stat(ctx, "sessions/abc")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir {
		t.Fatal("expected IsDir=true for prefix")
	}
}

func TestMinio_ListNonRecursiveSeparatesDirs(t *testing.T) {
	fc := newFakeClient()
	ms := newMinioForTest(fc)
	ctx := context.Background()

	for _, k := range []string{"sessions/abc/a", "sessions/abc/sub/x", "sessions/abc/sub/y"} {
		_ = ms.Write(ctx, k, strings.NewReader("z"), 1, "")
	}

	infos, err := ms.List(ctx, "sessions/abc")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// 期望：a (file) + sub/ (dir)，共 2 个；不应该展开 sub 下的 x、y
	if len(infos) != 2 {
		t.Fatalf("expected 2 entries (1 file + 1 dir), got %d", len(infos))
	}

	var sawFile, sawDir bool
	for _, fi := range infos {
		if !fi.IsDir && fi.Path == "sessions/abc/a" {
			sawFile = true
		}
		if fi.IsDir && fi.Path == "sessions/abc/sub/" {
			sawDir = true
		}
	}
	if !sawFile {
		t.Error("expected to see file 'sessions/abc/a'")
	}
	if !sawDir {
		t.Error("expected to see dir 'sessions/abc/sub/'")
	}
}

func TestMinio_RenameSuccess(t *testing.T) {
	fc := newFakeClient()
	ms := newMinioForTest(fc)
	ctx := context.Background()
	_ = ms.Write(ctx, "a/x.txt", strings.NewReader("hello"), 5, "")

	if err := ms.Rename(ctx, "a/x.txt", "b/y.txt"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, ok := fc.objects["a/x.txt"]; ok {
		t.Error("src should be deleted")
	}
	if got := fc.objects["b/y.txt"]; string(got) != "hello" {
		t.Errorf("dst content = %q, want hello", got)
	}
}

func TestMinio_RenameSrcNotFound(t *testing.T) {
	ms := newMinioForTest(newFakeClient())
	err := ms.Rename(context.Background(), "missing", "dst")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestMinio_RenameOverwritesStaleDst(t *testing.T) {
	fc := newFakeClient()
	ms := newMinioForTest(fc)
	ctx := context.Background()
	_ = ms.Write(ctx, "src", strings.NewReader("new"), 3, "")
	_ = ms.Write(ctx, "dst", strings.NewReader("stale"), 5, "")

	if err := ms.Rename(ctx, "src", "dst"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if got := fc.objects["dst"]; string(got) != "new" {
		t.Errorf("dst = %q, want new (stale should have been cleaned)", got)
	}
	if _, ok := fc.objects["src"]; ok {
		t.Error("src should be deleted")
	}
}

func TestMinio_RenameCopyFailureLeavesSrc(t *testing.T) {
	fc := newFakeClient()
	fc.copyErr = errors.New("simulated copy failure")
	ms := newMinioForTest(fc)
	ctx := context.Background()
	_ = ms.Write(ctx, "src", strings.NewReader("data"), 4, "")

	err := ms.Rename(ctx, "src", "dst")
	if err == nil {
		t.Fatal("expected error from copy failure")
	}
	// src 必须原封不动
	if got := fc.objects["src"]; string(got) != "data" {
		t.Errorf("src should be intact, got %q", got)
	}
	// dst 不应存在
	if _, ok := fc.objects["dst"]; ok {
		t.Error("dst should not exist when copy failed")
	}
}

func TestMinio_RenameDeleteFailureRollsBackDst(t *testing.T) {
	// 模拟 Copy 成功、Delete src 失败 → 应回滚删 dst
	fc := &fakeRollbackClient{
		fakeMinioClient: newFakeClient(),
		failKeys:        map[string]bool{"src": true}, // src 删除失败，dst 删除（回滚）成功
	}
	ms := newMinioForTest(fc)
	ctx := context.Background()
	_ = ms.Write(ctx, "src", strings.NewReader("data"), 4, "")

	err := ms.Rename(ctx, "src", "dst")
	if err == nil {
		t.Fatal("expected error from delete failure")
	}
	// src 应该还在（因为 delete 失败了）
	if _, ok := fc.objects["src"]; !ok {
		t.Error("src should still exist (delete failed)")
	}
	// dst 应该被回滚删除
	if _, ok := fc.objects["dst"]; ok {
		t.Error("dst should have been rolled back")
	}
}

func TestMinio_RenameStaleDstCleanupFailure(t *testing.T) {
	fc := &fakeRollbackClient{
		fakeMinioClient: newFakeClient(),
		failKeys:        map[string]bool{"dst": true}, // 步骤 2 删 dst 失败
	}
	ms := newMinioForTest(fc)
	ctx := context.Background()
	_ = ms.Write(ctx, "src", strings.NewReader("new"), 3, "")
	_ = ms.Write(ctx, "dst", strings.NewReader("stale"), 5, "")

	err := ms.Rename(ctx, "src", "dst")
	if err == nil {
		t.Fatal("expected error from cleanup-dst failure")
	}
	// src 必须原封不动（步骤 2 失败时未到 CopyObject）
	if got := fc.objects["src"]; string(got) != "new" {
		t.Errorf("src should be intact, got %q", got)
	}
	// dst 仍是旧的（清理失败）
	if got := fc.objects["dst"]; string(got) != "stale" {
		t.Errorf("dst should remain stale (cleanup failed), got %q", got)
	}
}

func TestMinio_RenameDeleteFailureAndRollbackFailure(t *testing.T) {
	// 步骤 4 删 src 失败 + 回滚（删 dst）也失败 → 最坏情况：src 与 dst 各一份
	fc := &fakeRollbackClient{
		fakeMinioClient: newFakeClient(),
		failKeys:        map[string]bool{"src": true, "dst": true},
	}
	ms := newMinioForTest(fc)
	ctx := context.Background()
	_ = ms.Write(ctx, "src", strings.NewReader("data"), 4, "")

	err := ms.Rename(ctx, "src", "dst")
	if err == nil {
		t.Fatal("expected error from delete src failure")
	}
	// 关键断言：返回的错误应该包装的是 src 删除失败（不是回滚失败）
	if !strings.Contains(err.Error(), "delete src") {
		t.Errorf("expected error to wrap 'delete src' failure, got: %v", err)
	}
	// src 仍存在（删失败）
	if _, ok := fc.objects["src"]; !ok {
		t.Error("src should still exist (delete failed)")
	}
	// dst 也仍存在（回滚失败）— 这是最坏情况
	if _, ok := fc.objects["dst"]; !ok {
		t.Error("dst should still exist (rollback also failed)")
	}
}

// ===== 目录级 Rename 测试 =====

func TestMinio_RenameDir_Success(t *testing.T) {
	fc := newFakeClient()
	ms := newMinioForTest(fc)
	ctx := context.Background()

	for _, k := range []string{"a/x.txt", "a/sub/y.txt", "a/sub/z.txt"} {
		_ = ms.Write(ctx, k, strings.NewReader("data:"+k), -1, "")
	}
	// 旁路对象不应被影响
	_ = ms.Write(ctx, "other/p.txt", strings.NewReader("p"), 1, "")

	if err := ms.Rename(ctx, "a", "b"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	// 所有 a/ 下的对象应迁移到 b/
	for _, k := range []string{"a/x.txt", "a/sub/y.txt", "a/sub/z.txt"} {
		if _, ok := fc.objects[k]; ok {
			t.Errorf("%s should be removed from src", k)
		}
	}
	for _, k := range []string{"b/x.txt", "b/sub/y.txt", "b/sub/z.txt"} {
		if _, ok := fc.objects[k]; !ok {
			t.Errorf("%s should exist at dst", k)
		}
	}
	// 旁路对象不变
	if _, ok := fc.objects["other/p.txt"]; !ok {
		t.Error("other/p.txt should be untouched")
	}
}

func TestMinio_RenameDir_SrcNotFoundNoObjectNoPrefix(t *testing.T) {
	fc := newFakeClient()
	ms := newMinioForTest(fc)
	err := ms.Rename(context.Background(), "ghost", "dst")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestMinio_RenameDir_PhaseACopyFailure_RollsBackDst(t *testing.T) {
	// 全部 CopyObject 失败时（copyErr 一旦置位影响所有 Copy），
	// 已 Copy 的子对象应被回滚清空，src 必须完整。
	fc := newFakeClient()
	ms := newMinioForTest(fc)
	ctx := context.Background()
	for _, k := range []string{"a/x.txt", "a/y.txt"} {
		_ = ms.Write(ctx, k, strings.NewReader("d"), 1, "")
	}
	// 第一次 Copy 就失败
	fc.copyErr = errors.New("simulated copy failure")

	if err := ms.Rename(ctx, "a", "b"); err == nil {
		t.Fatal("expected error from Phase A copy failure")
	}
	// src 完整
	for _, k := range []string{"a/x.txt", "a/y.txt"} {
		if _, ok := fc.objects[k]; !ok {
			t.Errorf("src %s should be intact", k)
		}
	}
	// dst 应该没有任何对象（首个 Copy 就失败，没有需要回滚的）
	for k := range fc.objects {
		if strings.HasPrefix(k, "b/") {
			t.Errorf("dst should be empty after Phase A failure, found %s", k)
		}
	}
}

// partialCopyClient 让指定 key 的 CopyObject 失败，其他 key 正常成功。
// 用于测试 Phase A 中后续 Copy 失败 → 已 Copy 的对象需回滚。
type partialCopyClient struct {
	*fakeMinioClient
	failCopyToKey map[string]bool
}

func (p *partialCopyClient) CopyObject(ctx context.Context, dstBucket, dstKey, srcBucket, srcKey string) error {
	if p.failCopyToKey[dstKey] {
		return errors.New("simulated copy failure for " + dstKey)
	}
	return p.fakeMinioClient.CopyObject(ctx, dstBucket, dstKey, srcBucket, srcKey)
}

func TestMinio_RenameDir_PhaseAPartialFailure_RollsBackCopied(t *testing.T) {
	// Phase A 中第二个对象 Copy 失败 → 第一个已 Copy 的 dst 子对象应被回滚
	fc := &partialCopyClient{
		fakeMinioClient: newFakeClient(),
		failCopyToKey:   map[string]bool{"b/y.txt": true},
	}
	ms := newMinioForTest(fc)
	ctx := context.Background()

	// 写入两个对象（fakeMinioClient.objects 是 map，迭代顺序不保证，
	// 但只要任一对象失败，回滚都应清空 dst）
	for _, k := range []string{"a/x.txt", "a/y.txt"} {
		_ = ms.Write(ctx, k, strings.NewReader("d"), 1, "")
	}

	if err := ms.Rename(ctx, "a", "b"); err == nil {
		t.Fatal("expected error from partial Phase A failure")
	}
	// src 完整
	for _, k := range []string{"a/x.txt", "a/y.txt"} {
		if _, ok := fc.objects[k]; !ok {
			t.Errorf("src %s should be intact", k)
		}
	}
	// dst 不应有任何已迁移对象（要么没 Copy 成功，要么被回滚清掉）
	for k := range fc.objects {
		if strings.HasPrefix(k, "b/") {
			t.Errorf("dst should be empty after Phase A rollback, found %s", k)
		}
	}
}

func TestMinio_RenameDir_PhaseBDeleteFailure_DstAuthoritative(t *testing.T) {
	// Phase B 删 src 失败 → 不回滚 dst（dst 已是权威完整副本）
	fc := &fakeRollbackClient{
		fakeMinioClient: newFakeClient(),
		failKeys:        map[string]bool{"a/y.txt": true}, // 删第二个 src 对象失败
	}
	ms := newMinioForTest(fc)
	ctx := context.Background()
	for _, k := range []string{"a/x.txt", "a/y.txt"} {
		_ = ms.Write(ctx, k, strings.NewReader("d"), 1, "")
	}

	err := ms.Rename(ctx, "a", "b")
	if err == nil {
		t.Fatal("expected Phase B error")
	}
	if !strings.Contains(err.Error(), "delete src") {
		t.Errorf("expected error to wrap 'delete src', got: %v", err)
	}
	// dst 必须完整存在（这是收敛原则的核心）
	for _, k := range []string{"b/x.txt", "b/y.txt"} {
		if _, ok := fc.objects[k]; !ok {
			t.Errorf("dst %s should exist (Phase A succeeded, Phase B does not roll back)", k)
		}
	}
	// src 中删除失败的对象仍存在（待业务层幂等清理）
	if _, ok := fc.objects["a/y.txt"]; !ok {
		t.Error("a/y.txt should remain (delete failed) — business layer cleanup needed")
	}
}

func TestMinio_RenameDir_DstHasStalePrefix_GetsCleaned(t *testing.T) {
	// dst 处已存在残留前缀（上次崩溃留下），Phase 0 应彻底清掉
	fc := newFakeClient()
	ms := newMinioForTest(fc)
	ctx := context.Background()
	_ = ms.Write(ctx, "a/x.txt", strings.NewReader("new"), 3, "")
	// dst 处的脏数据
	_ = ms.Write(ctx, "b/stale.txt", strings.NewReader("stale"), 5, "")

	if err := ms.Rename(ctx, "a", "b"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, ok := fc.objects["b/stale.txt"]; ok {
		t.Error("dst stale prefix should have been cleaned")
	}
	if got := fc.objects["b/x.txt"]; string(got) != "new" {
		t.Errorf("dst should have new content, got %q", got)
	}
}

// ===== 启动 fail-fast 探活测试 =====

func TestNewMinio_FailFast_BucketNotExist(t *testing.T) {
	fc := newFakeClient()
	fc.bucketExists = false

	_, err := newMinioWithClient(fc, "missing-bucket")
	if err == nil {
		t.Fatal("expected error when bucket does not exist, got nil")
	}
	if !strings.Contains(err.Error(), "bucket") {
		t.Errorf("expected error to mention 'bucket', got: %v", err)
	}
	// 探针不应该被写入（Step 1 失败时不应继续到 Step 2）
	for k := range fc.objects {
		if strings.HasPrefix(k, "__startup/probe-") {
			t.Errorf("probe key %s should not have been written when bucket check failed", k)
		}
	}
}

func TestNewMinio_FailFast_PutFails(t *testing.T) {
	fc := newFakeClient()
	fc.bucketExists = true
	fc.putErr = errors.New("permission denied")

	_, err := newMinioWithClient(fc, "groot")
	if err == nil {
		t.Fatal("expected error when probe put fails, got nil")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("expected error to wrap put failure, got: %v", err)
	}
	if !strings.Contains(err.Error(), "probe put") {
		t.Errorf("expected error to mention 'probe put', got: %v", err)
	}
}

func TestNewMinio_FailFast_OK(t *testing.T) {
	fc := newFakeClient()
	fc.bucketExists = true

	ms, err := newMinioWithClient(fc, "groot")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if ms == nil {
		t.Fatal("expected non-nil *Minio")
	}
	if ms.bucket != "groot" {
		t.Errorf("bucket = %q, want groot", ms.bucket)
	}
	// 探针写入后必须被删除：fc.objects 不应含任何 __startup/probe-* key
	for k := range fc.objects {
		if strings.HasPrefix(k, "__startup/probe-") {
			t.Errorf("probe key %s should have been removed after successful probe", k)
		}
	}
}

// TestNewMinio_FailFast_BucketExistsErr 覆盖 Step 1 中 BucketExists RPC 本身报错的失败路径
// （与"返回 false"是不同的失败语义：网络不通/权限拒绝等）。
func TestNewMinio_FailFast_BucketExistsErr(t *testing.T) {
	fc := newFakeClient()
	underlying := errors.New("network unreachable")
	fc.bucketExistsErr = underlying

	_, err := newMinioWithClient(fc, "groot")
	if err == nil {
		t.Fatal("expected error when BucketExists returns error, got nil")
	}
	// 错误必须 wrap 底层 error，使调用方能 errors.Is 还原根因
	if !errors.Is(err, underlying) {
		t.Errorf("expected error to wrap underlying network error (errors.Is), got: %v", err)
	}
	// 错误信息也应包含底层错误文本
	if !strings.Contains(err.Error(), "network unreachable") {
		t.Errorf("expected error to contain 'network unreachable', got: %v", err)
	}
	// Step 1 失败时绝不应继续到 Step 2 写探针
	for k := range fc.objects {
		if strings.HasPrefix(k, "__startup/probe-") {
			t.Errorf("probe key %s should not have been written when BucketExists errored", k)
		}
	}
}

// TestNewMinio_FailFast_RemoveFails 覆盖 Step 3 探针删除失败路径
// （对应"账号有 PutObject 但没 DeleteObject 权限"的真实场景）。
func TestNewMinio_FailFast_RemoveFails(t *testing.T) {
	fc := newFakeClient()
	fc.bucketExists = true
	fc.removeErr = errors.New("access denied")

	_, err := newMinioWithClient(fc, "groot")
	if err == nil {
		t.Fatal("expected error when probe remove fails, got nil")
	}
	// 关键断言：错误信息必须包含 "probe remove"，证明确实走到了 Step 3
	// （否则可能是 Step 1/2 提前失败，未真正校验删权限）
	if !strings.Contains(err.Error(), "probe remove") {
		t.Errorf("expected error to mention 'probe remove' (Step 3), got: %v", err)
	}
	// 也应包含底层错误文本
	if !strings.Contains(err.Error(), "access denied") {
		t.Errorf("expected error to wrap 'access denied', got: %v", err)
	}
	// Step 2 写入的探针因 Step 3 失败而残留 —— 这是已知代价（fail-fast 优先）
	// 此处不强制断言探针是否仍在，只确认错误正确报出删权限问题。
}
