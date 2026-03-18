# PrimitiveBox CLI 调试方案

## 设计目标

在 agent 自动跑之前，开发者需要能手动逐步执行每个 primitive，检查 CVR 状态，确认运行时行为符合预期。这套 CLI 方案分三层：

1. **`pb rpc`** — 手动调用任意 primitive，验证单个原语行为
2. **`pb trace`** — 检查事件流和 CVR 决策路径
3. **`pb demo`** — 一键跑 / 单步跑完整 demo 场景

---

## 一、手动 Primitive 调用：`pb rpc`

### 命令设计

```bash
# 基本格式
pb rpc <primitive> [--param key=value ...] [--sandbox <id>] [--json]

# 简写：常用 primitive 有 alias
pb fs read   --path ./buggy_calc/calc.go
pb fs write  --path ./buggy_calc/calc.go --content-file /tmp/fixed.go
pb fs list   --path ./buggy_calc/
pb fs search --pattern "return a - b"
pb shell     --command "cd buggy_calc && go test -v ./..."
pb checkpoint create  --label "pre-fix-001"
pb checkpoint list
pb checkpoint restore --id chk-xxxxxxxx
pb verify test --command "go test -v ./..." --working-dir ./buggy_calc
```

### 实现要点

```go
// cmd/pb/rpc.go — 新增子命令

// pb rpc 直接构造 JSON-RPC 请求发往 gateway
// --sandbox 指定时走 /sandboxes/{id}/rpc，否则走 /rpc
// --json 输出原始 JSON，否则做 human-friendly 格式化
// --stream 走 /rpc/stream，SSE 实时输出

var rpcCmd = &cobra.Command{
    Use:   "rpc <category.action>",
    Short: "Invoke a primitive directly",
    Example: `  pb rpc fs.read --param path=./calc.go
  pb rpc shell.exec --param command="go test ./..." --stream
  pb rpc checkpoint.create --param label=my-save`,
}
```

### 快捷子命令注册

```go
// cmd/pb/fs.go
var fsCmd = &cobra.Command{Use: "fs", Short: "Filesystem primitives"}
var fsReadCmd = &cobra.Command{
    Use:   "read",
    Short: "Read a file",
    RunE: func(cmd *cobra.Command, args []string) error {
        path, _ := cmd.Flags().GetString("path")
        return invokeRPC("fs.read", map[string]any{"path": path})
    },
}

// cmd/pb/checkpoint.go
var checkpointCmd = &cobra.Command{Use: "checkpoint", Aliases: []string{"cp"}, Short: "Checkpoint primitives"}

// cmd/pb/shell.go — shell 比较特殊，支持 --stream 实时输出
var shellCmd = &cobra.Command{
    Use:   "shell",
    Short: "Execute shell command",
    RunE: func(cmd *cobra.Command, args []string) error {
        command, _ := cmd.Flags().GetString("command")
        stream, _ := cmd.Flags().GetBool("stream")
        if stream {
            return invokeRPCStream("shell.exec", map[string]any{"command": command})
        }
        return invokeRPC("shell.exec", map[string]any{"command": command})
    },
}
```

---

## 二、事件检查：`pb trace`

### 命令设计

```bash
# 查看最近 N 条事件
pb trace list [--last 20] [--type cvr.*] [--sandbox <id>]

# 跟踪实时事件流（SSE）
pb trace watch [--type primitive.*,cvr.*] [--sandbox <id>]

# 查看某个 checkpoint 的完整 CVR 链路
pb trace inspect <checkpoint-id>

# 导出事件用于回放
pb trace export --from <event-id> --to <event-id> --output trace.json

# 回放已导出的事件（dry-run，不执行）
pb trace replay trace.json [--speed 2x] [--pause-on cvr.recover]
```

### 输出格式设计

```bash
$ pb trace watch

─── PrimitiveBox Event Stream ──────────────────────────────
 14:23:01.003  ▶ fs.read         path=./buggy_calc/BUG_REPORT.md
 14:23:01.005  ✓ fs.read         287 bytes  (2ms)
 14:23:01.120  ▶ shell.exec      cd buggy_calc && go test -v ./...
 14:23:02.340  ✓ shell.exec      exit=1  (1220ms)
 14:23:02.500  📌 checkpoint     chk-a1b2c3d4  label="pre-fix-bug-001"
 14:23:02.510  ▶ fs.write        path=./buggy_calc/calc.go  (risk: Medium)
 14:23:02.515  ✓ fs.write        written  (5ms)
 14:23:02.600  ▶ verify.test     go test -v ./...
 14:23:03.800  ✅ verify.pass    6/6 tests passed  (1200ms)
────────────────────────────────────────────────────────────

$ pb trace watch --type cvr.*

─── CVR Events Only ────────────────────────────────────────
 14:23:02.500  📌 cvr.checkpoint   chk-a1b2c3d4  "pre-fix-bug-001"
 14:23:03.800  ✅ cvr.verify.pass  6/6 passed
 14:25:10.300  📌 cvr.checkpoint   chk-e5f6g7h8  "pre-fix-bug-002"
 14:25:12.100  ❌ cvr.verify.fail  5/6 passed, 1 failed: TestDivideByZero
 14:25:12.150  ↩  cvr.recover      action=rollback → chk-e5f6g7h8
 14:25:12.200  📌 cvr.checkpoint   chk-i9j0k1l2  "pre-fix-bug-002-attempt-2"
 14:25:14.500  ✅ cvr.verify.pass  6/6 passed
────────────────────────────────────────────────────────────
```

