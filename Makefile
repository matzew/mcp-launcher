IMG ?= quay.io/matzew/mcp-launcher:latest
CONTAINER_TOOL ?= podman

.PHONY: build
build: ## Build the binary.
	go build -o bin/mcp-launcher .

.PHONY: run
run: build ## Run locally (uses kubeconfig).
	CATALOG_NAMESPACE=mcp-catalog TARGET_NAMESPACE=default ./bin/mcp-launcher

.PHONY: fmt
fmt: ## Format code.
	go fmt ./...

.PHONY: vet
vet: ## Vet code.
	go vet ./...

.PHONY: test
test: fmt vet ## Run tests.
	go test ./...

.PHONY: docker-build
docker-build: ## Build container image.
	$(CONTAINER_TOOL) build -t $(IMG) .

.PHONY: docker-push
docker-push: ## Push container image.
	$(CONTAINER_TOOL) push $(IMG)

.PHONY: ko-build
ko-build: ## Build and push with ko.
	KO_DOCKER_REPO=quay.io/matzew ko build -B --tags latest .

.PHONY: deploy
deploy: ## Deploy to cluster.
	kubectl apply -f deploy/rbac/
	kubectl apply -f deploy/deployment.yaml

.PHONY: undeploy
undeploy: ## Remove from cluster.
	kubectl delete -f deploy/deployment.yaml || true
	kubectl delete -f deploy/rbac/ || true

.PHONY: catalog
catalog: ## Apply sample catalog entries.
	kubectl create namespace mcp-catalog --dry-run=client -o yaml | kubectl apply -f -
	kubectl apply -f deploy/catalog/

.PHONY: help
help: ## Show this help.
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
