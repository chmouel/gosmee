NAME  := gosmee
TARGET_URL ?= http://localhost:8080
SMEE_URL ?= https://smee.io/new
IMAGE_VERSION ?= latest
MD_FILES := $(shell find . -type f -regex ".*md"  -not -regex '^./vendor/.*' -not -regex '^./.vale/.*' -not -regex "^./.git/.*" -print)

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

.PHONY: test-e2e
test-e2e:
	@if [ -n "$$GOSMEE_E2E_REDIS_URL" ] && [ -z "$$GOSMEE_REDIS_TEST_URL" ]; then \
		export GOSMEE_REDIS_TEST_URL="$$GOSMEE_E2E_REDIS_URL"; \
	fi; \
	if [ -n "$$GOSMEE_REDIS_TEST_URL" ]; then \
		echo "Running Redis e2e tests with $$GOSMEE_REDIS_TEST_URL"; \
		go test -tags=e2e $(TEST_FLAGS) -count=1 ./e2e && go test $(TEST_FLAGS) -count=1 ./gosmee -run TestRedisStreamsIntegration; \
	else \
		command -v docker >/dev/null || { echo "docker is required for make test-e2e when GOSMEE_REDIS_TEST_URL is unset" >&2; exit 1; }; \
		docker info >/dev/null 2>&1 || { echo "docker daemon is not available; set GOSMEE_REDIS_TEST_URL to use an existing Redis" >&2; exit 1; }; \
		container_id=$$(docker run -d -p 127.0.0.1::6379 redis:7-alpine) || { echo "failed to start redis:7-alpine container" >&2; exit 1; }; \
		trap 'docker rm -f "$$container_id" >/dev/null 2>&1 || true' EXIT INT TERM; \
		redis_port=""; \
		for _ in $$(seq 1 60); do \
			if docker exec "$$container_id" redis-cli ping >/dev/null 2>&1; then \
				redis_port=$$(docker port "$$container_id" 6379/tcp | sed 's/.*://'); \
				break; \
			fi; \
			sleep 1; \
		done; \
		if [ -z "$$redis_port" ]; then \
			echo "Redis container did not become ready" >&2; \
			exit 1; \
		fi; \
		export GOSMEE_REDIS_TEST_URL="redis://localhost:$$redis_port/0"; \
		echo "Running Redis e2e tests with $$GOSMEE_REDIS_TEST_URL"; \
		go test -tags=e2e $(TEST_FLAGS) -count=1 ./e2e && go test $(TEST_FLAGS) -count=1 ./gosmee -run TestRedisStreamsIntegration; \
	fi

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
