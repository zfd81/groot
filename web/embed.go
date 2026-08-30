// Package web 通过 go:embed 携带前端构建产物（web/dist）。
// dist 目录内容由 `make web` 生成；未构建时仅含 .gitkeep 占位，
// 此时 /ui 返回"前端未构建"提示页。
package web

import "embed"

//go:embed all:dist
var DistFS embed.FS
