# Role & Context
You are a Staff-Level AI Software Engineer working on **PrimitiveBox**, an AI-Native Execution Runtime. 

We have successfully completed Phase P1 (Dynamic UI, Entity System, Reviewer Gate) and effectively expanded our data boundaries with Database, Browser, and Document primitives (`db.*`, `browser.*`, `doc.*`).

We are now facing the final and most architecturally demanding challenge of Phase P2: **Kubernetes Runtime Deployment (真实的分布式调度落位)**.

### The Problem
PrimitiveBox currently relies entirely on a local `DockerDriver` (or simple process execution) to run its isolated sandboxes and Unix-socket app adapters. While acceptable for a single-node Command Center, it completely fails to scale to a true infrastructure-level OS where we need to orchestrate distributed primitives across a cluster, enforce strict hardware resource quotas, and leverage native zero-trust network isolation.

### Your Objective
Your mission is to complete the skeleton implementation of the **Kubernetes Sandbox Driver**. You must elevate PrimitiveBox so that when the Control Plane asks to spin up a new Sandbox or install an App Adapter, it natively spawns a Kubernetes Pod, configures its persistent volumes, and wires up a secure port-forward/exec tunnel to proxy the Agent's primitive calls.

# Task Breakdown

### Task 1: Complete the RuntimeDriver Implementation
- Examine the existing `internal/sandbox/kubernetes.go` skeleton.
- Implement the `Create`, `Start`, `Stop`, and `Destroy` methods adhering exactly to the `RuntimeDriver` Go interface.
- **Mapping Metadata**: Translate our `SandboxConfig` abstract intent into raw Kubernetes manifest structures:
  - Docker Image ➔ `Pod.Spec.Containers[0].Image`
  - Workspace Path ➔ `PersistentVolumeClaim` + `VolumeMounts`
  - Memory/CPU Limits ➔ `Container.Resources.Limits`

### Task 2: Advanced Network Isolation (NetworkPolicy)
- In the Docker driver, our network policy was coarse. In Kubernetes, you must implement true zero-trust.
- When `SandboxConfig.NetworkMode` specifies isolated egress (e.g., allowing only `tcp:5432` for a Postgres adapter), the driver must dynamically generate and apply a corresponding `NetworkPolicy` resource strictly bound to the target Pod's labels. 
- *Crucial*: This ensures a compromised Agent working in the sandbox literally cannot dial out to unauthorized IP addresses at the Linux kernel level.

### Task 3: The Communication Bridge (Proxying the Event Stream)
- The toughest distributed challenge: The control-plane Gateway must communicate with the isolated `pb server` (or an app adapter) running *inside* the Pod, usually over a Unix socket or local HTTP.
- **Implementation**: Implement a native Kubernetes port-forwarding tunnel or `kubectl exec`-style stream multiplexer under the hood inside `kubernetes.go`. 
- The Gateway's routing layer (`internal/rpc/`) must not need to know whether the sandbox is running via Docker or K8s; the Driver must seamlessly present an accessible local socket/endpoint.

# Architectural Constraints (MUST FOLLOW)
1. **Interface Adherence**: Do NOT modify the `RouterDriver` or core manager orchestration loop in `internal/sandbox/manager.go`. Your additions must be physically constrained to fulfilling the interface contract in the `kubernetes` package.
2. **K8s Client Go**: Rely purely on the official `client-go` library for generating manifests and interacting with the cluster. No shelling out to `kubectl`.
3. **Graceful Handling of Unavailability**: If the cluster is unreachable or no Kubeconfig is present, the driver must fail gracefully during initialization (`pb` booting) and clearly signal its lack of capabilities, rather than crashing the Gateway.

# Acceptance Criteria
- [ ] A local test cluster (e.g., Minikube/Kind) is running.
- [ ] Running `pb sandbox create --driver kubernetes --workspace /tmp/data` successfully creates a Pod and a PVC in the cluster.
- [ ] Primitive calls (e.g., `pb rpc shell.exec`) correctly route over the K8s API server tunnel and execute inside the Pod.
- [ ] Applying a restrict-network flag verifiably blocks the Pod from making external HTTP requests, proven by an internal `curl` failure.
