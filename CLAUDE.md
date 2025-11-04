# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# Cortex Architecture Overview

## Executive Summary

Cortex is a production-grade serverless platform for deploying and managing ML workloads on Kubernetes. The system supports three workload types (RealtimeAPI, AsyncAPI, BatchAPI, TaskAPI) with distinct execution and scaling patterns. It uses Istio for traffic management, Kubernetes Deployments for long-running services, CRDs for job orchestration, and AWS services (SQS, S3) for async/batch infrastructure.

## Common Development Commands

### Building and Testing

```bash
# Build the CLI (output: ./bin/cortex)
make cli

# Run Go unit tests
make test

# Run linter (golint, looppointer, format checks)
make lint

# Format code (gofmt for Go, black for Python)
make format

# Build all images and push to ECR
make images-all

# Build dev images only (faster, for operator/activator/autoscaler)
make images-dev
```

### Development Workflow

```bash
# Typical full dev workflow:
make cluster-up      # Creates cluster, builds & pushes images
make devstart        # Stops in-cluster operator, runs operator locally with file watching
# Make changes, test with: cortex deploy <config.yaml>
make cluster-down    # Clean up

# If only modifying operator code:
make operator-local  # Runs operator locally with file watching

# If only modifying CLI:
make cli-watch       # Builds CLI with file watching, uses in-cluster operator

# Update specific components in cluster:
make operator-update        # Build and update in-cluster operator
make async-gateway-update   # Build and update async gateway
```

### Cluster Management

```bash
# Configure kubectl for dev cluster (from dev/config/cluster.yaml)
make kubectl

# Stop/start/restart in-cluster operator
make operator-stop
make operator-start
make operator-restart

# Debug operator with delve
make operator-local-dbg  # Attaches debugger on port 2345
```

### Testing

```bash
# Unit tests
./build/test.sh go

# E2E tests (requires existing cluster in dev/config/cluster.yaml)
make test-e2e

# E2E tests with new cluster
make test-e2e-new
```

### Development Setup Requirements

**Prerequisites:**
- Go 1.24+ (see go.mod)
- Docker with buildx (multi-arch support)
- kubectl, eksctl, aws-cli v1
- Python 3 with boto3, pyyaml

**First-time setup:**
1. Create `dev/config/env.sh` with AWS credentials (see CONTRIBUTING.md for template)
2. Run `make registry-create` to create ECR registries
3. Create `dev/config/cluster.yaml` with cluster configuration
4. Run `make tools` to install dev dependencies (golint, rerun, dlv, etc.)

**Important environment variables to set in bash profile:**
```bash
export CORTEX_DEV_DEFAULT_IMAGE_REGISTRY="<account_id>.dkr.ecr.<region>.amazonaws.com/cortexlabs"
export CORTEX_CLI_PATH="<cortex_repo_path>/bin/cortex"
alias cortex="$CORTEX_CLI_PATH"
```

## Core Components

### 1. CLI and Operator Architecture

**CLI Layer** (`cli/`)
- Entry point: `cli/main.go` → `cmd.Execute()`
- Cobra-based command system in `cli/cmd/`
- Commands interact with operator REST API (port 8888)
- Delegates deployments, deletions, and status queries to operator via HTTP
- Manages cluster lifecycle through manager service

**Operator Service** (`cmd/operator/main.go` → `pkg/operator/`)
- REST API server (port 8888) that handles workload management
- Uses Gorilla Mux for routing with auth middleware stack:
  - `APIVersionCheckMiddleware`
  - `AWSAuthMiddleware` (AWS credentials validation)
  - `ClientIDMiddleware`
- Two router groups: authenticated (deploy, delete, get) and public (batch/task jobs, metrics)
- Initializes K8s client, Istio client, Prometheus client for metrics
- Runs background crons:
  - `DeleteEvictedPods` (hourly)
  - `ClusterTelemetry` (hourly)
  - `CostBreakdown` (5 minute intervals)
  - `ManageJobResources` for TaskAPI jobs

