package sync

import (
	"path"
	"strings"
)

// ignoredBaseNames 是操作系统或工具自动生成的文件名,不属于用户配置,
// 不参与同步:既不推送到数据库,也不从数据库拉取到本地。
//
//   - .DS_Store  macOS Finder 的目录元数据(每次打开目录都会重新生成)
//   - Thumbs.db  Windows 资源管理器的缩略图缓存
var ignoredBaseNames = map[string]bool{
	".DS_Store": true,
	"Thumbs.db": true,
}

// IsIgnoredPath 判断相对路径(或绝对路径)对应的文件是否应从同步中排除。
// 判断只看最后一段文件名,因此同一规则对任意层级生效。
//
// 排除两类:
//   - *.tmp    sync 自己原子写用的中转产物(上次 pull 崩溃可能留下残留)
//   - 系统生成文件 见 ignoredBaseNames,以及 macOS 的 AppleDouble 副本 ._*
//
// 本地与远端两侧都用这个判断,保证一个被忽略的文件不会因为只在一侧过滤
// 而被判成差异——否则本地过滤掉的 .DS_Store 会在远端侧显示为「数据库独有」
// 并被 pull 重新写回本地。
func IsIgnoredPath(p string) bool {
	name := path.Base(strings.ReplaceAll(p, "\\", "/"))
	if strings.HasSuffix(name, ".tmp") {
		return true
	}
	if ignoredBaseNames[name] {
		return true
	}
	// AppleDouble:macOS 在非 HFS 文件系统上为每个文件存放元数据的伴随文件。
	// 只认 "._" 前缀且名字更长的情况,避免把 "._" 本身之外的正常文件误伤。
	return strings.HasPrefix(name, "._") && len(name) > 2
}
