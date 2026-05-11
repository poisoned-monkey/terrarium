# Terrarium operator
.PHONY: build run install-crd uninstall-crd deploy run-local docker-build-sync-receiver sync-client build-client

# Build the operator binary
build:
	go build -o bin/manager ./cmd/manager

# Build the sync-receiver image (sidecar for live code sync)
SYNC_RECEIVER_IMAGE ?= dev-environment-operator/sync-receiver:latest
docker-build-sync-receiver:
	docker build -t $(SYNC_RECEIVER_IMAGE) -f images/sync-receiver/Dockerfile .

# Build sync-client and build-client binaries
sync-client:
	go build -o bin/sync-client ./cmd/sync-client
build-client:
	go build -o bin/build-client ./cmd/build-client

# Run locally (requires kubeconfig and CRD installed)
run: install-crd
	go run ./cmd/manager

# Install CRD into the current cluster
install-crd:
	kubectl apply -k config/crd

# Remove CRD (and all DevEnvironment resources)
uninstall-crd:
	kubectl delete -k config/crd --ignore-not-found

# Build the operator image
IMAGE ?= dev-environment-operator:latest
docker-build:
	docker build -t $(IMAGE) .

# Deploy the operator into the cluster (namespace + RBAC + Deployment).
# Build and push the sync-receiver image to a registry accessible by the cluster first.
deploy: install-crd
	kubectl apply -f config/operator/deploy.yaml

# Run outside the cluster (connects via KUBECONFIG)
run-local: install-crd
	go run ./cmd/manager --leader-elect=false

# Apply the sample DevEnvironment
apply-sample:
	kubectl apply -f config/samples/dev.example.com_v1alpha1_devenvironment.yaml

# Run tests
test:
	go test ./controllers/... ./api/... -v

# Lint
lint:
	go vet ./...
	staticcheck ./...