### 2. Workload Types and Their Execution Patterns

**RealtimeAPI** (`pkg/operator/resources/realtimeapi/`)
- Persistent Deployment + VirtualService + Service
- Routes through activator (port 8000) which manages request buffering
- Horizontal Pod Autoscaling (HPA) based on in-flight request metrics
- Uses Istio for traffic routing and service discovery
- Always-on minimal replicas, scales up on incoming requests
- No queue; direct request/response pattern
- Endpoint format: `/APINAME` via Istio Gateway (`apis-gateway`)

**AsyncAPI** (`pkg/operator/resources/asyncapi/`)
- Persistent Deployment + VirtualService + SQS FIFO Queue (AWS)
- Routes through async-gateway service (not activator)
- Dequeuer service consumes from SQS and forwards to API pod
- Autoscales based on queue depth (not in-flight requests)
- Uses dequeuer (separate container) to decouple request acceptance from processing
- Can scale to zero when queue is empty
- Queue URL passed via Istio header: `cortex-queue-url`
- Endpoint format: `/APINAME` (async response, returns job ID)

**BatchAPI** (`pkg/operator/resources/job/batchapi/`)
- Deployed via CRD (Custom Resource Definition) in `pkg/crds/apis/batch/v1alpha1/`
- BatchJob CRD created per submission with apiID reference
- Uses Kubernetes Job objects for worker distribution
- Workers read from S3 input path, write results to S3 output
- Supports dead letter queue (DLQ) for failed items
- TTL-based cleanup of completed jobs
- No autoscaling; static worker count per job
- Submitted via `/batch/{apiName}` endpoint (returns job ID)

**TaskAPI** (`pkg/operator/resources/job/taskapi/`)
- Like BatchAPI but with cron scheduling capability
- Scheduled execution via cron expressions
- Background resource management: `taskapi.ManageJobResources`
- Returns scheduled job ID vs. batch's immediate job ID
- Submitted via `/tasks/{apiName}` endpoint

## Request Flow Architecture

### RealtimeAPI Flow
```
Client → Istio Gateway → VirtualService (activator) → Activator (port 8000)
  ↓
Activator checks: ready replicas > 0? 
  - No: call Autoscaler.Awaken() → scale to 1 → wait for ready
  - Yes: forward request
  ↓
Request → Deployment Pod (user container)
  ↓
Response → Client
```

### AsyncAPI Flow
```
Client → Istio Gateway → VirtualService (async-gateway) → Async Gateway Service (port 8080)
  ↓
Async Gateway: Extract queue URL from Istio header, store request payload in S3
  ↓
Return job ID to client
  ↓
Dequeuer Container (sidecar/separate): Poll SQS queue
  ↓
Forward message to user container (:8000)
  ↓
User container calls back with result → stored in S3
```

### BatchAPI/TaskAPI Flow
```
CLI → Operator POST /batch/{apiName} or /tasks/{apiName}
  ↓
Operator: Parse job config, create BatchJob CRD
  ↓
CRD Controller (pkg/crds/controllers/batch/): Reconciles BatchJob
  ↓
Creates Kubernetes Job with N workers
  ↓
Workers: Read items from S3, process, write results
  ↓
Job completion → Result stored in S3
```

## Traffic Management via Istio

**VirtualService Pattern:**
- All workloads deploy a Istio VirtualService (API resource)
- VirtualService routes `/APINAME` to appropriate destination based on type:
  - RealtimeAPI → activator service (request queuing)
  - AsyncAPI → async-gateway service (SQS integration)
  - Batch/Task → internal Operator endpoints
- Supports traffic splitting for canary deployments via `trafficSplit` field
- Shadow traffic capability via `shadow: true` in traffic weights
- Rewrite rules, header injection (async-gateway adds `cortex-queue-url` header)
- Single Istio Gateway (`apis-gateway`) handles all route matching