### `pb trace inspect` — 单个 CVR 链路详情

```bash
$ pb trace inspect chk-e5f6g7h8

─── CVR Trace: chk-e5f6g7h8 ───────────────────────────────
 Checkpoint
   ID:       chk-e5f6g7h8
   Label:    pre-fix-bug-002
   Sandbox:  sb-12345678
   Created:  2026-03-17T14:25:10.300Z

 Mutations (between checkpoint and verify)
   1. fs.write  ./buggy_calc/calc.go
      - Line 17: return 0, nil  →  return 0, fmt.Errorf("divide by zero")
      - Risk: Medium | Reversible: true

 Verify
   Command:  go test -v ./...
   Result:   FAIL
   Passed:   5/6
   Failed:   TestDivideByZero
     calc_test.go:29: Divide(10,0) should return error, got nil
     (NOTE: 修复引入了 fmt 但未 import)

 Recovery Decision
   Strategy:  rollback
   Reason:    same test still failing after fix
   Target:    chk-e5f6g7h8
   Duration:  45ms

 Next Attempt
   Checkpoint: chk-i9j0k1l2  "pre-fix-bug-002-attempt-2"
────────────────────────────────────────────────────────────
```

---

## 三、Demo 运行：`pb demo`

### 命令设计

```bash
# 自动模式 — 一键跑完
pb demo run auto-fix-bug [--workspace ./testdata/buggy_calc]

# 单步模式 — 每个 primitive 执行前暂停，按 Enter 继续
pb demo run auto-fix-bug --step

# 断点模式 — 在指定事件类型处暂停
pb demo run auto-fix-bug --break-on cvr.verify.fail,cvr.recover

# dry-run — 只展示 agent 的决策，不实际执行 primitive
pb demo run auto-fix-bug --dry-run

# 指定模型
pb demo run auto-fix-bug --model claude-sonnet-4-20250514

# 列出可用 demo
pb demo list
```

### 单步模式输出

```bash
$ pb demo run auto-fix-bug --step

══════════════════════════════════════════════════════════════
  PrimitiveBox Demo: auto-fix-bug
  Mode: step-by-step | Model: claude-sonnet-4-20250514
══════════════════════════════════════════════════════════════

[Phase 0: Understand]

  Agent wants to invoke:
    fs.read  path="./buggy_calc/BUG_REPORT.md"
    Risk: None | Reversible: n/a

  ▸ Press Enter to execute, 's' to skip, 'q' to quit...

  ✓ fs.read  287 bytes (2ms)

  Agent wants to invoke:
    shell.exec  command="cd buggy_calc && go test -v ./..."
    Risk: High | Reversible: No
    ⚠ CVR: will auto-checkpoint before execution

  ▸ Press Enter to execute, 's' to skip, 'q' to quit...

  ✓ shell.exec  exit_code=1 (1220ms)
    --- FAIL: TestAdd (0.00s)
        calc_test.go:9: Add(2,3) = -1, want 5
    --- FAIL: TestDivideByZero (0.00s)
        calc_test.go:29: Divide(10,0) should return error, got nil
    FAIL

  Agent hypothesis: "BUG-001 is an operator error: a-b should be a+b"

[Phase 1: Checkpoint]

  Agent wants to invoke:
    checkpoint.create  label="pre-fix-bug-001-operator"
    Risk: None

  ▸ Press Enter to execute...

  📌 Checkpoint created: chk-a1b2c3d4

[Phase 2: Execute]

  Agent wants to invoke:
    fs.write  path="./buggy_calc/calc.go"
    Risk: Medium | Reversible: Yes
    Diff preview:
      - return a - b // BUG-001: should be a + b
      + return a + b

  ▸ Press Enter to execute, 'd' for full diff, 's' to skip...

  ✓ fs.write  (5ms)

[Phase 3: Verify]

  Agent wants to invoke:
    verify.test  command="go test -v ./..."
    Risk: None

  ▸ Press Enter to execute...

  Test Results:
    ✓ TestAdd           PASS
    ✓ TestAddNegative   PASS
    ✓ TestDivide        PASS
    ✗ TestDivideByZero  FAIL  ← expected (BUG-002 not fixed yet)
    ✓ TestSqrt          PASS
    ✓ TestSqrtNegative  PASS
    Result: 5/6 passed

  Agent: "BUG-001 fixed. Moving to BUG-002."

══════════════════════════════════════════════════════════════
```

