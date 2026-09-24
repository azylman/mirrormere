.PHONY: all build run test coverage lint verify clean

all: build

build:
	@mkdir -p bin
	go build -v -o bin/server ./cmd/server

run:
	go run ./cmd/server

test:
	go test -v ./...

coverage:
	./scripts/check-coverage.sh --check --summary

lint:
	golangci-lint run ./...

verify:
	./scripts/verify.sh --full

clean:
	rm -rf bin/ coverage.out .mirrormere-coverage-*.tmp
