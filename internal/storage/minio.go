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
	var listErr error
	go func() {
		defer close(objCh)
		for obj := range m.c.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
			if obj.Err != nil {
				listErr = obj.Err
				return // 提前返回，close(objCh) 由 defer 触发
			}
			select {
			case objCh <- obj:
			case <-ctx.Done():
				listErr = ctx.Err()
				return
			}
		}
	}()
	for rmErr := range m.c.RemoveObjects(ctx, bucket, objCh, minio.RemoveObjectsOptions{}) {
		if rmErr.Err != nil {
			return rmErr.Err
		}
	}
	return listErr
}

// Minio 是基于 MinIO 对象存储的 Storage 实现。
// path 直接作为 object key 使用，不做任何前缀处理。
type Minio struct {
	client minioAPI
	bucket string
}

// 编译期断言：Minio 必须满足 Storage 接口。
var _ Storage = (*Minio)(nil)

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
	// minio-go GetObject 是延迟执行的，错误要等到 Read/Stat 才暴露。
	// 先 Stat 一次以同步返回 ErrNotFound。
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
	// S3 RemoveObject 对不存在的 key 是 noop（idempotent delete），
	// 必须先 Stat 才能返回 ErrNotFound。
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
			// minio 没有目录概念，但调用方可能传"目录"路径。
			// 用 List 探测前缀，命中即视为目录返回 IsDir=true。
			prefix := strings.TrimSuffix(path, "/") + "/"
			for obj := range m.client.ListObjects(ctx, m.bucket, minio.ListObjectsOptions{Prefix: prefix, MaxKeys: 1}) {
				if obj.Err == nil {
					return &FileInfo{Path: path, IsDir: true}, nil
				}
			}
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
		fi := objectInfoToFileInfo(obj, isDir)
		if isDir {
			// CommonPrefix 没有真实元数据，显式置零避免泄漏 1970-01-01 等假数据
			fi.Size = 0
			fi.ContentType = ""
			fi.ModTime = time.Time{}
		}
		out = append(out, fi)
	}
	return out, nil
}

func objectInfoToFileInfo(info minio.ObjectInfo, isDir bool) *FileInfo {
	return &FileInfo{
		Path:        info.Key,
		Size:        info.Size,
		ContentType: info.ContentType,
		ModTime:     info.LastModified,
		IsDir:       isDir,
	}
}
