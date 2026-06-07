package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotFound 表示请求的 path 在底层存储不存在。
// 调用方可使用 errors.Is(err, ErrNotFound) 判断。
var ErrNotFound = errors.New("storage: file not found")

// ErrIsDir 表示对目录类型 path 调用了仅文件适用的方法（如 Read）。
var ErrIsDir = errors.New("storage: path is a directory")

// FileInfo 描述文件或目录的元数据。
type FileInfo struct {
	Path        string    // 与调用方传入的 path 一致
	Size        int64     // 字节数
	ContentType string    // MIME 类型
	ModTime     time.Time // 最后修改时间
	IsDir       bool      // 是否为目录
}

// Storage 是统一存储接口。两种类型对 path 的语义不同：
//   - local 实现要求 path 是文件系统绝对路径
//   - minio 实现把 path 直接当 object key
//
// storage 层不做任何路径拼接，调用方传什么就用什么。
type Storage interface {
	// Write 写入数据流到指定 path。
	// size >= 0 时必须等于 r 的实际字节数（不一致时返回错误）；
	// size < 0 表示长度未知（minio 会用分片上传，local 直接读到 EOF）。
	// contentType 为空时实现侧的行为因后端而异（local 留空、minio 兜底为
	// application/octet-stream），调用方如关心跨实现一致性应显式提供。
	//
	// 契约：单文件原子写——目标 path 要么保留旧内容、要么写出完整新内容，
	// 不会留下半成品。local 通过同目录 .tmp + Sync + Rename 实现，
	// minio 由 PutObject 协议契约天然保证。
	Write(ctx context.Context, path string, r io.Reader, size int64, contentType string) error

	// Read 返回指定 path 的内容流，调用方负责 Close。
	// 不存在返回 ErrNotFound，path 是目录返回 ErrIsDir。
	Read(ctx context.Context, path string) (io.ReadCloser, error)

	// Delete 删除指定 path 的单个文件。不存在返回 ErrNotFound。
	// path 是目录时返回 ErrIsDir（与 Read 对称）。
	Delete(ctx context.Context, path string) error

	// DeleteDir 递归删除指定目录及其所有内容。
	// 目录不存在视为已删除，返回 nil（与 os.RemoveAll 一致）。
	DeleteDir(ctx context.Context, path string) error

	// Stat 返回 path 的元信息。不存在返回 ErrNotFound。
	// path 是目录时返回 IsDir=true 的 FileInfo。
	Stat(ctx context.Context, path string) (*FileInfo, error)

	// List 列出指定目录下的直接子项（不递归），包括子文件和子目录。
	// 子目录在结果中以 IsDir=true 表示。
	// 目录不存在返回 ErrNotFound，目录为空返回空切片。
	List(ctx context.Context, dir string) ([]*FileInfo, error)

	// Rename 将 src 重命名为 dst（支持文件和目录，与 os.Rename 一致）。
	//
	// 行为契约：
	//   - src 不存在时返回 ErrNotFound
	//   - dst 已存在时按"覆盖"语义处理（实现负责清理）
	//   - local 实现：os.Rename，同文件系统下原子；文件 / 目录均原子
	//   - minio 实现（文件）：CopyObject + RemoveObject 两步补偿
	//   - minio 实现（目录）：枚举前缀 → Phase A 全量 Copy → Phase B 全量 Delete，
	//     Phase A 成功后 dst 即权威完整副本；Phase B 失败时 src 残留，
	//     业务层应以 dst 为准并做幂等清理
	//   - minio 两种形态都不是真正的原子，进程崩溃可能让 src 与 dst 共存，
	//     业务层需保证幂等。详见设计文档 §1.12
	Rename(ctx context.Context, src, dst string) error
}
