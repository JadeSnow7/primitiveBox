# PrimitiveBox

PrimitiveBox is a host-side JSON-RPC gateway for agent workflows. It supports:

- host workspace primitives at `/rpc`
- Docker-backed sandboxes that run their own `pb server` inside the container
- a SQLite-backed control plane for sandbox metadata and inspector events
- SSE streaming at `/rpc/stream` and `/sandboxes/{id}/rpc/stream`
- gateway proxy routes such as `/sandboxes/{id}/rpc`

The current repo now supports local workspace mode, Docker-backed sandboxes, a real Kubernetes runtime driver, sandbox-only `db.*` and `browser.*` primitives, and an embedded Inspector UI.

## Supported Modes

### Host workspace mode

```bash
./bin/pb server start --workspace ./my-project
```

Clients call `http://localhost:8080/rpc`.

### Docker sandbox gateway mode

1. Build the CLI:

```bash
make build
```

2. Build the sandbox image:

```bash
make sandbox-image
```

3. Create a sandbox:

```bash
./bin/pb sandbox create --driver docker --mount ./my-project --ttl 3600 --network-mode none
```

4. Start the host gateway:

```bash
./bin/pb server start --workspace .
```

5. Connect from Python:

```python
from primitivebox import PrimitiveBoxClient

client = PrimitiveBoxClient("http://localhost:8080", sandbox_id="sb-12345678")
print(client.health())
print(client.fs.read("README.md"))
for event in client.shell.stream_exec("printf 'hello\\n'"):
    print(event)
```

### Kubernetes sandbox mode

PrimitiveBox now exposes a `kubernetes` runtime driver behind the same `pb sandbox create --driver kubernetes ...` CLI surface.

The current implementation:

- creates Pod, Service, PVC, and optional `NetworkPolicy` resources
- waits for Pod readiness and then establishes a local `port-forward`
- persists runtime metadata in the same SQLite control plane
- supports `pods/exec` and TTL-driven remote cleanup

`--mount` is intentionally unsupported for Kubernetes sandboxes in v1; workspaces are PVC-backed.

### Inspector UI

Start the gateway with the embedded SPA:

```bash
./bin/pb server start --workspace . --ui
```

Then open `http://localhost:8080/`.

## CLI

- `pb version`
- `pb server start`
- `pb sandbox create`
- `pb sandbox list`
- `pb sandbox inspect <id>`
- `pb sandbox stop <id>`
- `pb sandbox destroy <id>`

Notable `pb sandbox create` flags:

- `--driver docker|kubernetes`
- `--namespace default`
- `--ttl 3600`
- `--idle-ttl 900`
- `--network-mode none|full|policy`
- `--network-host example.com`
- `--network-cidr 10.0.0.0/24`
- `--network-port 443`

`pb server start` notable flags:

- `--ui`

## HTTP Routes

- `POST /rpc`
- `POST /rpc/stream`
- `GET /health`
- `GET /primitives`
- `GET /sandboxes`
- `GET /sandboxes/{id}`
- `POST /sandboxes/{id}/rpc`
- `POST /sandboxes/{id}/rpc/stream`
- `GET /sandboxes/{id}/health`
- `GET /sandboxes/{id}/primitives`
- `GET /api/v1/sandboxes`
- `GET /api/v1/sandboxes/{id}`
- `GET /api/v1/sandboxes/{id}/tree`
- `GET /api/v1/sandboxes/{id}/checkpoints`
- `GET /api/v1/events`
- `GET /api/v1/events/stream`

## Testing

```bash
go build ./...
go test ./...
python3 -m pytest sdk/python/tests -q
```

Docker integration requires local Docker daemon access and a built sandbox image.

Browser automation requires the browser-capable sandbox image:

```bash
make sandbox-browser-image
```

## Notes

- Sandbox registry is stored at `~/.primitivebox/sandboxes/`.
- Control-plane state is now stored at `~/.primitivebox/controlplane.db`; legacy JSON registries are imported once on startup.
- Docker networking currently supports coarse `none/full` intent metadata. Fine-grained allowlists are designed for the Kubernetes driver first.
- Container-local `pb server` listens on port `8080` and is mapped to a random localhost port.
- `AsyncPrimitiveBoxClient` is implemented via `httpx`, and both sync/async SDKs now support streaming calls.
- `db.*` and `browser.*` are sandbox-only by default and are not registered in host workspace mode.
