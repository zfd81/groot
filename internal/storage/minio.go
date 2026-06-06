package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// minioAPI 抽象 MinIO 客户端调用，便于单元测试 mock。
type minioAPI interface {
	PutObject(ctx context.Context, bucket, key string, r io.Reader, size int64, opts minio.PutObjectOptions) error
	GetObject(ctx context.Context, bucket, key string, opts minio.GetObjectOptions) (io.ReadCloser, error)
	StatObject(ctx context.Context, bucket, key string, opts minio.StatObjectOptions) (minio.ObjectInfo, error)
	RemoveObject(ctx context.Context, bucket, key string, opts minio.RemoveObjectOptions) error
	ListObjects(ctx context.Context, bucket string, opts minio.ListObjectsOptions) <-chan minio.ObjectInfo
	RemoveObjectsRecursive(ctx context.Context, bucket, prefix string) error
}

// minioClient 是 minioAPI 的真实实现，包装 *minio.Client。
type minioClient struct{ c *minio.Client }

func (m *minioClient) PutObject(ctx context.Context, bucket, key string, r io.Reader, size int64, opts minio.PutObjectOptions) error {
	_, err := m.c.PutObject(ctx, bucket, key, r, size, opts)
	return err
}

func (m *minioClient) GetObject(ctx context.Context, bucket, key string, opts minio.GetObjectOptions) (io.ReadCloser, error) {
	obj, err := m.c.GetObject(ctx, bucket, key, opts)
	if err != nil {
		return nil, err
	}
	return obj, nil
}

func (m *minioClient) StatObject(ctx context.Context, bucket, key string, opts minio.StatObjectOptions) (minio.ObjectInfo, error) {
	return m.c.StatObject(ctx, bucket, key, opts)
}

func (m *minioClient) RemoveObject(ctx context.Context, bucket, key string, opts minio.RemoveObjectOptions) error {
	return m.c.RemoveObject(ctx, bucket, key, opts)
}

func (m *minioClient) ListObjects(ctx context.Context, bucket string, opts minio.ListObjectsOptions) <-chan minio.ObjectInfo {
	return m.c.ListObjects(ctx, bucket, opts)
}

func (m *minioClient) RemoveObjectsRecursive(ctx context.Context, bucket, prefix string) error {
	objCh := make(chan minio.ObjectInfo)
	go func() {
		defer close(objCh)
		for obj := range m.c.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
			if obj.Err != nil {
				continue
			}
			objCh <- obj
		}
	}()
	for rmErr := range m.c.RemoveObjects(ctx, bucket, objCh, minio.RemoveObjectsOptions{}) {
		if rmErr.Err != nil {
			return rmErr.Err
		}
	}
	return nil
}

// Minio 是基于 MinIO 对象存储的 Storage 实现。
// path 直接作为 object key 使用，不做任何前缀处理。
type Minio struct {
	client minioAPI
	bucket string
}

// NewMinio 用提供的 endpoint / 密钥 / bucket 创建一个 minio 存储实例。
func NewMinio(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*Minio, error) {
	c, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: init minio client: %w", err)
	}
	return &Minio{client: &minioClient{c: c}, bucket: bucket}, nil
}

func isNotExist(err error) bool {
	if err == nil {
		return false
	}
	var resp minio.ErrorResponse
	if errors.As(err, &resp) {
		return resp.Code == "NoSuchKey" || resp.Code == "NoSuchBucket"
	}
	return false
}

func (m *Minio) Write(ctx context.Context, path string, r io.Reader, size int64, contentType string) error {
	opts := minio.PutObjectOptions{ContentType: contentType}
	if err := m.client.PutObject(ctx, m.bucket, path, r, size, opts); err != nil {
		return fmt.Errorf("storage: minio put %s: %w", path, err)
	}
	return nil
}

func (m *Minio) Read(ctx context.Context, path string) (io.ReadCloser, error) {
	if _, err := m.client.StatObject(ctx, m.bucket, path, minio.StatObjectOptions{}); err != nil {
		if isNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: minio stat %s: %w", path, err)
	}
	rc, err := m.client.GetObject(ctx, m.bucket, path, minio.GetObjectOptions{})
	if err != nil {
		if isNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: minio get %s: %w", path, err)
	}
	return rc, nil
}

func (m *Minio) Delete(ctx context.Context, path string) error {
	if _, err := m.client.StatObject(ctx, m.bucket, path, minio.StatObjectOptions{}); err != nil {
		if isNotExist(err) {
			return ErrNotFound
		}
		return fmt.Errorf("storage: minio stat %s: %w", path, err)
	}
	if err := m.client.RemoveObject(ctx, m.bucket, path, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("storage: minio remove %s: %w", path, err)
	}
	return nil
}

func (m *Minio) DeleteDir(ctx context.Context, path string) error {
	prefix := strings.TrimSuffix(path, "/") + "/"
	if err := m.client.RemoveObjectsRecursive(ctx, m.bucket, prefix); err != nil {
		return fmt.Errorf("storage: minio remove dir %s: %w", path, err)
	}
	return nil
}

func (m *Minio) Stat(ctx context.Context, path string) (*FileInfo, error) {
	info, err := m.client.StatObject(ctx, m.bucket, path, minio.StatObjectOptions{})
	if err != nil {
		if isNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: minio stat %s: %w", path, err)
	}
	return objectInfoToFileInfo(info, false), nil
}

func (m *Minio) List(ctx context.Context, dir string) ([]*FileInfo, error) {
	prefix := strings.TrimSuffix(dir, "/") + "/"
	out := make([]*FileInfo, 0)
	for obj := range m.client.ListObjects(ctx, m.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: false}) {
		if obj.Err != nil {
			return nil, fmt.Errorf("storage: minio list %s: %w", dir, obj.Err)
		}
		isDir := strings.HasSuffix(obj.Key, "/")
		out = append(out, objectInfoToFileInfo(obj, isDir))
	}
	return out, nil
}

func objectInfoToFileInfo(info minio.ObjectInfo, isDir bool) *FileInfo {
	mod := info.LastModified
	if mod.IsZero() {
		mod = time.Time{}
	}
	return &FileInfo{
		Path:        info.Key,
		Size:        info.Size,
		ContentType: info.ContentType,
		ModTime:     mod,
		IsDir:       isDir,
	}
}
