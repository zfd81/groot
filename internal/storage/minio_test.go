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

	putErr    error
	getErr    error
	statErr   error
	removeErr error
	listErr   error
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
	for key, info := range f.stats {
		if !strings.HasPrefix(key, opts.Prefix) {
			continue
		}
		ch <- info
	}
	close(ch)
	return ch
}

func (f *fakeMinioClient) RemoveObjectsRecursive(ctx context.Context, bucket, prefix string) error {
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
