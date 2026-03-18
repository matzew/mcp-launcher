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

.PHONY: image-build
image-build: ## Build container image.
	$(CONTAINER_TOOL) build -t $(IMG) .

.PHONY: image-push
image-push: ## Push container image.
	$(CONTAINER_TOOL) push $(IMG)

.PHONY: image
image: image-build image-push ## Build and push container image.

.PHONY: install
install: ## Install everything from single-file distribution.
	kubectl apply -f dist/mcp-launcher.yaml

.PHONY: uninstall
uninstall: ## Remove everything installed by dist/mcp-launcher.yaml.
	kubectl delete -f dist/mcp-launcher.yaml || true

.PHONY: deploy
deploy: ## Deploy RBAC, launcher, and catalog to cluster.
	kubectl apply -f deploy/rbac/
	kubectl apply -f deploy/deployment.yaml
	kubectl create namespace mcp-catalog --dry-run=client -o yaml | kubectl apply -f -
	kubectl apply -f deploy/catalog/

.PHONY: rollout
rollout: ## Restart the launcher deployment and wait for it to be ready.
	kubectl rollout restart deployment/mcp-launcher -n mcp-system
	kubectl rollout status deployment/mcp-launcher -n mcp-system --timeout=120s

.PHONY: release
release: image deploy rollout ## Build, push, deploy, and rollout (full release cycle).

.PHONY: undeploy
undeploy: ## Remove launcher and catalog from cluster.
	kubectl delete -f deploy/deployment.yaml || true
	kubectl delete -f deploy/rbac/ || true

.PHONY: catalog
catalog: ## Apply catalog entries (ConfigMaps, RBAC, ServiceAccounts).
	kubectl create namespace mcp-catalog --dry-run=client -o yaml | kubectl apply -f -
	kubectl apply -f deploy/catalog/

.PHONY: help
help: ## Show this help.
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
