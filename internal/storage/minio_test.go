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

	putErr           error
	getErr           error
	statErr          error
	removeErr        error
	listErr          error
	recursiveListErr error
}

func newFakeClient() *fakeMinioClient {
	return &fakeMinioClient{
		objects: map[string][]byte{},
		stats:   map[string]minio.ObjectInfo{},
	}
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
