NAME  := gosmee
TARGET_URL ?= http://localhost:8080
SMEE_URL ?= https://smee.io/new
IMAGE_VERSION ?= latest
MD_FILES := $(shell find . -type f -regex ".*md"  -not -regex '^./vendor/.*'  -not -regex '^./.vale/.*' -not -regex "^./.git/.*" -print)

LDFLAGS := -s -w
FLAGS += -ldflags "$(LDFLAGS)" -buildvcs=true
OUTPUT_DIR = bin

all: test lint build docker-build


FORCE:

.PHONY: vendor
vendor:
	@echo Generating vendor directory
	@go mod tidy && go mod vendor

$(OUTPUT_DIR)/$(NAME): main.go FORCE
	go build -mod=vendor $(FLAGS)  -v -o $@ ./$<

test:
	@go test ./... -v

clean:
	@rm -rf bin/gosmee

build: clean
	@echo "building."
	@mkdir -p bin/
	@go build  -v $(FLAGS)  -o bin/gosmee gosmee.go

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

fmt:
	@go fmt `go list ./... | grep -v /vendor/`

fumpt:
	@gofumpt -w *.go

