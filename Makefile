.PHONY: build install install-skill test lint format

build: ## Build ./bin/wt
	go build -o bin/wt ./cmd/wt

install: ## Install wt into $(go env GOBIN) / GOPATH/bin
	go install ./cmd/wt

install-skill: ## Symlink the /wt Claude Code skill into ~/.claude/skills
	mkdir -p $(HOME)/.claude/skills
	ln -sfn $(CURDIR)/skills/wt $(HOME)/.claude/skills/wt

test: ## Run tests with race detection
	go test -race -count=1 ./...

lint: ## Run golangci-lint
	golangci-lint run ./...

format: ## gofmt + go mod tidy
	gofmt -w .
	go mod tidy
