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
	CopyObject(ctx context.Context, dstBucket, dstKey, srcBucket, srcKey string) error
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

func (m *minioClient) CopyObject(ctx context.Context, dstBucket, dstKey, srcBucket, srcKey string) error {
	src := minio.CopySrcOptions{Bucket: srcBucket, Object: srcKey}
	dst := minio.CopyDestOptions{Bucket: dstBucket, Object: dstKey}
	_, err := m.c.CopyObject(ctx, dst, src)
	return err
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

// Rename 同时支持文件和目录，与 os.Rename 语义一致。
//
// 实现策略：
//   - 先 StatObject(src) 探测，命中走文件分支；
//   - 不命中再 ListObjects(prefix=src+"/", MaxKeys=1) 探测，命中走目录分支；
//   - 都不命中返回 ErrNotFound。
//
// 两条分支都不是真正的原子，通过补偿逻辑让语义尽量接近原子；最坏情况
// （src 与 dst 双份共存）由业务层幂等扫描兜底。详见设计文档 §1.12。
func (m *Minio) Rename(ctx context.Context, src, dst string) error {
	// 1. 判别文件还是目录
	_, statErr := m.client.StatObject(ctx, m.bucket, src, minio.StatObjectOptions{})
	if statErr == nil {
		return m.renameFile(ctx, src, dst)
	}
	if !isNotExist(statErr) {
		return fmt.Errorf("storage: minio stat %s: %w", src, statErr)
	}
	// src 没有同名对象，看是否存在 src+"/" 前缀
	prefix := strings.TrimSuffix(src, "/") + "/"
	hit := false
	for obj := range m.client.ListObjects(ctx, m.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true, MaxKeys: 1}) {
		if obj.Err != nil {
			return fmt.Errorf("storage: minio rename probe %s: %w", src, obj.Err)
		}
		hit = true
		break
	}
	if !hit {
		return ErrNotFound
	}
	return m.renameDir(ctx, src, dst)
}

// renameFile 执行单 object 的重命名：清 dst 残留 → CopyObject → RemoveObject src。
// 任一步失败按设计文档 §1.12.1 故障路径表恢复；最后一步失败时尽力回滚 dst。
func (m *Minio) renameFile(ctx context.Context, src, dst string) error {
	// 2. 清理 dst 残留（裸对象）
	if err := m.cleanupDstObject(ctx, dst); err != nil {
		return err
	}
	// 3. 服务端 Copy
	if err := m.client.CopyObject(ctx, m.bucket, dst, m.bucket, src); err != nil {
		return fmt.Errorf("storage: minio rename copy %s -> %s: %w", src, dst, err)
	}
	// 4. 删源，失败尽力回滚
	if err := m.client.RemoveObject(ctx, m.bucket, src, minio.RemoveObjectOptions{}); err != nil {
		_ = m.client.RemoveObject(ctx, m.bucket, dst, minio.RemoveObjectOptions{})
		return fmt.Errorf("storage: minio rename delete src %s: %w", src, err)
	}
	return nil
}

// renameDir 执行目录（前缀）的重命名：
//
//	Phase 0: 清 dst 残留（裸对象 + 同名前缀都兜底清理）
//	Phase A: 枚举 src+"/" 下所有 key，逐个 CopyObject 到 dst+"/"
//	         任一失败 → 回滚已 Copy 的 dst 子对象 → 返回错误，src 完整
//	Phase B: 全部 Copy 完成后，逐个 RemoveObject src+"/" 下的对象
//	         失败不回滚（dst 已是权威新位置），返回错误让业务层幂等扫描兜底
func (m *Minio) renameDir(ctx context.Context, src, dst string) error {
	srcPrefix := strings.TrimSuffix(src, "/") + "/"
	dstPrefix := strings.TrimSuffix(dst, "/") + "/"

	// Phase 0: 清 dst 残留（同时兜两种形态：裸对象 + 同名前缀）
	if err := m.cleanupDstObject(ctx, dst); err != nil {
		return err
	}
	if err := m.client.RemoveObjectsRecursive(ctx, m.bucket, dstPrefix); err != nil {
		return fmt.Errorf("storage: minio rename cleanup dst dir %s: %w", dst, err)
	}

	// Phase A: 枚举并 Copy
	var keys []string
	for obj := range m.client.ListObjects(ctx, m.bucket, minio.ListObjectsOptions{Prefix: srcPrefix, Recursive: true}) {
		if obj.Err != nil {
			return fmt.Errorf("storage: minio rename list %s: %w", src, obj.Err)
		}
		keys = append(keys, obj.Key)
	}
	copied := make([]string, 0, len(keys))
	for _, srcKey := range keys {
		dstKey := dstPrefix + strings.TrimPrefix(srcKey, srcPrefix)
		if err := m.client.CopyObject(ctx, m.bucket, dstKey, m.bucket, srcKey); err != nil {
			// 回滚：删掉已 Copy 的 dst 子对象，让 dst 回到空状态
			for _, k := range copied {
				_ = m.client.RemoveObject(ctx, m.bucket, k, minio.RemoveObjectOptions{})
			}
			return fmt.Errorf("storage: minio rename copy %s -> %s: %w", srcKey, dstKey, err)
		}
		copied = append(copied, dstKey)
	}

	// Phase B: 逐个删源
	for _, srcKey := range keys {
		if err := m.client.RemoveObject(ctx, m.bucket, srcKey, minio.RemoveObjectOptions{}); err != nil {
			// 不回滚 dst：dst 此时已是权威完整副本，业务层下一轮幂等扫描清理 src 残留
			return fmt.Errorf("storage: minio rename delete src %s: %w", srcKey, err)
		}
	}
	return nil
}

// cleanupDstObject 删除 dst 处可能残留的裸对象。dst 不存在视为正常返回 nil。
func (m *Minio) cleanupDstObject(ctx context.Context, dst string) error {
	_, err := m.client.StatObject(ctx, m.bucket, dst, minio.StatObjectOptions{})
	if err == nil {
		if rmErr := m.client.RemoveObject(ctx, m.bucket, dst, minio.RemoveObjectOptions{}); rmErr != nil {
			return fmt.Errorf("storage: minio rename cleanup dst %s: %w", dst, rmErr)
		}
		return nil
	}
	if !isNotExist(err) {
		return fmt.Errorf("storage: minio rename stat dst %s: %w", dst, err)
	}
	return nil
}
