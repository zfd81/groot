.PHONY: build run test clean

build:
	go build -o bin/groot cmd/groot/main.go

run:
	go run cmd/groot/main.go

test:
	go test -v ./...

clean:
	rm -rf bin/