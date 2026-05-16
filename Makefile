.PHONY: build build-all build-darwin build-linux build-windows run test clean

# 默认编译（当前平台）
build:
	go build -o bin/groot ./cmd/groot

# 编译所有平台
build-all: build-darwin build-linux build-windows
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