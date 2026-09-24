.PHONY: all build run test coverage lint verify clean docker-build docker-run

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

docker-build:
	docker build -t mirrormere:local .

docker-run:
	docker run --rm -p 8080:8080 mirrormere:local

clean:
	rm -rf bin/ coverage.out .mirrormere-coverage-*.tmp
