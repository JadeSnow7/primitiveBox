# PrimitiveBox 官方使用文档

PrimitiveBox 是一个面向 AI Agent 工作流的宿主机端 JSON-RPC 网关与沙盒执行环境。它的核心目标不是“让模型能执行命令”这么简单，而是让执行过程具备明确边界、可回滚状态和可审计事件链。

如果你正在为 Agent 系统接入文件读写、代码搜索、命令执行、浏览器自动化或数据库访问能力，PrimitiveBox 提供了一种更稳妥的方式：通过容器划分边界，通过原语提供能力，通过验证保证可靠性。

## 1. 概览

在没有统一执行层的 Agent 系统中，常见问题往往集中在三个方向：

- 模型直接操作宿主机目录或进程，执行边界不清晰，风险难以控制。
- 缺少沙盒隔离，导致不同任务之间互相污染，或把本应在隔离环境中运行的动作误执行在网关宿主机上。
- 缺少快照、回滚和事件留痕，代码被修改后难以恢复，出错后也无法准确追溯执行过程。

PrimitiveBox 的定位，就是解决这些问题。

它采用清晰的控制平面 / 执行平面分层：

- Gateway 负责 API 边界、控制平面、路由与事件持久化。
- Sandbox 内的 `pb server` 负责执行 workspace-bound primitives。

你可以把 PrimitiveBox 理解为一层“Agent 执行网关”：

- 对上，它为 CLI、Python SDK、自动化代理提供统一的 JSON-RPC / SSE 接口。
- 对下，它把具体执行路由到宿主机工作区，或路由到 Docker / Kubernetes 沙盒中的 `pb server`。

> **注意：**
> PrimitiveBox 的安全边界建立在“网关负责管理，沙盒负责执行”的分工上。属于沙盒工作区的文件读写、命令执行、浏览器操作或数据库访问，不应偷跑到 Gateway 宿主机侧完成。

## 2. 快速开始

这一节先带你跑通最小链路：构建 CLI、启动宿主机工作区模式，然后用 HTTP 和 Python SDK 发起一次调用。

### 2.1 构建 CLI

在仓库根目录执行：

```bash
make build
```

构建完成后会生成：

```bash
./bin/pb
```

### 2.2 启动宿主机工作区模式

Host workspace mode 会把一个本地目录直接暴露为 primitives 工作区，入口是 `/rpc`。

```bash
./bin/pb server start --workspace ./my-project
```

默认监听地址为：

```text
http://localhost:8080
```

### 2.3 验证服务已启动

可以先检查健康状态：

```bash
curl http://localhost:8080/health
```

或查看当前可用原语列表：

```bash
curl http://localhost:8080/primitives
```

### 2.4 发起一条最小 JSON-RPC 调用

下面的示例会读取工作区中的 `README.md`：

```bash
curl -X POST http://localhost:8080/rpc \
  -H 'Content-Type: application/json' \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "fs.read",
    "params": {
      "path": "README.md"
    }
  }'
```

### 2.5 使用 Python SDK 调用

```python
from primitivebox import PrimitiveBoxClient

client = PrimitiveBoxClient("http://localhost:8080")

print(client.health())
print(client.fs.read("README.md"))
```

> **提示：**
> 当你没有指定 `sandbox_id` 时，`PrimitiveBoxClient` 会直接调用宿主机 `/rpc`。这适合本地开发和最小集成验证，但不等同于沙盒执行。

## 3. 核心概念

理解以下三个概念后，你会更容易正确使用 PrimitiveBox：执行边界、原语契约、快照优先。

### 3.1 Gateway 代理架构

PrimitiveBox 的典型调用路径如下：

```text
CLI / Python SDK / Agent
        ↓
     Gateway
        ↓
Router / Runtime Driver
        ↓
Sandbox 内 pb server
        ↓
Primitive execution
```

按模式不同，入口有两种：

- Host workspace mode：调用 `POST /rpc`
- Sandbox mode：调用 `POST /sandboxes/{id}/rpc`

它们的差异不只是路径不同，而是执行位置不同：

- `/rpc` 面向显式宿主机工作区。
- `/sandboxes/{id}/rpc` 面向具体沙盒，由 Gateway 代理到该沙盒内的 `pb server`。

> **注意：**
> 不要把“网关正在本地运行”理解成“所有工作都应在宿主机执行”。如果目标是某个 sandbox 的工作区，就应当走 `/sandboxes/{id}/rpc` 或在 SDK 中传入 `sandbox_id`。

### 3.2 原语（Primitives）是什么

