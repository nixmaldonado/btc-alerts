# btc-alerts — developer tasks
#
# Common targets:
#   make test              run unit tests (integration tests skip without DynamoDB Local)
#   make test-integration  start DynamoDB Local, run the full suite, tear it down
#   make check             vet + unit tests

# DynamoDB Local endpoint used by the store integration tests. When unset, those
# tests t.Skip, so `make test` stays green without Docker.
DYNAMODB_ENDPOINT ?= http://localhost:8000
DDB_CONTAINER     := btc-alerts-ddb-local

# Coverage profile written by `make cover` and rendered by `make cover-html`.
COVERPROFILE := coverage.out

# Module prefix so goimports groups our own packages in their own import block.
LOCAL_PREFIX := github.com/nixmaldonado/btc-alerts
# Run goimports without requiring a pre-installed binary (uses the module cache).
GOIMPORTS    := go run golang.org/x/tools/cmd/goimports@latest

.PHONY: test
test: ## Run unit tests (integration tests skip without DYNAMODB_ENDPOINT)
	go test ./...

.PHONY: test-verbose
test-verbose: ## Run unit tests with verbose output
	go test ./... -v

.PHONY: test-integration
test-integration: ddb-up ## Run the full suite against DynamoDB Local, then tear it down
	DYNAMODB_ENDPOINT=$(DYNAMODB_ENDPOINT) go test ./... -v; \
	status=$$?; \
	$(MAKE) ddb-down; \
	exit $$status

.PHONY: ddb-up
ddb-up: ## Start a DynamoDB Local container and wait until it accepts connections
	@if [ -z "$$(docker ps -q -f name=^/$(DDB_CONTAINER)$$)" ]; then \
		echo "starting DynamoDB Local ($(DDB_CONTAINER))..."; \
		docker run -d --rm --name $(DDB_CONTAINER) -p 8000:8000 amazon/dynamodb-local >/dev/null; \
	else \
		echo "DynamoDB Local already running"; \
	fi
	@echo "waiting for $(DYNAMODB_ENDPOINT) ..."; \
	for i in $$(seq 1 30); do \
		if curl -s -o /dev/null "$(DYNAMODB_ENDPOINT)"; then echo "ready"; exit 0; fi; \
		sleep 0.5; \
	done; \
	echo "timed out waiting for DynamoDB Local" >&2; exit 1

.PHONY: ddb-down
ddb-down: ## Stop the DynamoDB Local container
	@docker rm -f $(DDB_CONTAINER) >/dev/null 2>&1 || true

.PHONY: cover
cover: ## Run unit tests with coverage and print a per-package + total summary
	go test ./... -coverprofile=$(COVERPROFILE)
	go tool cover -func=$(COVERPROFILE)

.PHONY: cover-html
cover-html: ## Render the coverage profile as HTML and open it in a browser
	go test ./... -coverprofile=$(COVERPROFILE)
	go tool cover -html=$(COVERPROFILE)

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: imports
imports: ## Format code and group imports stdlib / third-party / local (goimports)
	$(GOIMPORTS) -w -local $(LOCAL_PREFIX) .

.PHONY: fmt
fmt: imports ## Format all Go source and sort/group imports

.PHONY: tidy
tidy: ## Tidy module dependencies
	go mod tidy

.PHONY: check
check: vet imports test ## Vet, format imports, then run unit tests

.PHONY: help
help: ## List available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
