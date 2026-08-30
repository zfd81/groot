.PHONY: build build-go build-all build-darwin build-linux build-windows web run test clean

# 默认编译（当前平台，先构建前端再嵌入）
build: web
	go build -o bin/groot ./cmd/groot

# 仅编译后端（复用 web/dist 中已有的前端产物，无需 Node）
build-go:
	go build -o bin/groot ./cmd/groot

# 构建 Web 前端（产物输出到 web/dist，由 go:embed 嵌入二进制）
# 用 npm ci 保证可复现，不改动 package-lock.json
web:
	cd web && npm ci && npm run build

# 编译所有平台
build-all: web build-darwin build-linux build-windows
	@echo "编译完成：darwin-arm64, linux-amd64, windows-amd64"

# macOS ARM64
build-darwin:
	@mkdir -p bin/darwin-arm64
	GOOS=darwin GOARCH=arm64 go build -o bin/darwin-arm64/groot ./cmd/groot

# Linux AMD64
build-linux:
	@mkdir -p bin/linux-amd64
	GOOS=linux GOARCH=amd64 go build -o bin/linux-amd64/groot ./cmd/groot

# Windows AMD64
build-windows:
	@mkdir -p bin/windows-amd64
	GOOS=windows GOARCH=amd64 go build -o bin/windows-amd64/groot.exe ./cmd/groot

run:
	go run ./cmd/groot

test:
	go test -v ./...

clean:
	rm -rf bin/
