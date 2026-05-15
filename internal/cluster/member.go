package cluster

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func EnsureMembersDir(homeDir string) (string, error) {
	dir := filepath.Join(homeDir, "cluster", "members")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

func WriteRegistration(membersDir, id, role, host string, port, pid int) error {
	content := fmt.Sprintf("%s|%s:%d|%d", role, host, port, pid)
	return os.WriteFile(filepath.Join(membersDir, id), []byte(content), 0644)
}

func ListMembers(membersDir string) ([]MemberInfo, error) {
	entries, err := os.ReadDir(membersDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var members []MemberInfo
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		members = append(members, MemberInfo{
			ID:    entry.Name(),
			Mtime: info.ModTime(),
		})
	}
	return members, nil
}

func RemoveFile(membersDir, id string) error {
	path := filepath.Join(membersDir, id)
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func GenerateRegID() string {
	s := time.Now().Format("20060102150405.000")
	return strings.Replace(s, ".", "", 1)
}
