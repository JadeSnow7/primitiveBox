# KV Adapter Example

`examples/kv_adapter/` is a minimal dynamic app adapter used to stress-test PrimitiveBox's current application primitive registration protocol.

It exposes 9 primitives:

- `kv.get`
- `kv.set`
- `kv.delete`
- `kv.list`
- `kv.exists`
- `kv.batch_set`
- `kv.export`
- `kv.import`
- `kv.verify`

## Build

```bash
go build -o ./bin/kv-adapter ./examples/kv_adapter
```

## Run Against A Sandbox-Local PB Server

```bash
./bin/kv-adapter \
  --socket /tmp/pb-kv.sock \
  --manifest ./examples/kv_adapter/manifest.json \
  --rpc-endpoint http://127.0.0.1:8081
```

Useful flags:

- `--socket`: Unix socket used for request/response dispatch.
- `--manifest`: manifest file to register.
- `--rpc-endpoint`: sandbox-local PrimitiveBox HTTP endpoint used for `app.register`.
- `--app-id`: override `app_id` across all registered primitives.
- `--namespace`: override the primitive name prefix.
- `--backend=memory|sqlite`: choose storage backend.
- `--db-path`: SQLite database file when `--backend=sqlite`. If omitted, the adapter derives a stable default file from the Unix socket path so different adapter instances do not share one SQLite database implicitly.
- `--no-register`: only serve the Unix socket, skip registration.

## Test Controls

Most primitives accept an optional `test_control` object so the validation suite can probe timeout and error behavior without modifying runtime code:

```json
{
  "delay_ms": 250,
  "error_code": 4091,
  "error_message": "simulated adapter error"
}
```

## Validation Suite

Run the protocol stress test:

```bash
go test ./examples/kv_adapter -v -run TestValidation
```