**Service Discovery:**
- K8s Services created for each API (Deployment.Service)
- Service labels: `apiName`, `apiKind`, `apiID`, `specID`, `deploymentID`
- Informers watch VirtualServices/Deployments to track API lifecycle

## Autoscaling System

**Architecture** (`pkg/autoscaler/` and `cmd/autoscaler/main.go`)

Autoscaler is a separate service (port 8000) that:
1. Watches Deployments and VirtualServices (Istio informers)
2. Registers `Scaler` implementations per workload kind
3. Polls Prometheus for metrics every `spec.AutoscalingTickInterval` (default: 15s)

**RealtimeAPI Scaling:**
- Metric: in-flight request count (from activator Prometheus metrics)
- Window: last 2 minutes of request data
- Scale decision: replicas = ceil(inFlightRequests / maxConcurrency)
- Min replicas: 0 (can scale to zero)
- Max replicas: from `autoscaling.max_replicas` config

**AsyncAPI Scaling:**
- Metric: SQS queue depth (ApproximateNumberOfMessages)
- Scale decision: replicas = ceil(queueDepth / maxConcurrency)
- Enables zero-cost idle state

**Activator's Role** (`pkg/activator/` and `cmd/activator/main.go`):
- Buffers incoming requests when replicas = 0
- Calls `autoscaler.Awaken()` when first request arrives
- Waits for deployment readiness (via deployment informer)
- Timeout-based request queuing
- Tracks per-API request stats for metrics
- Returns 429 (Too Many Requests) if queue fills

## Kubernetes Integration

**Client Hierarchy:**
```
pkg/config/config.go:
  - K8s: default namespace, authenticated
  - K8sIstio: Istio namespace, for VirtualService/Gateway management
  - K8sAllNamespaces: cluster-wide queries (nodes, costs)
```

**Core K8s Resources:**
- `Deployment`: User container + probes (readiness/liveness/preStop)
- `Service`: ClusterIP for internal routing
- `ConfigMap`: API spec, readiness probe definitions (from `pkg/workloads/`)
- `VirtualService`: Istio routing rules
- `Job`/`CronJob`: BatchAPI/TaskAPI execution
- `HPA`: RealtimeAPI horizontal scaling (legacy, mostly replaced by custom autoscaler)

**K8s Helpers** (`pkg/lib/k8s/`):
- Virtual service builders with support for traffic splitting, rewrites, mirrors
- Deployment/ConfigMap/Service builders with label/annotation management
- Pod readiness tracking via informer
- Resource quantity parsing (CPU, memory, GPU)

## Configuration Model

**Spec System** (`pkg/types/spec/` and `pkg/types/spec/`)
- User config (YAML) → Parsed to `userconfig.API`
- `userconfig.API`: Generic container for all workload types with shared fields:
  - Pod: container definitions, port, max_queue_length, max_concurrency
  - Autoscaling: min/max replicas, target metrics
  - UpdateStrategy: rolling updates, max unavailable
  - NodeGroups: node affinity
  - Networking: endpoint path configuration
- Converted to `spec.API`: Immutable deployment spec with IDs, timestamps
  - SpecID: hash of user config (detects changes)
  - DeploymentID: random per deploy (for rolling updates)
  - InitialDeploymentTime: Unix nano timestamp (used in queue naming)
  - Kind: userconfig.RealtimeAPIKind, AsyncAPIKind, etc.

**Storage:**
- API specs uploaded to S3 at deploy time (fetch path in operator)
- Cluster config stored in ConfigMap `cluster-config` with schema validation
- Deployed resource metadata in Deployment/VirtualService labels

## Design Patterns & Key Concepts

### 1. **Label-Based Discovery**
- All K8s resources labeled with:
  - `apiName`, `apiKind`, `apiID`, `specID`, `deploymentID`
  - `cortex.dev/api: "true"` for filtering