Primitive 是 PrimitiveBox 对外暴露的能力单元。它们遵循统一的契约：

- JSON 输入 / JSON 输出
- 显式 schema
- 命名空间化方法名
- 尽可能保持确定性语义
- 在关键执行阶段发出结构化事件

当前仓库中的主要命名空间包括：

- `fs.*`：安全文件操作，例如 `fs.read`、`fs.write`、`fs.list`、`fs.diff`
- `code.*`：代码搜索与符号分析，例如 `code.search`、`code.symbols`
- `shell.*`：命令执行与流式输出，例如 `shell.exec`
- `state.*`：基于 Git 的状态快照与恢复，例如 `state.checkpoint`、`state.restore`、`state.list`
- `verify.*`：验证与测试执行，例如 `verify.test`
- `macro.*`：组合型原语，例如 `macro.safe_edit`
- `db.*`：只读数据库探查，例如 `db.schema`、`db.query_readonly`
- `browser.*`：浏览器自动化，例如 `browser.goto`、`browser.click`、`browser.extract`、`browser.screenshot`

其中：

- `db.*`
- `browser.*`

默认只在沙盒容器模式中注册，不会在宿主机工作区模式中默认暴露。

### 3.3 Snapshot-First：快照优先

对 Agent 来说，“先修改，再看结果”是远远不够的。更稳妥的执行习惯是：

1. 先创建检查点。
2. 再进行写入、命令执行或自动修复。
3. 如果验证失败，回滚到先前状态。

PrimitiveBox 使用 `state.checkpoint` 和 `state.restore` 支持这一工作流。

下面是一条推荐的操作序列：

```bash
curl -X POST http://localhost:8080/rpc \
  -H 'Content-Type: application/json' \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "state.checkpoint",
    "params": {
      "label": "before-edit"
    }
  }'
```

然后进行写入或执行验证：

```bash
curl -X POST http://localhost:8080/rpc \
  -H 'Content-Type: application/json' \
  -d '{
    "jsonrpc": "2.0",
    "id": 2,
    "method": "verify.test",
    "params": {
      "command": "pytest",
      "timeout_s": 60
    }
  }'
```

如果需要回滚：

```bash
curl -X POST http://localhost:8080/rpc \
  -H 'Content-Type: application/json' \
  -d '{
    "jsonrpc": "2.0",
    "id": 3,
    "method": "state.restore",
    "params": {
      "checkpoint_id": "latest"
    }
  }'
```

使用 Python SDK 时，这一流程会更自然：

```python
from primitivebox import PrimitiveBoxClient

client = PrimitiveBoxClient("http://localhost:8080")

checkpoint = client.state.checkpoint("before-edit")
test_result = client.verify.test("pytest", timeout_s=60)

if not test_result.get("data", {}).get("passed", False):
    client.state.restore(checkpoint.get("data", {}).get("checkpoint_id", "latest"))
```

> **提示：**
> 如果你的 Agent 会进行自动修复、批量改写或多轮试错，把 `state.checkpoint` 作为每轮动作的起点，会明显降低“改乱了无法恢复”的风险。

## 4. 沙盒与运行模式详解

PrimitiveBox 支持多种运行模式。最常用的是三种：

- 宿主机工作区模式
- Docker 沙盒模式
- Kubernetes 沙盒模式

### 4.1 宿主机工作区模式

适合场景：

- 本地开发
- 快速验证 primitives 接口
- 不需要额外容器隔离的实验性任务

启动方式：

```bash
./bin/pb server start --workspace ./my-project
```

访问入口：

- `POST /rpc`
- `POST /rpc/stream`

这种模式最简单，但也意味着执行发生在本地工作区上。对于需要隔离的 Agent 工作流，建议切换到沙盒模式。

### 4.2 Docker 模式

Docker 模式下，每个 sandbox 都在独立容器中运行自己的 `pb server`。Gateway 保存控制平面状态，并代理请求到该容器。

#### 4.2.1 构建 Docker 沙盒镜像

```bash
make sandbox-image
```

默认镜像名为：

```text
primitivebox-sandbox:latest
```

#### 4.2.2 创建 Docker 沙盒

```bash
./bin/pb sandbox create \
  --driver docker \
  --mount ./my-project \
  --ttl 3600 \
  --idle-ttl 900 \
  --network-mode none
```

常见参数说明：

- `--driver docker`：使用 Docker runtime driver
- `--mount ./my-project`：把本地目录挂载到沙盒工作区
- `--ttl 3600`：沙盒最大生命周期为 3600 秒
- `--idle-ttl 900`：空闲 900 秒后允许被回收
- `--network-mode none`：禁用网络访问