---

## 四、完整调试 Session 示例

一个从零开始的调试流程，按实际操作顺序：

```bash
# ──────────────────────────────────────────────────
# Step 1: 启动 server，挂载 buggy 项目
# ──────────────────────────────────────────────────
make build
./bin/pb server start --workspace ./testdata/buggy_calc --port 8080

# ──────────────────────────────────────────────────
# Step 2: 确认 primitive 注册正确
# ──────────────────────────────────────────────────
pb primitives list

# 预期输出:
#   fs.read          ReadOnly   Risk:None
#   fs.write         Mutation   Risk:Medium  Reversible:true
#   fs.list          ReadOnly   Risk:None
#   fs.search        ReadOnly   Risk:None
#   shell.exec       Mutation   Risk:High   Reversible:false
#   checkpoint.create ReadOnly  Risk:None
#   checkpoint.restore Mutation Risk:High   Reversible:false
#   checkpoint.list  ReadOnly   Risk:None
#   verify.test      ReadOnly   Risk:None

# ──────────────────────────────────────────────────
# Step 3: 手动验证单个 primitive
# ──────────────────────────────────────────────────

# 读文件
pb fs read --path ./buggy_calc/calc.go

# 搜索 bug
pb fs search --pattern "return a - b"
# → calc.go:11: return a - b // BUG-001

# 跑测试，确认 baseline 失败
pb shell --command "cd buggy_calc && go test -v ./..." --stream
# → 2 FAIL, 4 PASS

# ──────────────────────────────────────────────────
# Step 4: 手动走一遍 CVR 循环
# ──────────────────────────────────────────────────

# Checkpoint
pb checkpoint create --label "manual-debug-baseline"
# → chk-aaaabbbb

# 手动修一个 bug — 写入修复后的文件
pb fs write --path ./buggy_calc/calc.go --content-file ./fixed_calc.go

# Verify
pb verify test --command "go test -v ./..." --working-dir ./buggy_calc
# → 看结果

# 如果修错了，Restore
pb checkpoint restore --id chk-aaaabbbb
# → workspace 回到修改前

# 再确认恢复成功
pb fs read --path ./buggy_calc/calc.go | head -15
# → 应该看到原始的 buggy 代码

# ──────────────────────────────────────────────────
# Step 5: 检查 CVR 事件
# ──────────────────────────────────────────────────

# 看刚才产生的事件
pb trace list --last 10
# → 应该看到 checkpoint.create / fs.write / verify.test / checkpoint.restore

# 看 checkpoint 详情
pb trace inspect chk-aaaabbbb

# ──────────────────────────────────────────────────
# Step 6: 用 agent 自动跑，单步模式
# ──────────────────────────────────────────────────

# 先 restore 到干净状态
pb checkpoint restore --id chk-aaaabbbb

# 单步模式跑 agent demo
export ANTHROPIC_API_KEY="sk-ant-..."
pb demo run auto-fix-bug --step

# 每步都能看到 agent 要做什么，按 Enter 放行
# 在 verify.fail 时可以手动检查状态

# ──────────────────────────────────────────────────
# Step 7: 用 sandbox 模式跑（隔离环境）
# ──────────────────────────────────────────────────

# 构建沙箱镜像
make sandbox-image

# 创建沙箱
pb sandbox create \
  --driver docker \
  --mount ./testdata/buggy_calc \
  --ttl 3600 \
  --network-mode none
# → sb-12345678

# 在沙箱里跑 primitive
pb fs read --path ./buggy_calc/calc.go --sandbox sb-12345678

# 在沙箱里跑 demo
pb demo run auto-fix-bug --sandbox sb-12345678 --step

# 看沙箱状态
pb sandbox inspect sb-12345678

# 结束
pb sandbox stop sb-12345678
pb sandbox destroy sb-12345678
```

---

## 五、需要新增的 CLI 命令汇总

基于现有 `pb` 的 `server`/`sandbox` 命令，新增：