- Enables quick discovery without CRD queries
- Informers use label selectors for efficient watching

### 2. **Immutable Deployment IDs**
- Each deploy gets unique `deploymentID` (first 10 chars of random K8s name)
- Prevents race conditions during rolling updates
- Old pods complete requests before new spec takes effect

### 3. **Dual API Server Pattern**
- Operator (port 8888): Control plane, handles deployments and lifecycle
- Activator/Autoscaler/Dequeuer (separate containers): Data plane, handle requests/metrics
- Enables independent scaling and failure isolation

### 4. **Async Infrastructure Separation**
- AsyncAPI requests don't block on processing
- SQS FIFO queue ensures message ordering
- Dequeuer is independent process (can be container sidecar or separate deployment)
- Decouples request acceptance from processing capacity

### 5. **Workload-Specific Controllers**
- No monolithic controller; each workload type has its module:
  - `realtimeapi.UpdateAPI()`: Creates Deployment, Service, VirtualService
  - `asyncapi.UpdateAPI()`: Creates above + SQS queue, async-gateway routes
  - `batchapi.SubmitJob()`: Creates BatchJob CRD
  - `taskapi.ScheduleJob()`: Creates TaskJob CRD with cron
- Shared validation, spec generation, but distinct deployment strategies

### 6. **VirtualService as API Gateway**
- Single Istio ingress point (`apis-gateway`)
- VirtualService per API for routing and traffic management
- Enables canary deployments, shadow traffic, traffic splitting
- Simpler than multiple Ingress objects, integrates with service mesh observability

### 7. **CRD-Based Job Management**
- BatchJob CRD (v1alpha1) for job state tracking
- Controller reconciles CRD → Kubernetes Job
- Allows users to query job status via `kubectl get batchjobs`
- TTL controller cleans up completed jobs

## Entry Points

| Component | Binary | Role |
|-----------|--------|------|
| **CLI** | cortex | User-facing; deploys YAML, queries APIs, manages cluster |
| **Operator** | cmd/operator/main.go | REST API (8888); deploys workloads, manages lifecycle |
| **Activator** | cmd/activator/main.go | Port 8000; buffers requests, triggers autoscale, routes to pods |
| **Autoscaler** | cmd/autoscaler/main.go | Port 8000; scales APIs based on Prometheus metrics |
| **Async Gateway** | cmd/async-gateway/main.go | Port 8080; accepts async requests, queues to SQS |
| **Dequeuer** | cmd/dequeuer/main.go | Sidecar container; pulls from SQS, calls user endpoint |
| **Proxy** | cmd/proxy/main.go | Pod's main PID 1; supervises user container, reports metrics |
| **Manager** | pkg/crds/main.go | Kubernetes controller manager; reconciles BatchJob CRDs |
| **Enqueuer** | cmd/enqueuer/main.go | Standalone tool; pushes items to SQS queues |

## Typical Deployment Architecture

```
Kubernetes Cluster (EKS)
├── cortex namespace
│   ├── Operator Deployment (manages lifecycle)
│   ├── Activator Deployment (buffers realtime requests)
│   ├── Autoscaler Deployment (scales all APIs)
│   ├── Async Gateway Deployment (accepts async requests)
│   ├── Manager Deployment (reconciles batch jobs)
│   ├── Per-API Deployments (user containers + proxy sidecar)
│   │   ├── ConfigMap (spec, probes)
│   │   ├── Service (routing)
│   │   └── VirtualService (Istio routing)
│   └── Metrics (Prometheus)
├── istio-system namespace
│   ├── Ingress Controller
│   └── apis-gateway (Gateway resource)
└── aws
    ├── SQS FIFO queues (per AsyncAPI)
    └── S3 buckets (specs, batch inputs/outputs)
```

## Key Dependencies