创建后，可用以下命令查看：

```bash
./bin/pb sandbox list
./bin/pb sandbox inspect <sandbox_id>
```

#### 4.2.3 启动 Gateway 并代理到沙盒

创建 sandbox 后，启动宿主 Gateway：

```bash
./bin/pb server start --workspace .
```

此时对某个沙盒的调用入口为：

- `POST /sandboxes/{id}/rpc`
- `POST /sandboxes/{id}/rpc/stream`

#### 4.2.4 `--network-mode` 的作用

CLI 当前暴露的网络策略相关参数包括：

- `--network-mode none|full|policy`
- `--network-host`
- `--network-cidr`
- `--network-port`

对 Docker 模式来说，当前文档应重点按 coarse-grained 语义理解：

- `none`：尽量不让沙盒访问外部网络
- `full`：允许完整网络访问

虽然 CLI 也暴露了 `policy` 与 allowlist 相关参数，但当前 Docker 模式的实践重点仍是粗粒度的 `none/full` 意图控制。更细粒度的网络策略更适合作为 Kubernetes 驱动的能力来理解。

> **注意：**
> 如果你的安全模型依赖严格的细粒度网络出站控制，请优先评估 Kubernetes 模式，而不是把 Docker 模式写成“已经完整支持精细网络策略”。

### 4.3 Kubernetes 模式

PrimitiveBox 当前已经暴露 `kubernetes` runtime driver，并保留与 Docker 类似的 CLI 入口。

```bash
./bin/pb sandbox create \
  --driver kubernetes \
  --namespace default \
  --ttl 3600 \
  --idle-ttl 900 \
  --network-mode none
```

Kubernetes 模式的当前能力包括：

- 自动创建 Pod
- 自动创建 Service
- 使用 PVC 作为工作区
- 在需要时创建 `NetworkPolicy`
- 等待 Pod 就绪后建立本地 `port-forward`
- 将运行时信息持久化到同一个 SQLite 控制平面

#### 4.3.1 Kubernetes 模式的关键边界

Kubernetes 模式是当前可用能力，但文档中需要明确它的边界：

- 工作区是 PVC-backed，而不是直接挂载宿主目录
- `--mount` 在 Kubernetes 模式下不支持
- 实际访问依赖网关侧建立的 port-forward
- 适合需要集群隔离、命名空间治理和网络策略编排的场景

也就是说，Kubernetes 部分应理解为“当前可用，适合进一步演进”，而不是“无边界的生产承诺”。

> **注意：**
> 当 `--driver kubernetes` 时，不要再传 `--mount`。当前实现会明确拒绝这种用法，因为 Kubernetes v1 工作区基于 PVC，而不是宿主机挂载。

### 4.4 什么时候选 Docker，什么时候选 Kubernetes

可以按以下原则判断：

- 选 Docker：你需要最快速的本地隔离、镜像式交付和较低的集成门槛。
- 选 Kubernetes：你需要 namespace 级治理、PVC 工作区、集群调度和更强的网络策略承载能力。
- 选 Host mode：你在本地开发、调试 primitives，且明确知道自己在操作宿主机工作区。

## 5. Python SDK 指南

PrimitiveBox 官方 Python SDK 提供同步和异步两套客户端：

- `PrimitiveBoxClient`
- `AsyncPrimitiveBoxClient`

它们都支持：

- 普通 JSON-RPC 调用
- 指定 `sandbox_id` 访问沙盒
- 流式调用 `/rpc/stream` 或 `/sandboxes/{id}/rpc/stream`

### 5.1 初始化客户端

连接宿主机工作区模式：

```python
from primitivebox import PrimitiveBoxClient

client = PrimitiveBoxClient("http://localhost:8080")
print(client.health())
```

连接指定沙盒：

```python
from primitivebox import PrimitiveBoxClient

client = PrimitiveBoxClient(
    "http://localhost:8080",
    sandbox_id="sb-12345678",
)

print(client.health())
print(client.list_primitives())
```

当传入 `sandbox_id` 后，SDK 会自动把请求路由到：

```text
/sandboxes/{sandbox_id}/rpc
```

### 5.2 普通 JSON-RPC 调用示例

读取文件：

```python
from primitivebox import PrimitiveBoxClient

client = PrimitiveBoxClient("http://localhost:8080", sandbox_id="sb-12345678")

result = client.fs.read("README.md")
print(result)
```

代码搜索：

```python
result = client.code.search("PrimitiveBox", path="README.md")
print(result)
```

