# Auto-Fix-Bug Demo

PrimitiveBox 的核心演示场景：AI agent 在沙箱中自动修复代码 bug，完整展示 CVR（Checkpoint → Verify → Recover）循环。

## 文件结构

```
auto_fix_bug_demo/
├── AGENT_PROMPT.md              # Agent System Prompt（喂给 LLM）
├── PROMPT.md                    # 完整方案文档（含设计说明）
├── run_demo.py                  # 主驱动脚本 — LLM agent loop
├── stream_demo.py               # SSE 事件流可视化
├── testdata/buggy_calc/         # 测试用 buggy 项目
│   ├── calc.go                  # 含 2 个 bug 的计算器
│   ├── calc_test.go             # 测试文件（agent 不应修改）
│   ├── go.mod
│   └── BUG_REPORT.md            # Bug 报告（agent 输入）
└── README.md
```

## 演示的核心价值

这个 demo 展示的不是 "AI 能改代码" —— 而是 **AI 在一个有事务保障的运行时里改代码**：

1. **每次修改前必须 checkpoint** — 不是可选的好习惯，是运行时强制的
2. **验证靠测试，不靠模型自信** — verify primitive 的成功标准是客观的
3. **失败了就 rollback，不是在错误基础上继续修** — 干净恢复，重新推理
4. **整个过程可重放** — event stream 记录了每一步操作和决策

## 快速运行

```bash
# 1. 启动 PrimitiveBox
make build
./bin/pb server start --workspace ./testdata/buggy_calc

# 2. 开一个终端看事件流
python3 stream_demo.py

# 3. 另一个终端跑 agent
export ANTHROPIC_API_KEY="sk-ant-..."
python3 run_demo.py
```

## 预期流程

Agent 会按以下顺序工作：

1. 读取 BUG_REPORT.md，理解 bug
2. 跑一遍测试，确认失败（2 个 test fail）
3. **Checkpoint** → 修 BUG-001（`a - b` → `a + b`）→ **Verify**（测试）→ 通过
4. **Checkpoint** → 修 BUG-002（`return 0, nil` → `return 0, errors.New(...)`）→ **Verify** → 通过
5. 输出最终报告

如果 agent 的修复错误，CVR 循环会自动触发 rollback + retry。

## 自定义

- 修改 `AGENT_PROMPT.md` 调整 agent 行为
- 替换 `testdata/buggy_calc/` 为你自己的 buggy 项目
- 设置 `MODEL` 环境变量切换模型
- 设置 `PB_SANDBOX_ID` 使用 Docker 沙箱模式
