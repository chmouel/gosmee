NAME  := gosmee
TARGET_URL ?= http://localhost:8080
SMEE_URL ?= https://smee.io/new
IMAGE_VERSION ?= latest
MD_FILES := $(shell find . -type f -regex ".*md"  -not -regex '^./vendor/.*' -not -regex '^./tint/.*' -not -regex '^./.vale/.*' -not -regex "^./.git/.*" -print)

LDFLAGS := -s -w
FLAGS += -ldflags "$(LDFLAGS)" -buildvcs=true
OUTPUT_DIR = bin
TEST_FLAGS = -v 
COVERAGE_FLAGS = -coverprofile=coverage.out -covermode=atomic 

all: test lint build
FORCE:

.PHONY: vendor
vendor:
	@echo Generating vendor directory
	@go mod tidy && go mod vendor

$(OUTPUT_DIR)/$(NAME): main.go FORCE
	go build -mod=vendor $(FLAGS)  -o $@ ./$<

$(OUTPUT_DIR)/$(NAME)-aarch64-linux: main.go FORCE
	env GOARCH=arm64 GOOS=linux	go build -mod=vendor $(FLAGS)   -o $@ ./$<

test:
	@go test $(TEST_FLAGS) ./... 

.PHONY: html-coverage
html-coverage: ## generate html coverage
	@mkdir -p tmp
	@go test $(COVERAGE_FLAGS) -coverprofile=tmp/c.out ./.../ && go tool cover -html=tmp/c.out

clean:
	@rm -rf $(OUTPUT_DIR)/gosmee

build: clean
	@echo "building."
	@mkdir -p $(OUTPUT_DIR)/
	@go build  $(FLAGS)  -o $(OUTPUT_DIR)/gosmee main.go

lint: lint-go lint-md

lint-go:
	@echo "linting."
	golangci-lint version
	golangci-lint run ./... --modules-download-mode=vendor

.PHONY: lint-md
lint-md: ${MD_FILES} ## runs markdownlint and vale on all markdown files
	@echo "Linting markdown files..."
	@markdownlint $(MD_FILES)
	@echo "Grammar check with vale of documentation..."
	@vale docs/content --minAlertLevel=error --output=line

dev-server:
	reflex -r '.*\.(tmpl|go)' -s go run main.go -- server --footer "Contact: <a href=\"https://twitter.com/me\">Me</a> - use it at your own risk"

fmt:
	@go fmt `go list ./... | grep -v /vendor/`

fumpt:
	@gofumpt -e -w -extra ./