执行测试：

```python
result = client.verify.test("pytest", timeout_s=60)
print(result)
```

### 5.3 流式调用：实时获取 shell 输出

对于 Agent 场景，最常用的流式接口之一是 `client.shell.stream_exec()`。

```python
from primitivebox import PrimitiveBoxClient

client = PrimitiveBoxClient("http://localhost:8080", sandbox_id="sb-12345678")

for event in client.shell.stream_exec("printf 'hello\\n' && echo done", timeout_s=30):
    print(event["event"], event["data"])
```

当前仓库中的流式事件名包括：

- `started`
- `stdout`
- `stderr`
- `progress`
- `completed`
- `error`

你也可以按事件类型进行分支处理：

```python
from primitivebox import PrimitiveBoxClient

client = PrimitiveBoxClient("http://localhost:8080", sandbox_id="sb-12345678")

for event in client.shell.stream_exec("pytest -q", timeout_s=120):
    name = event["event"]
    data = event["data"]

    if name == "stdout":
        print(data)
    elif name == "stderr":
        print(data)
    elif name == "completed":
        print("Command finished:", data)
    elif name == "error":
        print("Command failed:", data)
```

### 5.4 直接使用通用流式入口

如果你要对任意 primitive 进行流式调用，可以使用 `client.stream_call(...)`：

```python
from primitivebox import PrimitiveBoxClient

client = PrimitiveBoxClient("http://localhost:8080", sandbox_id="sb-12345678")

for event in client.stream_call("fs.diff", {"path": "README.md"}):
    print(event["event"], event["data"])
```

### 5.5 异步客户端示例

如果你的 Agent runtime 本身是 async 的，可以使用 `AsyncPrimitiveBoxClient`：

```python
import asyncio
from primitivebox import AsyncPrimitiveBoxClient


async def main():
    async with AsyncPrimitiveBoxClient(
        "http://localhost:8080",
        sandbox_id="sb-12345678",
    ) as client:
        result = await client.fs.read("README.md")
        print(result)

        async for event in client.stream_call("shell.exec", {
            "command": "printf 'hello\\n'",
            "timeout_s": 30,
        }):
            print(event["event"], event["data"])


asyncio.run(main())
```

> **提示：**
> 当前 SDK 已实现 sync/async 两套客户端的流式调用能力。如果你只想尽快接入，优先从同步客户端开始通常更简单。

## 6. 内置控制台（Inspector UI）

PrimitiveBox 内置了一个 Inspector 单页应用，用于查看沙盒元数据、实时事件流和执行轨迹。

### 6.1 启动带 UI 的 Gateway

```bash
./bin/pb server start --workspace . --ui
```

启动后，直接在浏览器打开：

```text
http://localhost:8080/
```

### 6.2 Inspector 能看到什么

Inspector UI 主要基于以下控制平面接口工作：

- `GET /api/v1/sandboxes`
- `GET /api/v1/events`
- `GET /api/v1/events/stream`

你可以在界面中看到：

- 当前沙盒列表与状态
- 选中沙盒的元数据
- RPC 生命周期事件
- 沙盒生命周期事件
- `db.progress` 与 `browser.progress` 等事件
- 近期事件时间线与审计轨迹

### 6.3 典型使用方式

推荐在以下场景开启 UI：

- 调试 Agent 为什么会失败
- 审计某次自动化修复具体做了什么
- 观察 `shell.exec`、`browser.*`、`db.*` 等操作的事件流
- 验证 TTL / idle TTL 回收是否按预期工作

> **注意：**
> Inspector 不是额外的旁路日志系统，它依赖的正是 PrimitiveBox 控制平面事件与 SSE 流。换句话说，UI 看到的不是“调试输出”，而是系统的正式观测面。

## 7. CLI 参考卡片

这一节汇总日常最常用的 `pb` 命令。

### 7.1 生命周期命令

启动网关：

```bash
./bin/pb server start --workspace .
```

启动网关并附带 UI：

```bash
./bin/pb server start --workspace . --ui
```

创建沙盒：

```bash
./bin/pb sandbox create --driver docker --mount ./my-project
```

列出沙盒：

```bash
./bin/pb sandbox list
```

查看沙盒详情：

```bash
./bin/pb sandbox inspect <sandbox_id>
```

停止沙盒：

```bash
./bin/pb sandbox stop <sandbox_id>
```

销毁沙盒：

```bash
./bin/pb sandbox destroy <sandbox_id>
```

查看版本：

```bash
./bin/pb version
```