| 命令 | 优先级 | 说明 |
|------|--------|------|
| `pb rpc <prim>` | P0 | 通用 primitive 调用，其他命令的基础 |
| `pb fs read/write/list/search` | P0 | fs primitive 快捷命令 |
| `pb shell` | P0 | shell.exec 快捷命令，支持 `--stream` |
| `pb checkpoint create/list/restore` | P0 | checkpoint 管理 |
| `pb verify test` | P0 | 测试验证 |
| `pb primitives list` | P0 | 列出所有已注册 primitive 及其 intent |
| `pb trace list` | P1 | 查看历史事件 |
| `pb trace watch` | P1 | SSE 实时事件流 |
| `pb trace inspect` | P1 | 单个 CVR 链路详情 |
| `pb trace export/replay` | P2 | 事件导出与回放 |
| `pb demo list` | P1 | 列出可用 demo |
| `pb demo run` | P1 | 运行 demo，支持 `--step` / `--break-on` |

### 实现顺序建议

```
Week 1:  pb rpc + pb fs/shell/checkpoint/verify  (P0)
         → 能手动调试单个 primitive

Week 2:  pb primitives list + pb trace list/watch  (P0-P1)
         → 能看到运行时内部状态

Week 3:  pb demo run --step  (P1)
         → 完整的可控 demo 体验

Week 4:  pb trace inspect/export/replay  (P2)
         → 事后分析和可重放性
```

---

## 六、`invokeRPC` 核心实现参考

所有 CLI 命令最终都走这个函数：

```go
// internal/cli/rpc_client.go

package cli

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
)

type RPCRequest struct {
    Primitive string         `json:"primitive"`
    Params    map[string]any `json:"params"`
}

type RPCResponse struct {
    Result json.RawMessage `json:"result,omitempty"`
    Error  *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
}

func InvokeRPC(host, sandboxID, primitive string, params map[string]any) (*RPCResponse, error) {
    // 构造 endpoint
    var endpoint string
    if sandboxID != "" {
        endpoint = fmt.Sprintf("%s/sandboxes/%s/rpc", host, sandboxID)
    } else {
        endpoint = fmt.Sprintf("%s/rpc", host)
    }

    // 构造请求
    req := RPCRequest{
        Primitive: primitive,
        Params:    params,
    }
    body, _ := json.Marshal(req)

    resp, err := http.Post(endpoint, "application/json", bytes.NewReader(body))
    if err != nil {
        return nil, fmt.Errorf("rpc call failed: %w", err)
    }
    defer resp.Body.Close()

    data, _ := io.ReadAll(resp.Body)
    var rpcResp RPCResponse
    if err := json.Unmarshal(data, &rpcResp); err != nil {
        return nil, fmt.Errorf("invalid response: %w", err)
    }

    if rpcResp.Error != nil {
        return nil, fmt.Errorf("rpc error [%d]: %s", rpcResp.Error.Code, rpcResp.Error.Message)
    }

    return &rpcResp, nil
}

func InvokeRPCStream(host, sandboxID, primitive string, params map[string]any, handler func(event []byte)) error {
    var endpoint string
    if sandboxID != "" {
        endpoint = fmt.Sprintf("%s/sandboxes/%s/rpc/stream", host, sandboxID)
    } else {
        endpoint = fmt.Sprintf("%s/rpc/stream", host)
    }

    req := RPCRequest{Primitive: primitive, Params: params}
    body, _ := json.Marshal(req)

    resp, err := http.Post(endpoint, "application/json", bytes.NewReader(body))
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    // SSE 解析
    buf := make([]byte, 4096)
    for {
        n, err := resp.Body.Read(buf)
        if n > 0 {
            handler(buf[:n])
        }
        if err == io.EOF {
            break
        }
        if err != nil {
            return err
        }
    }
    return nil
}
```

---

## 七、`--step` 模式核心逻辑参考

```go
// internal/cli/step_runner.go

package cli

import (
    "bufio"
    "fmt"
    "os"
    "strings"
)

type StepAction int

const (
    StepExecute StepAction = iota
    StepSkip
    StepQuit
)

func PromptStep(primitive string, params map[string]any, risk string) StepAction {
    fmt.Printf("\n  Agent wants to invoke:\n")
    fmt.Printf("    %s\n", primitive)
    for k, v := range params {
        vs := fmt.Sprintf("%v", v)
        if len(vs) > 80 {
            vs = vs[:77] + "..."
        }
        fmt.Printf("    %s=%s\n", k, vs)
    }
    fmt.Printf("    Risk: %s\n", risk)

    if risk == "High" {
        fmt.Printf("    ⚠  High-risk primitive — CVR checkpoint recommended\n")
    }

    fmt.Printf("\n  ▸ [Enter] execute | [s] skip | [d] detail | [q] quit: ")

    reader := bufio.NewReader(os.Stdin)
    input, _ := reader.ReadString('\n')
    input = strings.TrimSpace(strings.ToLower(input))

    switch input {
    case "s":
        return StepSkip
    case "q":
        return StepQuit
    default:
        return StepExecute
    }
}
```
