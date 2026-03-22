# Phase 3 Demo: `pb-os-adapter` Process Foundation

This is the canonical maintainer validation path for Phase 3 Milestone 1.

It proves:

- a real Phase 3 reference adapter can register through the existing app protocol
- `process.*` primitives appear in unified primitive discovery
- PrimitiveBox can spawn, wait on, gracefully terminate, and force-kill adapter-managed OS processes
- the safety boundary is strict: mutation primitives only operate on processes spawned through this adapter

It intentionally does not prove:

- `service.*`, `pkg.*`, or `net.*`
- app-level checkpoint / rollback integration
- persistence or reattachment across adapter restart
- arbitrary host-wide PID management

## Manual 60-second Validation

Build the binaries:

```bash
make build
```

Start the sandbox-local runtime daemon:

```bash
./bin/pb-runtimed \
  --host 127.0.0.1 \
  --port 8080 \
  --workspace /tmp/pb-phase3-workspace \
  --data-dir /tmp/pb-phase3-data
```

Start the Phase 3 OS adapter in another terminal:

```bash
./bin/pb-os-adapter \
  --socket /tmp/pb-os.sock \
  --rpc-endpoint http://127.0.0.1:8080
```

Verify primitive discovery:

```bash
./bin/pb --endpoint http://127.0.0.1:8080 primitives list
./bin/pb --endpoint http://127.0.0.1:8080 primitives schema process.spawn --json
```

Expected result:

- `process.list`, `process.spawn`, `process.wait`, `process.terminate`, and `process.kill` appear in `pb primitives list`
- these rows show `SOURCE=app`, `STATUS=active`, and `ADAPTER=pb-os-adapter`

Spawn a short-lived process and wait for it:

```bash
./bin/pb --endpoint http://127.0.0.1:8080 rpc process.spawn --params '{"command":["sleep","1"]}'
./bin/pb --endpoint http://127.0.0.1:8080 rpc process.wait --params '{"process_id":"proc-1","timeout_s":5}'
```

List known adapter-managed processes:

```bash
./bin/pb --endpoint http://127.0.0.1:8080 rpc process.list --params '{}'
```

Spawn a long-lived process and terminate it gracefully:

```bash
./bin/pb --endpoint http://127.0.0.1:8080 rpc process.spawn --params '{"command":["sleep","30"]}'
./bin/pb --endpoint http://127.0.0.1:8080 rpc process.terminate --params '{"process_id":"proc-2"}'
./bin/pb --endpoint http://127.0.0.1:8080 rpc process.wait --params '{"process_id":"proc-2","timeout_s":5}'
```

Spawn another long-lived process and force-kill it:

```bash
./bin/pb --endpoint http://127.0.0.1:8080 rpc process.spawn --params '{"command":["sleep","30"]}'
./bin/pb --endpoint http://127.0.0.1:8080 rpc process.kill --params '{"process_id":"proc-3"}'
./bin/pb --endpoint http://127.0.0.1:8080 rpc process.wait --params '{"process_id":"proc-3","timeout_s":5}'
```

Run the black-box smoke:

```bash
python3 tests/e2e/os_adapter_smoke.py
```

## Notes

- `process.list` only shows adapter-managed processes, not host-wide processes.
- `process.wait`, `process.terminate`, and `process.kill` reject unknown `process_id` values.
- The process registry is in-memory only for Milestone 1. If `pb-os-adapter` restarts or crashes, previously returned `process_id` values are lost and are not reattached.
