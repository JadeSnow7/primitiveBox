# PrimitiveBox Quickstart

This guide takes you from zero to a running PrimitiveBox instance with the demo data pack installed, the Reviewer Gate exercised, and a replay demonstrated — all in under 10 minutes.

## Prerequisites

- Go 1.22+
- Docker (for sandbox mode) or a Kubernetes cluster
- Python 3.10+ with `pip`
- Node 18+ (for the web workspace UI)

---

## Step 1 — Build and start the backend

```bash
git clone https://github.com/JadeSnow7/primitivebox
cd primitivebox
make build
```

This produces all binaries in `bin/`, including `pb`, `pb-os-adapter`, `pb-data-adapter`, and `pb-mcp-bridge`.

Start the host gateway against a local workspace:

```bash
./bin/pb server start --workspace .
```

You should see:

```
[gateway] Listening on :8080
[pkgmgr] Loaded 0 installed packages
```

Verify it is healthy:

```bash
curl http://localhost:8080/health
# → {"status":"ok"}
```

---

## Step 2 — Start the web workspace UI

In a second terminal:

```bash
cd web
npm install
npm run dev
```

Open `http://localhost:5173` in your browser. You will see the AI Command Bar and an empty workspace panel grid.

---

## Step 3 — Install the demo data pack

The `data-pack` ships a SQLite-backed adapter that registers three primitives: `data.schema`, `data.query`, and `data.insert`.

```bash
./bin/pb install data-pack --boxfile examples/data-pack/Boxfile
```

The installer:
1. Launches `bin/pb-data-adapter` with the configured socket and database path
2. Polls until `data.schema` appears in the app primitive registry
3. Runs the healthcheck (`data.schema`) — must succeed within 5 s
4. Persists the install record to `.primitivebox/packages.db`

Confirm the primitives are live:

```bash
curl http://localhost:8080/app-primitives | python3 -m json.tool
# → data.schema, data.query, data.insert listed with intent metadata
```

Seed the database (one-time):

```bash
sqlite3 .pb/data-pack.db < examples/data-pack/seed.sql
```

Query it:

```bash
curl -s -X POST http://localhost:8080/rpc \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"data.query","params":{"sql":"SELECT * FROM products"},"id":1}' \
  | python3 -m json.tool
```

---

## Step 4 — Experience the Reviewer Gate

`data.insert` is declared `risk_level: high, reversible: false`. Any agent attempt to call it will pause orchestration and require human approval before the write executes.

In the web UI, type into the AI Command Bar:

```
Add a product named "Widget Pro" priced at 49.99 to the products table
```

Watch the workspace:

1. The orchestrator emits a `plan` step and an `execution` entry with `pending_review` status
2. The workspace enters `AWAITING_REVIEW` state — a **ReviewerPanel** modal appears
3. The panel shows:
   - Method: `data.insert`
   - Risk: `high / irreversible`
   - Proposed params: `{ "table": "products", "values": { "name": "Widget Pro", "price": 49.99 } }`
4. Click **Approve** — the exact params shown are dispatched to the adapter (unchanged)
5. The timeline records `execution.result` and the agent loop continues

Click **Reject** instead to see the rejection path: the timeline records `execution.rejected`, the agent receives an error outcome, and the orchestrator decides whether to replan or abort.

---

## Step 5 — Experience the Replay engine

The replay engine lets you re-run any past orchestrator group without side effects, using cached results for execution calls and simulating UI primitives in a read-only mode.

After completing step 4, find the group ID in the timeline panel (the label next to the most recent plan block).

In the web UI, click the replay icon on that group entry. Or from the command bar:

```
Replay the last action
```

The replay engine will:
1. Load the `plan`, `execution`, and `ui` entries for that group from the timeline store
2. Re-emit UI primitives (panel opens, layout splits) without re-calling the backend
3. Show `simulated` badges on execution entries that were replayed from cache
4. Skip the Reviewer Gate (replay uses the recorded approval decision)

The workspace state after replay is visually identical to the original run, but no data was written.

---

## Step 6 — Run the automated smoke test

To verify everything end-to-end in CI or after a rebuild:

```bash
python3 tests/e2e/data_pack_smoke.py
```

Expected output:

```
[1/9] Gateway health ...          PASS
[2/9] data-pack install ...       PASS
[3/9] data.schema ...             PASS
[4/9] data.query (empty) ...      PASS
[5/9] Seed products ...           PASS
[6/9] data.query (rows) ...       PASS
[7/9] data.insert ...             PASS
[8/9] SELECT-only enforcement ... PASS
[9/9] Remove data-pack ...        PASS
All 9 checks passed in <60s
```

---

## Next Steps

- **Add your own adapter** — copy `cmd/pb-data-adapter/main.go`, swap the primitive handlers, write a Boxfile, and `pb install` it. The protocol is documented in `docs/arch/02_app_primitive_protocol.md`.
- **Connect an MCP server** — `pb install mcp-bridge --mcp-server <transport>` wraps any MCP-compatible server as typed primitives automatically.
- **Run in Kubernetes** — swap `--driver docker` for `--driver kubernetes` in `pb sandbox create`. See `internal/sandbox/kubernetes.go` for the driver implementation.
- **Explore the architecture** — start with `docs/VISION.md` for the phase-by-phase design rationale.
