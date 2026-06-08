package cluster

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	istorage "github.com/zfd81/groot/internal/storage"
)

// WriteRegistration 写入注册文件。内容格式不变:role|host:port|pid。
// 单文件原子写由 storage.Storage.Write 接口契约保证。
func WriteRegistration(store istorage.Storage, membersDir, id, role, host string, port, pid int) error {
	content := fmt.Sprintf("%s|%s:%d|%d", role, host, port, pid)
	return store.Write(
		context.Background(),
		filepath.Join(membersDir, id),
		bytes.NewReader([]byte(content)),
		int64(len(content)),
		"text/plain",
	)
}

// ListMembers 列出 members 目录下所有注册文件的元信息(ID + Mtime)。
// 目录不存在(local 首次启动 / minio 无前缀)时返回 nil(与原 os.IsNotExist 语义一致)。
func ListMembers(store istorage.Storage, membersDir string) ([]MemberInfo, error) {
	entries, err := store.List(context.Background(), membersDir)
	if err != nil {
		if errors.Is(err, istorage.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var members []MemberInfo
	for _, entry := range entries {
		if entry.IsDir {
			continue
		}
		members = append(members, MemberInfo{
			ID:    filepath.Base(entry.Path),
			Mtime: entry.ModTime,
		})
	}
	return members, nil
}

// RemoveFile 删除注册文件;不存在视为成功(幂等),与原 os.IsNotExist 处理一致。
func RemoveFile(store istorage.Storage, membersDir, id string) error {
	err := store.Delete(context.Background(), filepath.Join(membersDir, id))
	if err != nil && !errors.Is(err, istorage.ErrNotFound) {
		return err
	}
	return nil
}

// GenerateRegID 生成 17 位纯数字注册 ID(格式:YYYYMMDDHHMMSSmmm)。
func GenerateRegID() string {
	s := time.Now().Format("20060102150405.000")
	return strings.Replace(s, ".", "", 1)
}
