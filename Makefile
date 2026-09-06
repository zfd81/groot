.PHONY: build build-go build-all build-darwin build-linux build-windows web run test clean

# 编译产物输出目录（已被 .gitignore 忽略）
DIST_DIR := dist

# 默认编译（当前平台，先构建前端再嵌入；产物 dist/groot，用于本地开发运行）
build: web
	go build -o $(DIST_DIR)/groot ./cmd/groot

# 仅编译后端（复用 web/dist 中已有的前端产物，无需 Node）
build-go:
	go build -o $(DIST_DIR)/groot ./cmd/groot

# 构建 Web 前端（产物输出到 web/dist，由 go:embed 嵌入二进制）
# 用 npm ci 保证可复现，不改动 package-lock.json
web:
	cd web && npm ci && npm run build

# 编译所有平台：产物为 dist/ 下三个 zip，可直接上传 GitHub Release
# zip 内不含目录层级，解压即得可执行文件（macOS/Linux 为 groot，Windows 为 groot.exe）
build-all: web build-darwin build-linux build-windows
	@echo "编译完成：$(DIST_DIR)/groot-darwin-arm64.zip, $(DIST_DIR)/groot-linux-amd64.zip, $(DIST_DIR)/groot-windows-amd64.zip"

# macOS ARM64 → dist/groot-darwin-arm64.zip（解压后为 groot）
build-darwin:
	@mkdir -p $(DIST_DIR)/.build-darwin
	GOOS=darwin GOARCH=arm64 go build -o $(DIST_DIR)/.build-darwin/groot ./cmd/groot
	cd $(DIST_DIR)/.build-darwin && zip -q -X ../groot-darwin-arm64.zip groot
	@rm -rf $(DIST_DIR)/.build-darwin

# Linux AMD64 → dist/groot-linux-amd64.zip（解压后为 groot）
build-linux:
	@mkdir -p $(DIST_DIR)/.build-linux
	GOOS=linux GOARCH=amd64 go build -o $(DIST_DIR)/.build-linux/groot ./cmd/groot
	cd $(DIST_DIR)/.build-linux && zip -q -X ../groot-linux-amd64.zip groot
	@rm -rf $(DIST_DIR)/.build-linux

# Windows AMD64 → dist/groot-windows-amd64.zip（解压后为 groot.exe）
build-windows:
	@mkdir -p $(DIST_DIR)/.build-windows
	GOOS=windows GOARCH=amd64 go build -o $(DIST_DIR)/.build-windows/groot.exe ./cmd/groot
	cd $(DIST_DIR)/.build-windows && zip -q -X ../groot-windows-amd64.zip groot.exe
	@rm -rf $(DIST_DIR)/.build-windows

run:
	go run ./cmd/groot

test:
	go test -v ./...

clean:
	rm -rf $(DIST_DIR)/