- **Istio**: VirtualService routing, traffic management, mesh observability
- **Kubernetes 1.34+**: Native API, informers, CRDs
- **AWS SDK**: SQS, S3, IAM authentication, CloudWatch metrics
- **Prometheus**: Metrics collection for autoscaling decisions
- **Cobra**: CLI framework
- **Gorilla**: HTTP routing and CORS handling
- **Controller-runtime**: CRD controller framework (manager)

## Notable Implementation Details

1. **Telemetry**: Optional integration with Segment/Sentry for error tracking and analytics
2. **Concurrency Model**: Informers use goroutines; cron jobs manage background tasks
3. **Error Handling**: Wrapped error messages with context; telemetry integration
4. **Logging**: Zap logger (production-ready, structured JSON logs)
5. **Status Tracking**: Separate status objects in specs track deployment progression
6. **Resource Cleanup**: Finalizers and TTLs prevent orphaned resources

---

This architecture enables:
- **Multi-tenant workload isolation** via namespaces and RBAC
- **Cost optimization** through zero-scaling for async/batch
- **Production reliability** with health checks, graceful shutdowns, rolling updates
- **Horizontal scaling** across worker nodes and cloud regions
- **Framework-agnostic** deployment (any containerized workload supported)

## File Structure Overview

### Core Packages

```
cli/                           # CLI commands (deploy, get, delete, logs, cluster)
cmd/                           # Binary entry points for all components
  ├── operator/                # Operator REST API server
  ├── activator/               # Request buffering for realtime APIs
  ├── autoscaler/              # Autoscaling logic
  ├── async-gateway/           # Async request handler
  ├── dequeuer/                # SQS consumer for async APIs
  ├── enqueuer/                # SQS producer utility
  └── proxy/                   # Pod supervisor (PID 1 in user containers)

pkg/                           # Shared packages
  ├── operator/                # Operator implementation
  │   ├── endpoints/           # REST API handlers
  │   ├── resources/           # Workload resource management
  │   │   ├── realtimeapi/     # Realtime API lifecycle
  │   │   ├── asyncapi/        # Async API lifecycle
  │   │   ├── batchapi/        # Batch job management
  │   │   └── taskapi/         # Scheduled job management
  │   └── schema/              # API validation
  ├── autoscaler/              # Autoscaling algorithms
  ├── activator/               # Request buffering logic
  ├── types/                   # Type definitions and specs
  │   ├── spec/                # Internal API specifications
  │   └── userconfig/          # User-facing configuration
  ├── lib/                     # Shared libraries
  │   ├── k8s/                 # Kubernetes helpers
  │   ├── aws/                 # AWS SDK wrappers
  │   ├── telemetry/           # Metrics and error tracking
  │   └── errors/              # Error handling
  ├── crds/                    # Custom Resource Definitions
  │   ├── apis/batch/          # BatchJob/TaskJob CRD schemas
  │   └── controllers/         # CRD controllers
  └── workloads/               # Workload-specific code

images/                        # Dockerfiles for all components
manager/                       # Cluster bootstrap and system manifests
  ├── manifests/               # Kubernetes manifests (istio, prometheus, etc.)
  └── generate_eks.py          # EKS CloudFormation generator

dev/                           # Development scripts
  ├── registry.sh              # Docker image build/push script
  ├── operator_local.sh        # Local operator runner
  └── config/                  # Your local dev config (not in git)

build/                         # CI/CD scripts
test/                          # E2E tests and test utilities
docs/                          # Documentation (docs.cortexlabs.com)
```

## Common Gotchas and Tips

### 1. Version Alignment
- Kubernetes client version in `go.mod` must match EKS cluster version
- Check `dev/versions.md` for upgrade procedures when updating K8s, Istio, or other dependencies
- After upgrading K8s client: `go mod tidy` and verify the diff is reasonable

### 2. Image Registry Workflow
- All images must be pushed to ECR before cluster creation
- Use `make images-all` before first `make cluster-up`
- Development images (operator, activator, autoscaler): `make images-dev` (faster)
- Single image update in cluster: `make operator-update`, `make async-gateway-update`, etc.

