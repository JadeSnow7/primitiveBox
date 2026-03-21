# Phase 2 Demo: Application Primitive Protocol

This is the canonical maintainer validation path for Phase 2.

It proves:

- adapter registration through `app.register`
- unified primitive discovery through `/primitives` and `pb primitives list`
- successful dispatch through the runtime
- deliberate adapter-side failure semantics
- crash -> unavailable -> reconnect -> active lifecycle
- declared verify/rollback metadata visibility for app primitives

## Manual 60-second Validation

Start the sandbox-local runtime daemon:

```bash
./bin/pb-runtimed \
  --host 127.0.0.1 \
  --port 8080 \
  --workspace /tmp/pb-phase2-workspace \
  --data-dir /tmp/pb-phase2-data
```

Start the minimal protocol-validation adapter in another terminal:

```bash
./bin/pb-test-adapter \
  --socket /tmp/pb-test-app.sock \
  --rpc-endpoint http://127.0.0.1:8080
```

Verify registration, metadata, and dispatch:

```bash
./bin/pb --endpoint http://127.0.0.1:8080 primitives list
./bin/pb --endpoint http://127.0.0.1:8080 primitives schema demo.set --json
./bin/pb --endpoint http://127.0.0.1:8080 rpc demo.echo --params '{"message":"hello"}'
./bin/pb --endpoint http://127.0.0.1:8080 rpc demo.fail --params '{"reason":"deliberate"}'
curl http://127.0.0.1:8080/app-primitives
```

The expected signals are:

- `demo.echo`, `demo.fail`, and `demo.set` appear in `pb primitives list`
- app primitives show `SOURCE=app`, `STATUS=active`, and `ADAPTER=pb-test-adapter`
- `pb primitives schema demo.set --json` shows the declared `verify` and `rollback` blocks
- `demo.echo` returns structured JSON
- `demo.fail` exits non-zero and includes `deliberate failure: deliberate`

To verify crash handling, stop `pb-test-adapter` and call `demo.echo` again:

```bash
./bin/pb --endpoint http://127.0.0.1:8080 rpc demo.echo --params '{"message":"after-crash"}'
./bin/pb --endpoint http://127.0.0.1:8080 primitives list
```

Expected result:

- the call fails fast with `adapter pb-test-adapter is unavailable`
- `pb primitives list` shows `STATUS=unavailable`

Restart `pb-test-adapter` with the same command and call `demo.echo` again:

```bash
./bin/pb --endpoint http://127.0.0.1:8080 rpc demo.echo --params '{"message":"back"}'
```

Expected result:

- the adapter re-registers
- `STATUS` returns to `active`
- `demo.echo` succeeds again

`demo.*` is used intentionally because `test.*` is a reserved system namespace and app primitives must not register inside it.

## One-Command Proof

Run the black-box smoke:

```bash
python3 tests/e2e/app_protocol_smoke.py
```

This script builds or reuses `pb`, `pb-runtimed`, and `pb-test-adapter`, then exercises the full register -> list -> call -> fail -> unavailable -> reconnect flow automatically.
