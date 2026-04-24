.PHONY: build build-all build-darwin build-linux build-windows run test clean

# 默认编译（当前平台）
build:
	go build -o bin/groot ./cmd/groot

# 编译所有平台
build-all: build-darwin build-linux build-windows
	@echo "编译完成：darwin-arm64, linux-amd64, windows-amd64"

# macOS ARM64
build-darwin:
	GOOS=darwin GOARCH=arm64 go build -o bin/groot-darwin-arm64 ./cmd/groot

# Linux AMD64
build-linux:
	GOOS=linux GOARCH=amd64 go build -o bin/groot-linux-amd64 ./cmd/groot

# Windows AMD64
build-windows:
	GOOS=windows GOARCH=amd64 go build -o bin/groot-windows-amd64.exe ./cmd/groot

run:
	go run ./cmd/groot

test:
	go test -v ./...

clean:
	rm -rf bin/