### 3. Local vs. In-Cluster Operator
- `make devstart` scales in-cluster operator to 0 and runs locally
- Local operator enables faster iteration (auto-rebuilds on file changes)
- Switch back to in-cluster: `<ctrl+c>` local operator, then `make operator-start`
- Useful for debugging: `make operator-local-dbg` attaches delve on port 2345

### 4. AWS Credentials
- `dev/config/env.sh` is sourced by Makefile's `BASH_ENV`
- Changes require new shell or `source dev/config/env.sh`
- `DEFAULT_USER_ARN` needs EKS IAM permissions (see CONTRIBUTING.md)
- Operator uses dedicated IAM role created during cluster setup

### 5. Go Module Management
- `make tools` may modify `go.mod` and `go.sum` - revert these changes
- Non-versioned modules (k8s.io, istio.io) require specific update procedures
- After dependency updates: always run `go mod tidy` and check the diff
- Docker client uses replace directive: `github.com/docker/docker => github.com/docker/engine v19.03.13`

### 6. Docker Buildx
- Multi-arch builds require buildx initialization:
  ```bash
  docker buildx create --driver-opt image=moby/buildkit:master --name builder \
    --platform linux/amd64,linux/arm64 --use
  docker buildx inspect --bootstrap builder
  ```
- Without buildx, `make images-all` will fail

### 7. Development Environment Setup
- Remote development recommended (EC2) due to frequent registry pushes
- Tools like Mutagen help sync local/remote filesystems
- `rerun` watches files and rebuilds (used in `make devstart`, `make cli-watch`)
- Python dependencies: `python3 -m pip install aiohttp boto3 pyyaml black==20.8b1`

### 8. Debugging and Logs
- Local operator: Check stdout from `make devstart`
- In-cluster operator: `kubectl logs -l workloadID=operator`
- Activator logs: `kubectl logs -l workloadID=activator`
- Autoscaler logs: `kubectl logs -l workloadID=autoscaler`
- User API logs: `cortex logs <api-name>` or `kubectl logs <pod-name>`
- Prometheus metrics: Port-forward to prometheus pod for metric debugging

### 9. Cluster Permissions
- Operator creates IAM role with specific policies during cluster setup
- Development user needs EKS permissions (see `dev/minimum_aws_policy.json`)
- `eksctl create iamidentitymapping` adds your user to cluster RBAC
- If "unauthorized" errors: Check AWS credentials and IAM role mappings

### 10. Testing Workflow
- Unit tests: Fast, run before committing (`make test`)
- E2E tests: Slow, require real cluster (`make test-e2e`)
- E2E setup: See `test/e2e/README.md` for configuration
- Lint before committing: `make lint` (includes golint, looppointer, format checks)
- Manual testing: Deploy examples from `test/apis/` directory

## Important Files

- `Makefile` - All development commands and targets
- `go.mod` - Go dependencies (note K8s and Istio client versions)
- `dev/config/env.sh` - Your AWS credentials (not checked into git)
- `dev/config/cluster.yaml` - Cluster configuration for development
- `dev/versions.md` - Version upgrade procedures and notes
- `dev/registry.sh` - Builds and pushes Docker images
- `dev/operator_local.sh` - Runs operator locally with file watching
- `manager/generate_eks.py` - Generates EKS CloudFormation template
- `CONTRIBUTING.md` - Detailed setup instructions and prerequisites

## Notes on Current Branch (k8s_1.34_update)

This branch is updating Kubernetes from an earlier version to 1.34. Key files being modified:
- `go.mod` - Updated k8s.io/api, k8s.io/apimachinery, k8s.io/client-go to v0.32.3
- `images/*/Dockerfile` - Updated kubectl to 1.34.0
- `dev/versions.md` - Documentation of upgrade process
- All images need to be rebuilt after K8s version changes