### 7.2 `pb server start` 关键参数

```bash
./bin/pb server start --help
```

重点参数：

- `--workspace`：宿主机工作区目录
- `--host`：绑定的主机地址
- `--port`：监听端口，默认 `8080`
- `--ui`：启用内置 Inspector UI

### 7.3 `pb sandbox create` 关键参数

```bash
./bin/pb sandbox create --help
```

重点参数：

- `--driver docker|kubernetes`：选择 runtime driver
- `--image`：沙盒镜像，默认 `primitivebox-sandbox:latest`
- `--mount`：Docker 模式下挂载宿主目录
- `--namespace`：运行时 namespace / tenancy scope
- `--ttl`：沙盒绝对生命周期上限，单位秒
- `--idle-ttl`：空闲回收阈值，单位秒
- `--network-mode none|full|policy`：网络策略模式
- `--network-host`：允许访问的域名，可重复传入
- `--network-cidr`：允许访问的 CIDR，可重复传入
- `--network-port`：允许访问的端口，可重复传入
- `--cpu`：CPU 限额
- `--memory`：内存限额，单位 MB
- `--user`：容器运行用户，例如 `1000:1000`

### 7.4 常用检查命令

查看健康状态：

```bash
curl http://localhost:8080/health
```

查看原语列表：

```bash
curl http://localhost:8080/primitives
```

查看事件流接口是否存在：

```bash
curl http://localhost:8080/api/v1/events
```

## 8. 最佳实践与注意事项

### 8.1 默认优先沙盒，而不是宿主机

Host workspace mode 很方便，但它更适合：

- 本地开发
- 快速调试
- 最小集成验证

真正的 Agent 执行链路，尤其是涉及命令执行、浏览器自动化、数据库探查或潜在不可信输入时，默认应优先考虑沙盒模式。

### 8.2 先快照，再写入，再验证

推荐把以下顺序作为固定模式：

1. `state.checkpoint`
2. `fs.write` 或 `shell.exec`
3. `verify.test`
4. 必要时 `state.restore`

如果你的 Agent 有自动修复能力，这个顺序尤其重要。

### 8.3 Docker 网络策略当前以 coarse 模式理解

虽然 CLI 暴露了 `policy` 与 allowlist 相关参数，但当前 Docker 模式的推荐理解仍是粗粒度的：

- `none`
- `full`

如果你需要更严格的出站网络治理，请优先从 Kubernetes 驱动的网络策略能力来设计。

### 8.4 `db.*` 与 `browser.*` 默认只在沙盒容器模式可用

这两类 primitive 默认不在宿主机工作区模式中注册。原因很简单：

- 数据库访问往往需要更清晰的运行边界
- 浏览器自动化依赖容器内额外运行时与图形/浏览器环境

如果你计划使用浏览器能力，还需要构建浏览器版镜像：

```bash
make sandbox-browser-image
```

### 8.5 控制平面事实来源在 SQLite

PrimitiveBox 的控制平面状态默认保存在：

```text
~/.primitivebox/controlplane.db
```

这里记录的是：

- 沙盒元数据
- 生命周期状态
- Inspector 事件历史

历史上的 JSON registry 仍可作为兼容导入来源，但不是新的控制平面真相来源。

### 8.6 事件流是正式接口，不只是调试输出

以下接口是正式能力：

- `POST /rpc/stream`
- `POST /sandboxes/{id}/rpc/stream`
- `GET /api/v1/events/stream`

如果你正在编写 Agent 运行时、编排器或观测平台，建议直接围绕这些 SSE 接口构建，而不是依赖零散日志。

### 8.7 把 Kubernetes 写成“当前可用”，而不是“已无边界成熟”

当前仓库已经提供 Kubernetes runtime driver，并支持 PVC、Pod、Service、可选 `NetworkPolicy` 和本地 port-forward。但在对外使用时，仍应保持务实表达：

- 它已经可用于实践和集成
- 它具备清晰能力边界
- 它仍然应被视为持续完善中的运行时实现

这种表述比“默认宣称完全生产成熟”更符合当前仓库事实。

---

如果你准备把 PrimitiveBox 接入到自己的 Agent 系统，推荐从以下路径开始：

1. 先用 Host mode 验证 primitives 与 SDK 调用链。
2. 再切到 Docker sandbox mode，建立隔离执行与 TTL 回收习惯。
3. 最后在需要集群调度、PVC 工作区和更强治理时引入 Kubernetes 模式。

这样能在最短时间内建立正确的执行边界，同时逐步把 Agent 能力迁移到更可靠的运行环境中。
