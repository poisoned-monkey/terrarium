# Testing the DevEnvironment Operator

## Option 1: Local cluster (kind) + operator in a terminal

Best for a quick smoke test without building operator images.

### Setup

```bash
# Install kind if needed: https://kind.sigs.k8s.io/
kind create cluster --name dev-env-test

# Verify kubeconfig points to this cluster
kubectl cluster-info --context kind-dev-env-test
```

### 1. Install the CRD and start the operator

**Terminal 1:**

```bash
cd /path/to/terrarium
go mod tidy
kubectl apply -k config/crd
go run ./cmd/manager --leader-elect=false
```

You should see log output: `starting manager`, controller registered.

### 2. Create a DevEnvironment

**Terminal 2:**

```bash
kubectl apply -f config/samples/dev.example.com_v1alpha1_devenvironment_simple.yaml
```

Verify:

```bash
# CR should reach Ready phase
kubectl get devenvironments
# NAME     PHASE   NAMESPACE   BRANCH   AGE
# simple   Ready   dev-simple              XXs

# Namespace created
kubectl get ns dev-simple

# Pods and services
kubectl get all -n dev-simple
# Expected: Deployment web (nginx), Deployment postgres, Service web, Service postgres, pods
```

### 3. Verify clean-up on CR deletion

```bash
kubectl delete devenvironment simple
kubectl get ns dev-simple   # namespace should be gone (or in Terminating)
```

---

## Option 2: Sync test (sync-receiver + sync-client)

Requires a DevEnvironment with a service that has `sync` configured.

### Build and load the sync-receiver image into kind

```bash
make docker-build-sync-receiver
kind load docker-image dev-environment-operator/sync-receiver:latest --name dev-env-test
```

### Create a DevEnvironment with sync

Apply the sample manifest:

```yaml
# config/samples/dev.example.com_v1alpha1_devenvironment_sync_test.yaml
apiVersion: dev.example.com/v1alpha1
kind: DevEnvironment
metadata:
  name: sync-test
spec:
  namespace: dev-sync-test
  stack:
    services:
      - name: api
        image: nginx:alpine
        sync:
          paths:
            - "."
          exclude:
            - node_modules
            - .git
          hotReload: true
        ports:
          - containerPort: 80
            name: http
```

```bash
kubectl apply -f config/samples/dev.example.com_v1alpha1_devenvironment_sync_test.yaml
# Wait for Ready and pod to start
kubectl get pods -n dev-sync-test -w
```

### Run sync-client

From a directory that contains files to sync (e.g. create `hello.txt`):

```bash
echo "hello" > hello.txt
make sync-client
bin/sync-client --devenv=sync-test --service=api --base-dir=.
```

Logs should contain lines like `sync hello.txt`. Verify inside the pod:

```bash
kubectl exec -n dev-sync-test deploy/api -c api -- cat /workspace/hello.txt
# hello
```

Stop sync-client with Ctrl+C.

---

## Option 3: build-client test

Requires a service with `build.context` and a real directory containing a Dockerfile.

### Minimal build context

```bash
mkdir -p /tmp/dev-build-test
echo 'FROM busybox:1.36
CMD ["sleep", "infinity"]' > /tmp/dev-build-test/Dockerfile
```

### DevEnvironment with build

```yaml
# Add to an existing CR or create a new one:
spec:
  stack:
    services:
      - name: api
        build:
          context: "."
          dockerfile: Dockerfile
```

Or edit an existing CR:

```bash
kubectl edit devenvironment simple
# Add build.context and build.dockerfile to one of the services.
```

### Build and update the image

Local registry (no remote push):

```bash
# For kind you can skip the push and use kind load after the build
bin/build-client --devenv=simple --service=api --base-dir=/tmp/dev-build-test --image=dev-api:local --no-push
kind load docker-image dev-api:local --name dev-env-test
```

With your own registry:

```bash
export REGISTRY=your-registry.com/your-user
bin/build-client --devenv=simple --service=api --base-dir=/tmp/dev-build-test
# Verify
kubectl get deploy api -n dev-simple -o jsonpath='{.spec.template.spec.containers[0].image}'
```

---

## Option 4: Unit tests (Go)

Run controller and helper tests:

```bash
go test ./controllers/... ./api/... -v
```

With coverage:

```bash
go test ./controllers/... -coverprofile=coverage.out
go tool cover -html=coverage.out
```

---

## Quick-check checklist

| Step | Command | Expected result |
|------|---------|-----------------|
| 1 | `kubectl apply -k config/crd` | CRD created |
| 2 | `go run ./cmd/manager --leader-elect=false` | Operator running |
| 3 | `kubectl apply -f config/samples/..._simple.yaml` | CR created |
| 4 | `kubectl get devenvironments` | Phase=Ready, Namespace=dev-simple |
| 5 | `kubectl get all -n dev-simple` | Deployment web, postgres, pods Running |
| 6 | `kubectl delete devenvironment simple` | CR deleted |
| 7 | `kubectl get ns dev-simple` | Namespace gone (or Terminating) |
