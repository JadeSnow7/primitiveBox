# PrimitiveBox

PrimitiveBox is a host-side JSON-RPC gateway for agent workflows. It supports:

- host workspace primitives at `/rpc`
- Docker-backed sandboxes that run their own `pb server` inside the container
- gateway proxy routes such as `/sandboxes/{id}/rpc`

The current repo now supports both local workspace mode and the first Docker sandbox gateway iteration.

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
./bin/pb sandbox create --mount ./my-project
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
```

## CLI

- `pb version`
- `pb server start`
- `pb sandbox create`
- `pb sandbox list`
- `pb sandbox inspect <id>`
- `pb sandbox stop <id>`
- `pb sandbox destroy <id>`

## HTTP Routes

- `POST /rpc`
- `GET /health`
- `GET /primitives`
- `GET /sandboxes`
- `GET /sandboxes/{id}`
- `POST /sandboxes/{id}/rpc`
- `GET /sandboxes/{id}/health`
- `GET /sandboxes/{id}/primitives`

## Testing

```bash
go build ./...
go test ./...
python3 -m pytest sdk/python/tests -q
```

Docker integration requires local Docker daemon access and a built sandbox image.

## Notes

- Sandbox registry is stored at `~/.primitivebox/sandboxes/`.
- Sandbox containers run with `--network none` in Iteration 1.
- Container-local `pb server` listens on port `8080` and is mapped to a random localhost port.
- `AsyncPrimitiveBoxClient` remains intentionally unimplemented.
