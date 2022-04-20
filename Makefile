TARGET_URL ?= http://localhost:8080
SMEE_URL ?= https://smee.io/new
MD_FILES := $(shell find . -type f -regex ".*md"  -not -regex '^./vendor/.*'  -not -regex '^./.vale/.*' -not -regex "^./.git/.*" -print)


all: test lint build

test:
	@go test ./... -v

build:
	@echo "building."
	@mkdir -p bin/
	@go build  -v -ldflags="-s -w"  -o bin/gosmee gosmee.go

lint: lint-go lint-md

lint-go:
	@echo "linting."
	@golangci-lint run --disable gosimple --disable staticcheck --disable structcheck --disable unused

.PHONY: lint-md
lint-md: ${MD_FILES} ## runs markdownlint and vale on all markdown files
	@echo "Linting markdown files..."
	@markdownlint $(MD_FILES)
	@echo "Grammar check with vale of documentation..."
	@vale docs/content --minAlertLevel=error --output=line

dev:
	reflex -r 'gosmee.go' -s go run gosmee.go -- --saveDir /tmp/save2 $(SMEE_URL) $(TARGET_URL)

.PHONY: vendor
vendor:
	@go mod tidy
	@go mod vendor