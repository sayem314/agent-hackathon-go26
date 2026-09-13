BINARY := agent-hackathon-go26

.PHONY: help build fmt vet eval clean

help: ## Show all commands
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-8s %s\n", $$1, $$2}'

build: ## Build the binary
	go build -o $(BINARY) .

fmt: ## gofmt write
	gofmt -w .

vet: ## go vet
	go vet ./...

eval: ## Run live eval scenarios (needs .env)
	sh evals/eval.sh

clean: ## Remove build artifacts and transcripts
	rm -f $(BINARY)
	rm -rf runs
