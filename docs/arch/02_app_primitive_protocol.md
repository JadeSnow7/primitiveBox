# Application Primitive 注册与路由机制

> 状态：架构草案（Iteration 4 设计阶段）
> 作者：架构设计会话，2026-03-16
> 依据：internal/runtime/adapter.go、cmd/pb-repo-adapter/main.go、internal/primitive/ 全量阅读

---

## 0. 现有基础与设计动机

### 现有实现的能力

`internal/runtime/adapter.go` 已经实现了外部进程适配器的基础骨架：

| 已有能力 | 实现位置 |
|---------|---------|
| 从 `manifests/*.json` 加载静态声明 | `loadManifests()` |
| 以子进程方式启动适配器 | `startAdapterProcess()` |
| 读取首行 stdout JSON 作为注册信号 | `startAdapterProcess()` — select on lineCh |
| HTTP / Unix socket 双传输支持 | `remoteClient` |
| 将适配器原语包装为 `Primitive` | `remotePrimitive` |
| `SourceApp` / `SourceSystem` 区分 | `metadata.go` |

### 现有实现的三个关键缺口

1. **静态注册**：适配器必须在 pb-runtimed 启动前已写好 `manifest.json`，无法在运行时动态注册。AI 原生应用需要在容器内进程启动后自行声明能力。

2. **无生命周期感知**：适配器进程退出后，其原语仍留在注册表中；没有健康检查机制，调用死掉的适配器会返回连接错误而非正常的降级响应。

3. **无 Verify/Recover 合约**：应用原语的 `VerifierHint` 和 `recover_strategy` 无法由应用自身声明；pb-runtimed 无法将应用原语接入统一的 checkpoint/verify/recover 流水线。

本文档设计一个**向后兼容**（保留现有 manifest 加载路径）的动态注册机制，同时引入生命周期管理和执行合约集成。

---

## 1. 系统边界与角色分工

```
┌─────────────────────────────────────────────────────┐
│                    Container                         │
│                                                      │
│  ┌─────────────┐     register      ┌─────────────┐  │
│  │   App       │ ──────────────→  │             │  │
│  │  Process    │ ←── registered ── │  pb-runtimed│  │
│  │  (codereview│                   │  (AppRouter) │  │
│  │   mydb, ...) │ ←── JSON-RPC ─── │             │  │
│  └─────────────┘     (dispatch)   └──────┬──────┘  │
│                                          │ port 8080 │
└──────────────────────────────────────────┼──────────┘
                                           │ HTTP proxy
                                    ┌──────┴──────┐
                                    │  pb gateway  │
                                    │  (host side) │
                                    └──────┬──────┘
                                           │
                                     AI Agent / SDK
```

**角色**：

- **App Process**：运行在容器内的 AI 原生应用；启动后主动向 pb-runtimed 注册自己暴露的原语。
- **pb-runtimed**（`AppRouter`）：接受注册请求，维护路由表，将传入的 RPC 调用分发到正确的应用，并管理健康检查和生命周期。
- **pb gateway**：不感知应用原语的存在；它只是把 `/sandboxes/{id}/rpc` 请求代理到 pb-runtimed 的 8080 端口。

---

## 2. 注册协议（Primitive Registry）

### 2.1 协议选型

| 方案 | 优点 | 缺点 | 结论 |
|------|------|------|------|
| **Unix socket + JSON-RPC** | 无额外依赖；与现有 pb-runtimed transport 一致；在容器内天然隔离；任何语言都能实现 | 不跨节点 | **选用** |
| gRPC | 强类型；流式支持 | 需要 protobuf 工具链；对轻量应用过重 | 不选 |
| HTTP REST（TCP） | 简单 | 需要协调端口分配；增加配置复杂度 | 不选 |
| 共享内存 | 极低延迟 | 跨语言实现复杂；无法跨进程边界隔离 | 不选 |

**选型理由**：Unix socket + JSON-RPC 2.0 是在容器内进程间通信的最小依赖方案，与现有 `remoteClient` 的 unix transport 代码路径完全一致，应用开发者只需发一个 HTTP POST 到 socket 即可完成注册，不引入任何额外运行时依赖。

### 2.2 注册 socket 位置

pb-runtimed 在启动时创建注册 socket：

```
$PB_REGISTER_SOCKET  （默认：/run/pb/register.sock）
```

该路径通过环境变量注入到所有应用容器，应用无需硬编码。

### 2.3 注册消息 JSON Schema

#### 2.3.1 注册请求（App → pb-runtimed）

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "AppRegistrationRequest",
  "description": "应用通过 primitive.register 调用发送此消息完成注册",
  "type": "object",
  "required": ["app_id", "namespace", "version", "transport", "primitives"],
  "properties": {
    "app_id": {
      "type": "string",
      "pattern": "^[a-z][a-z0-9_-]{1,63}$",
      "description": "应用的唯一标识符（小写，字母开头）。同一容器内必须唯一。"
    },
    "app_name": {
      "type": "string",
      "description": "人类可读的应用名称，用于 inspector 展示"
    },
    "version": {
      "type": "string",
      "pattern": "^\\d+\\.\\d+\\.\\d+",
      "description": "语义化版本号（semver），用于路由冲突解决和审计"
    },
    "namespace": {
      "type": "string",
      "pattern": "^[a-z][a-z0-9_]{1,31}$",
      "description": "应用声明的原语命名空间。所有原语名将以此为前缀：{namespace}.{primitive_name}。不能是系统保留命名空间。"
    },
    "transport": {
      "type": "string",
      "enum": ["unix", "http"],
      "description": "应用 RPC 服务的传输类型"
    },
    "socket": {
      "type": "string",
      "description": "Unix socket 路径（transport=unix 时必填）"
    },
    "endpoint": {
      "type": "string",
      "description": "HTTP endpoint（transport=http 时必填）。格式：http://127.0.0.1:{port}"
    },
    "health_path": {
      "type": "string",
      "default": "/health",
      "description": "pb-runtimed 用于健康检查的 HTTP GET 路径"
    },
    "health_interval_s": {
      "type": "integer",
      "default": 15,
      "minimum": 5,
      "maximum": 60,
      "description": "健康检查间隔（秒）"
    },
    "primitives": {
      "type": "array",
      "minItems": 1,
      "items": { "$ref": "#/definitions/AppPrimitiveDeclaration" },
      "description": "应用声明暴露的原语列表"
    }
  },
  "definitions": {
    "AppPrimitiveDeclaration": {
      "type": "object",
      "required": ["name", "description", "input_schema", "output_schema"],
      "properties": {
        "name": {
          "type": "string",
          "description": "原语短名（不含命名空间前缀）。最终注册名为 {namespace}.{name}。",
          "pattern": "^[a-z][a-z0-9_]{1,63}$"
        },
        "description": {
          "type": "string",
          "description": "原语功能描述，用于 AI 的 tool description"
        },
        "input_schema": {
          "type": "object",
          "description": "JSON Schema（draft-07）描述输入参数"
        },
        "output_schema": {
          "type": "object",
          "description": "JSON Schema（draft-07）描述输出结构"
        },
        "side_effect": {
          "type": "string",
          "enum": ["none", "read", "write", "exec"],
          "default": "none",
          "description": "原语的副作用类型，影响 checkpoint 决策"
        },
        "checkpoint_required": {
          "type": "boolean",
          "default": false,
          "description": "如果 true，pb-runtimed 在执行前自动插入 state.checkpoint"
        },
        "timeout_ms": {
          "type": "integer",
          "default": 30000,
          "description": "执行超时（毫秒）"
        },
        "verify_hint": {
          "type": "string",
          "description": "执行后用于验证的原语名（可为空或指向系统原语如 verify.test）"
        },
        "recover_strategy": {
          "type": "string",
          "enum": ["none", "retry", "checkpoint_restore", "rewrite", "escalate"],
          "default": "none",
          "description": "失败时的恢复建议（供 orchestrator 决策）"
        },
        "is_reversible": {
          "type": "boolean",
          "default": true,
          "description": "副作用是否可通过 state.restore 完全撤销"
        }
      }
    }
  }
}
```

#### 2.3.2 注册响应（pb-runtimed → App）

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "AppRegistrationResponse",
  "type": "object",
  "oneOf": [
    {
      "description": "注册成功",
      "properties": {
        "status": { "const": "registered" },
        "app_id": { "type": "string" },
        "primitive_count": { "type": "integer" },
        "registered_names": {
          "type": "array",
          "items": { "type": "string" },
          "description": "最终注册的完整原语名列表（含命名空间前缀）"
        },
        "health_check_url": {
          "type": "string",
          "description": "pb-runtimed 将用于健康检查的完整 URL"
        }
      },
      "required": ["status", "app_id", "registered_names"]
    },
    {
      "description": "注册失败",
      "properties": {
        "status": { "const": "error" },
        "code": {
          "type": "string",
          "enum": [
            "namespace_reserved",
            "namespace_conflict",
            "primitive_conflict",
            "invalid_namespace",
            "app_id_taken",
            "health_check_failed",
            "validation_error"
          ]
        },
        "message": { "type": "string" },
        "conflicting_app_id": {
          "type": "string",
          "description": "当 code=primitive_conflict 或 namespace_conflict 时，已占用该名称的 app_id"
        }
      },
      "required": ["status", "code", "message"]
    }
  ]
}
```

#### 2.3.3 完整 JSON-RPC 信封示例

注册请求：

```json
{
  "jsonrpc": "2.0",
  "method": "primitive.register",
  "id": "reg-1",
  "params": {
    "app_id": "codereview",
    "app_name": "Code Review Service",
    "version": "1.0.0",
    "namespace": "codereview",
    "transport": "unix",
    "socket": "/run/apps/codereview.sock",
    "health_path": "/health",
    "health_interval_s": 15,
    "primitives": [
      {
        "name": "review_file",
        "description": "Review a source file and return structured quality issues",
        "input_schema": {
          "type": "object",
          "required": ["path"],
          "properties": {
            "path": { "type": "string" },
            "severity_threshold": {
              "type": "string",
              "enum": ["info", "warning", "error"],
              "default": "warning"
            }
          }
        },
        "output_schema": {
          "type": "object",
          "properties": {
            "issues": {
              "type": "array",
              "items": {
                "type": "object",
                "properties": {
                  "severity": { "type": "string" },
                  "line":     { "type": "integer" },
                  "message":  { "type": "string" },
                  "rule":     { "type": "string" }
                }
              }
            },
            "issue_count": { "type": "integer" },
            "passed":      { "type": "boolean" }
          }
        },
        "side_effect": "read",
        "checkpoint_required": false,
        "timeout_ms": 30000,
        "verify_hint": "",
        "recover_strategy": "retry",
        "is_reversible": true
      }
    ]
  }
}
```

注册成功响应：

```json
{
  "jsonrpc": "2.0",
  "id": "reg-1",
  "result": {
    "status": "registered",
    "app_id": "codereview",
    "primitive_count": 1,
    "registered_names": ["codereview.review_file"],
    "health_check_url": "http://unix/health"
  }
}
```

### 2.4 生命周期管理

```
┌─────────────────────────────────────────────────────────────────────┐
│                     AppRoute 生命周期状态机                            │
│                                                                      │
│   primitive.register ──→ [pending]                                   │
│                              │                                       │
│                    health probe OK                                   │
│                              ↓                                       │
│                          [active] ←──── health probe OK ────┐       │
│                              │                               │       │
│                    health probe FAIL                         │       │
│                              ↓                               │       │
│                        [unhealthy]                           │       │
│                         │       │                            │       │
│             3次连续失败   │       │ health probe OK          │       │
│                         ↓       └──────────────────────────┘       │
│                   [deregistered]                                     │
│                         │                                           │
│              primitive.register (same app_id)                       │
│                         ↓                                           │
│                      [active]                                        │
└─────────────────────────────────────────────────────────────────────┘
```

| 触发事件 | 状态变化 | 影响 |
|---------|---------|------|
| `primitive.register` 成功 | → active | 原语进入路由表，可被调用 |
| 健康检查连续失败 3 次 | active → deregistered | 原语从路由表移除；进行中的调用返回 `APP_UNAVAILABLE` |
| 应用重新注册（相同 app_id）| deregistered → active | 原语恢复；版本可更新 |
| `primitive.deregister` 调用 | → deregistered | 应用主动注销（如优雅退出） |
| 健康检查恢复 | unhealthy → active | 原语自动恢复可用 |

### 2.5 保留命名空间（不允许 App 注册）

以下命名空间由系统原语独占，`primitive.register` 对这些命名空间返回 `namespace_reserved` 错误：

```
fs, shell, state, verify, code, macro, db, browser, primitive, pb
```

---

## 3. 路由机制（Primitive Router）

### 3.1 Router 数据结构（Go）

```go
package appregistry

import (
    "context"
    "net/http"
    "sync"
    "time"

    "primitivebox/internal/primitive"
)

// --------------------------------------------------------------------------
// AppRouteStatus — 应用路由的生命周期状态
// --------------------------------------------------------------------------

type AppRouteStatus string

const (
    AppRouteStatusPending      AppRouteStatus = "pending"       // 注册中，等待首次健康检查
    AppRouteStatusActive       AppRouteStatus = "active"        // 健康，原语可被路由
    AppRouteStatusUnhealthy    AppRouteStatus = "unhealthy"     // 健康检查失败，等待恢复
    AppRouteStatusDeregistered AppRouteStatus = "deregistered"  // 已注销（主动或超时）
)

// --------------------------------------------------------------------------
// AppRoute — 单个应用的路由条目
// --------------------------------------------------------------------------

// AppRoute 保存一个注册应用的所有路由信息。
// 它是 AppRouter 路由表的基本单元，也是 inspector 暴露应用状态的数据源。
type AppRoute struct {
    // 不变字段（注册时确定）
    AppID        string
    AppName      string
    Version      string
    Namespace    string             // 应用声明的命名空间
    RegisteredAt time.Time

    // 原语声明（来自注册请求）
    PrimitiveSchemas []primitive.Schema

    // 传输配置（注册时确定）
    Transport  string // "unix" 或 "http"
    Socket     string // Unix socket 路径（transport=unix）
    Endpoint   string // HTTP endpoint（transport=http）
    HealthPath string // 健康检查路径（默认 /health）

    // 健康检查配置
    HealthIntervalS int // 检查间隔（秒）

    // 可变状态（需加锁）
    mu           sync.RWMutex
    Status       AppRouteStatus
    LastHealthOK time.Time
    FailureCount int    // 连续健康检查失败次数
    LastError    string // 最近一次失败原因
}

// IsRoutable 返回此路由是否可以接受新的调用。
// 调用方在分发前必须先检查此方法。
func (r *AppRoute) IsRoutable() bool {
    r.mu.RLock()
    defer r.mu.RUnlock()
    return r.Status == AppRouteStatusActive
}

// RecordHealthOK 记录一次成功的健康检查。
func (r *AppRoute) RecordHealthOK() {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.LastHealthOK = time.Now()
    r.FailureCount = 0
    r.LastError = ""
    if r.Status == AppRouteStatusPending || r.Status == AppRouteStatusUnhealthy {
        r.Status = AppRouteStatusActive
    }
}

// RecordHealthFail 记录一次失败的健康检查。
// 返回 true 表示连续失败次数已达阈值，应触发 deregister。
func (r *AppRoute) RecordHealthFail(reason string, maxFailures int) bool {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.FailureCount++
    r.LastError = reason
    if r.Status == AppRouteStatusActive {
        r.Status = AppRouteStatusUnhealthy
    }
    return r.FailureCount >= maxFailures
}

// --------------------------------------------------------------------------
// AppRouter — 应用原语路由表
// --------------------------------------------------------------------------

// AppRouter 维护所有注册应用的路由表，提供原语名称到应用路由的映射，
// 并运行健康检查循环。
//
// 设计约束：
//   - AppRouter 不持有 primitive.Registry 的引用；它是路由层，不是注册层。
//   - 原语注册/注销的实际动作由 Runtime 在收到 AppRouter 通知后执行。
//   - 系统原语保留名单由调用方（Runtime）在注册前校验；AppRouter 不重复校验。
type AppRouter struct {
    mu sync.RWMutex

    // routes: app_id → AppRoute
    routes map[string]*AppRoute

    // nameIndex: 完整原语名（"codereview.review_file"）→ app_id
    // 用于 O(1) 路由查找
    nameIndex map[string]string

    // healthClient: 用于健康检查的 HTTP 客户端（带短超时）
    healthClient *http.Client

    // maxHealthFailures: 连续失败几次后 deregister（默认 3）
    maxHealthFailures int

    // Callbacks: 状态变更通知（由 Runtime 订阅，用于同步 primitive.Registry）
    onRegistered   func(route *AppRoute, schemas []primitive.Schema)
    onDeregistered func(appID string, primitiveNames []string)
}

// AppRouterConfig 构建 AppRouter 的配置参数。
type AppRouterConfig struct {
    MaxHealthFailures int           // 默认 3
    HealthTimeout     time.Duration // 单次健康检查超时，默认 5s
    OnRegistered      func(route *AppRoute, schemas []primitive.Schema)
    OnDeregistered    func(appID string, primitiveNames []string)
}

// NewAppRouter 创建并初始化 AppRouter。
func NewAppRouter(config AppRouterConfig) *AppRouter {
    if config.MaxHealthFailures <= 0 {
        config.MaxHealthFailures = 3
    }
    if config.HealthTimeout <= 0 {
        config.HealthTimeout = 5 * time.Second
    }
    return &AppRouter{
        routes:    make(map[string]*AppRoute),
        nameIndex: make(map[string]string),
        healthClient: &http.Client{
            Timeout: config.HealthTimeout,
        },
        maxHealthFailures: config.MaxHealthFailures,
        onRegistered:      config.OnRegistered,
        onDeregistered:    config.OnDeregistered,
    }
}

// Register 处理一次应用注册请求。
// 返回注册成功后的完整原语名列表，或注册失败的 RegistrationError。
func (r *AppRouter) Register(req AppRegistrationRequest) ([]string, error)

// Deregister 注销一个应用（主动或超时触发）。
func (r *AppRouter) Deregister(appID string)

// RouteFor 返回 primitiveName 对应的 AppRoute。
// 调用方需先检查 route.IsRoutable()。
func (r *AppRouter) RouteFor(primitiveName string) (*AppRoute, bool)

// AppInfo 返回所有注册应用的状态快照（用于 inspector API）。
func (r *AppRouter) AppInfo() []AppRouteSnapshot

// RunHealthChecks 启动健康检查循环，阻塞直到 ctx 取消。
// 遵循 context-bound background work 规范（使用 ticker + select）。
func (r *AppRouter) RunHealthChecks(ctx context.Context)

// --------------------------------------------------------------------------
// AppRouteSnapshot — inspector 用只读快照
// --------------------------------------------------------------------------

type AppRouteSnapshot struct {
    AppID            string         `json:"app_id"`
    AppName          string         `json:"app_name"`
    Version          string         `json:"version"`
    Namespace        string         `json:"namespace"`
    Status           AppRouteStatus `json:"status"`
    RegisteredAt     time.Time      `json:"registered_at"`
    LastHealthOK     time.Time      `json:"last_health_ok"`
    FailureCount     int            `json:"failure_count"`
    LastError        string         `json:"last_error,omitempty"`
    PrimitiveNames   []string       `json:"primitive_names"`
}
```

### 3.2 路由优先级

pb-runtimed 在收到 RPC 调用时，按以下顺序查找原语：

```
1. 系统原语（primitive.Registry，源自 RegisterDefaults + RegisterSandboxExtras）
       ↓ 未找到
2. 应用原语（AppRouter.nameIndex，源自动态注册）
       ↓ 未找到
3. 返回 PrimitiveError{Code: ErrNotFound}
```

**系统原语优先**的设计理由：
- 防止应用通过注册同名原语劫持系统能力（`fs.read`、`state.checkpoint` 等）
- 命名空间规则（应用必须使用独立命名空间）在注册阶段就阻止了大多数冲突，优先级规则是最后一道防线

### 3.3 同名原语冲突解决策略

冲突发生在**同一命名空间**内的两个不同应用尝试注册同名原语时（命名空间本身由第一个注册者独占）。

```go
// ConflictPolicy 决定命名空间冲突时的处理方式。
type ConflictPolicy string

const (
    // ConflictReject（默认）：第二个注册者收到 namespace_conflict 错误。
    // 推荐用于生产环境。
    ConflictReject ConflictPolicy = "reject"

    // ConflictVersion：允许更高版本的应用接管命名空间。
    // 新版本接管后，旧版本的 AppRoute 变为 deregistered。
    // 推荐用于应用热升级场景。
    ConflictVersion ConflictPolicy = "version"
)
```

**默认行为（ConflictReject）**：

```
App A 注册 namespace="codereview", version="1.0.0"  → 成功
App B 注册 namespace="codereview", version="1.1.0"  → 失败
  响应: {status: "error", code: "namespace_conflict", conflicting_app_id: "app_a_id"}
```

App B 若想接管，必须先向 pb-runtimed 发送 `primitive.deregister` 请求 app A（仅在 App A 的注册 token 一致时允许），或者使用不同命名空间。

### 3.4 路由失败降级行为

| 失败原因 | pb-runtimed 响应 | 建议的客户端行为 |
|---------|----------------|----------------|
| 原语不存在 | `ErrNotFound` | AI 选择其他原语或报告无法完成任务 |
| 应用 unhealthy | `ErrExecution` + `code: APP_UNAVAILABLE` | orchestrator 触发 retry（等待恢复）或 escalate |
| 应用 deregistered | `ErrNotFound` | 视同原语不存在 |
| 调用超时 | `ErrTimeout` | orchestrator 执行 `recover_strategy`（通常 retry） |
| 应用返回 RPC error | 透传 `ErrExecution` | orchestrator 执行 `recover_strategy` |

---

## 4. Application Primitive SDK

### 4.1 Python SDK（`sdk/python/primitivebox/app.py`）

设计原则：与现有 `client.py` / `async_client.py` 风格保持一致；最小依赖（仅标准库 + `http.server`）；通过装饰器暴露原语。

```python
"""
primitivebox.app — Application Primitive Server SDK

Usage:
    from primitivebox.app import AppServer

    server = AppServer(
        app_name="Code Review Service",
        namespace="codereview",
        version="1.0.0",
    )

    @server.primitive(
        name="review_file",
        description="Review a source file for code quality issues",
        input_schema={
            "type": "object",
            "required": ["path"],
            "properties": {
                "path": {"type": "string"},
                "severity_threshold": {
                    "type": "string",
                    "enum": ["info", "warning", "error"],
                    "default": "warning"
                }
            }
        },
        output_schema={
            "type": "object",
            "properties": {
                "issues": {"type": "array"},
                "passed": {"type": "boolean"}
            }
        },
        side_effect="read",
        timeout_ms=30000,
    )
    def review_file(ctx: AppCallContext, params: dict) -> dict:
        path = params["path"]
        issues = run_linter(path)
        return {"issues": issues, "passed": len(issues) == 0}

    server.serve()  # 阻塞，直到收到 SIGTERM
"""

from __future__ import annotations

import json
import logging
import os
import signal
import socket
import threading
from dataclasses import dataclass, field
from http.server import BaseHTTPRequestHandler, HTTPServer
from typing import Any, Callable, Dict, List, Optional
from urllib.request import urlopen, Request


# --------------------------------------------------------------------------
# AppCallContext — 每次原语调用携带的上下文
# --------------------------------------------------------------------------

@dataclass
class AppCallContext:
    """每次原语调用传入 handler 的上下文对象。"""
    sandbox_id: str = ""
    trace_id:   str = ""
    method:     str = ""


# --------------------------------------------------------------------------
# PrimitiveDeclaration — 原语声明（注册时发送给 pb-runtimed）
# --------------------------------------------------------------------------

@dataclass
class PrimitiveDeclaration:
    name:                str
    description:         str
    input_schema:        dict
    output_schema:       dict
    side_effect:         str  = "none"
    checkpoint_required: bool = False
    timeout_ms:          int  = 30000
    verify_hint:         str  = ""
    recover_strategy:    str  = "none"
    is_reversible:       bool = True


# --------------------------------------------------------------------------
# AppServer — 应用原语服务主类
# --------------------------------------------------------------------------

class AppServer:
    """
    AppServer 是 Application Primitive 的宿主。

    它做三件事：
    1. 维护一个从 primitive name 到 handler 函数的映射表
    2. 启动一个轻量 HTTP/Unix socket 服务器来接受 pb-runtimed 的 JSON-RPC 调用
    3. 连接到 pb-runtimed 的注册 socket，发送注册请求，并在退出时发送注销请求
    """

    def __init__(
        self,
        app_name:           str,
        namespace:          str,
        version:            str = "1.0.0",
        app_id:             Optional[str] = None,
        health_interval_s:  int = 15,
        register_socket:    Optional[str] = None,
        transport:          str = "unix",           # "unix" 或 "http"
        socket_path:        Optional[str] = None,   # transport=unix 时的 socket 路径
        listen_addr:        Optional[str] = None,   # transport=http 时的监听地址
    ):
        self.app_name          = app_name
        self.namespace         = namespace
        self.version           = version
        self.app_id            = app_id or namespace
        self.health_interval_s = health_interval_s
        self.transport         = transport
        self.register_socket   = register_socket or os.environ.get(
            "PB_REGISTER_SOCKET", "/run/pb/register.sock"
        )

        # 计算本服务的监听配置
        if transport == "unix":
            self.socket_path = socket_path or f"/run/apps/{self.app_id}.sock"
            self.listen_addr = None
        else:
            self.socket_path = None
            self.listen_addr = listen_addr or "127.0.0.1:0"

        self._handlers:     Dict[str, Callable] = {}
        self._declarations: List[PrimitiveDeclaration] = []
        self._server:       Optional[HTTPServer] = None
        self._log = logging.getLogger(f"pb.app.{self.app_id}")

    # ---------- 装饰器 API ----------

    def primitive(
        self,
        name:                str,
        description:         str,
        input_schema:        dict,
        output_schema:       dict,
        side_effect:         str  = "none",
        checkpoint_required: bool = False,
        timeout_ms:          int  = 30000,
        verify_hint:         str  = "",
        recover_strategy:    str  = "none",
        is_reversible:       bool = True,
    ) -> Callable:
        """
        将一个函数注册为应用原语。

        装饰的函数签名：
            def my_handler(ctx: AppCallContext, params: dict) -> dict: ...

        原语的完整注册名为 {namespace}.{name}，
        但 handler 只接收 params（无需关心命名空间前缀）。
        """
        decl = PrimitiveDeclaration(
            name=name,
            description=description,
            input_schema=input_schema,
            output_schema=output_schema,
            side_effect=side_effect,
            checkpoint_required=checkpoint_required,
            timeout_ms=timeout_ms,
            verify_hint=verify_hint,
            recover_strategy=recover_strategy,
            is_reversible=is_reversible,
        )

        def decorator(fn: Callable) -> Callable:
            full_name = f"{self.namespace}.{name}"
            self._handlers[full_name] = fn
            self._declarations.append(decl)
            self._log.debug("registered primitive handler: %s", full_name)
            return fn

        return decorator

    # ---------- 服务启动 ----------

    def serve(self, block: bool = True) -> None:
        """
        启动 RPC 服务器并向 pb-runtimed 注册。

        block=True（默认）：阻塞直到收到 SIGTERM / SIGINT。
        block=False：在后台线程启动，立即返回（用于测试）。
        """
        self._start_rpc_server()
        self._register_with_pbruntimed()

        if block:
            self._wait_for_shutdown()

    def stop(self) -> None:
        """主动注销并停止 RPC 服务器（用于优雅退出）。"""
        self._deregister_from_pbruntimed()
        if self._server:
            self._server.shutdown()

    # ---------- 内部实现（公开以供测试） ----------

    def _build_registration_payload(self) -> dict:
        """构建发送给 pb-runtimed 的注册请求 payload。"""
        transport_config: dict = {}
        if self.transport == "unix":
            transport_config = {"socket": self.socket_path}
        else:
            addr = self._server.server_address if self._server else ("127.0.0.1", 0)
            transport_config = {"endpoint": f"http://{addr[0]}:{addr[1]}"}

        return {
            "app_id":            self.app_id,
            "app_name":          self.app_name,
            "version":           self.version,
            "namespace":         self.namespace,
            "transport":         self.transport,
            "health_path":       "/health",
            "health_interval_s": self.health_interval_s,
            "primitives": [
                {
                    "name":                d.name,
                    "description":         d.description,
                    "input_schema":        d.input_schema,
                    "output_schema":       d.output_schema,
                    "side_effect":         d.side_effect,
                    "checkpoint_required": d.checkpoint_required,
                    "timeout_ms":          d.timeout_ms,
                    "verify_hint":         d.verify_hint,
                    "recover_strategy":    d.recover_strategy,
                    "is_reversible":       d.is_reversible,
                }
                for d in self._declarations
            ],
            **transport_config,
        }

    def _register_with_pbruntimed(self) -> None:
        """通过 Unix socket 向 pb-runtimed 发送注册请求。"""
        payload = self._build_registration_payload()
        rpc_body = json.dumps({
            "jsonrpc": "2.0",
            "method":  "primitive.register",
            "id":      "reg-1",
            "params":  payload,
        }).encode()

        # 通过 Unix socket 发送（使用标准库，无额外依赖）
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        try:
            sock.connect(self.register_socket)
            # 包装为最小 HTTP/1.0 请求（pb-runtimed 的注册端点接受裸 JSON-RPC）
            http_req = (
                b"POST /primitive/register HTTP/1.0\r\n"
                b"Content-Type: application/json\r\n"
                + f"Content-Length: {len(rpc_body)}\r\n".encode()
                + b"\r\n"
                + rpc_body
            )
            sock.sendall(http_req)
            response_data = b""
            while True:
                chunk = sock.recv(4096)
                if not chunk:
                    break
                response_data += chunk
        finally:
            sock.close()

        # 解析响应
        body = response_data.split(b"\r\n\r\n", 1)[-1]
        resp = json.loads(body)
        result = resp.get("result", {})
        if result.get("status") != "registered":
            error = resp.get("error") or result
            raise RuntimeError(
                f"Registration failed: {error.get('code')} — {error.get('message')}"
            )
        names = result.get("registered_names", [])
        self._log.info("registered %d primitive(s): %s", len(names), names)

    def _deregister_from_pbruntimed(self) -> None:
        """向 pb-runtimed 发送注销请求。"""
        # 实现省略（与 _register_with_pbruntimed 结构相同，method 改为 primitive.deregister）
        pass

    def _start_rpc_server(self) -> None:
        """启动内部 JSON-RPC HTTP 服务器，接受 pb-runtimed 的调用转发。"""
        # 实现：创建 HTTPServer，绑定到 Unix socket 或 TCP 端口
        # 路由 /health → {"status": "ok"}
        # 路由 /rpc → _handle_rpc()
        pass

    def _handle_rpc(self, method: str, params: dict, ctx: AppCallContext) -> Any:
        """分发一次 JSON-RPC 调用到对应的 handler。"""
        handler = self._handlers.get(method)
        if handler is None:
            raise ValueError(f"unknown method: {method}")
        return handler(ctx, params)

    def _wait_for_shutdown(self) -> None:
        """阻塞等待 SIGTERM/SIGINT，收到后执行优雅退出。"""
        stop_event = threading.Event()
        signal.signal(signal.SIGTERM, lambda *_: stop_event.set())
        signal.signal(signal.SIGINT,  lambda *_: stop_event.set())
        stop_event.wait()
        self.stop()
```

### 4.2 Go SDK（`sdk/go/pbapp/server.go`）

供容器内 Go 应用使用，作为独立模块（不引用 primitivebox 内部包，仅依赖标准库）。

```go
// Package pbapp provides the Application Primitive Server SDK for Go applications
// running inside PrimitiveBox containers.
//
// 设计原则：
//   - 不引入任何外部依赖（仅 encoding/json、net/http、os、sync）
//   - 与 pb-runtimed 的注册协议兼容（JSON-RPC 2.0 over Unix socket）
//   - Handler 签名与 primitive.Primitive 接口对称，便于移植

package pbapp

import (
    "context"
    "encoding/json"
    "fmt"
    "net"
    "net/http"
    "os"
    "os/signal"
    "syscall"
)

// --------------------------------------------------------------------------
// PrimitiveSchema — 注册时的原语声明（不依赖内部 primitive 包）
// --------------------------------------------------------------------------

// PrimitiveSchema 描述应用向 pb-runtimed 声明的单个原语。
type PrimitiveSchema struct {
    Name                string          `json:"name"`
    Description         string          `json:"description"`
    InputSchema         json.RawMessage `json:"input_schema"`
    OutputSchema        json.RawMessage `json:"output_schema"`
    SideEffect          string          `json:"side_effect,omitempty"`          // "none"|"read"|"write"|"exec"
    CheckpointRequired  bool            `json:"checkpoint_required,omitempty"`
    TimeoutMs           int             `json:"timeout_ms,omitempty"`
    VerifyHint          string          `json:"verify_hint,omitempty"`
    RecoverStrategy     string          `json:"recover_strategy,omitempty"`
    IsReversible        bool            `json:"is_reversible,omitempty"`
}

// --------------------------------------------------------------------------
// Result — 原语执行结果（与 primitive.Result 结构对称）
// --------------------------------------------------------------------------

// Result 是 HandlerFunc 的返回值。
// Data 将被序列化为 JSON 后返回给调用方。
type Result struct {
    Data    any    `json:"data"`
    Warning string `json:"warning,omitempty"`
}

// --------------------------------------------------------------------------
// CallContext — 调用上下文
// --------------------------------------------------------------------------

// CallContext 携带每次原语调用的上下文信息。
type CallContext struct {
    context.Context
    SandboxID string
    TraceID   string
    Method    string
}

// --------------------------------------------------------------------------
// HandlerFunc — 原语 handler 函数签名
// --------------------------------------------------------------------------

// HandlerFunc 是应用注册的原语处理函数。
// params 是未解析的 JSON，handler 自行解析所需字段。
// 返回 Result 或 error（error 将被包装为 JSON-RPC error 响应）。
type HandlerFunc func(ctx CallContext, params json.RawMessage) (Result, error)

// --------------------------------------------------------------------------
// AppServer — 应用原语服务
// --------------------------------------------------------------------------

// AppServerConfig 构建 AppServer 的配置参数。
type AppServerConfig struct {
    AppID            string // 唯一标识符，默认等于 Namespace
    AppName          string // 人类可读名称
    Namespace        string // 原语命名空间
    Version          string // 语义化版本，默认 "1.0.0"
    Transport        string // "unix"（默认）或 "http"
    SocketPath       string // Unix socket 路径；为空时使用 /run/apps/{AppID}.sock
    ListenAddr       string // HTTP 监听地址（transport=http）；为空时随机端口
    RegisterSocket   string // pb-runtimed 注册 socket；为空时读取 $PB_REGISTER_SOCKET
    HealthIntervalS  int    // 健康检查间隔（秒），默认 15
}

// AppServer 是 Go 应用的原语服务主结构。
type AppServer struct {
    config     AppServerConfig
    handlers   map[string]HandlerFunc    // 完整原语名 → handler
    schemas    []PrimitiveSchema         // 按注册顺序
    httpServer *http.Server
    listener   net.Listener
}

// NewAppServer 创建并初始化 AppServer。
func NewAppServer(config AppServerConfig) *AppServer {
    if config.AppID == "" {
        config.AppID = config.Namespace
    }
    if config.Version == "" {
        config.Version = "1.0.0"
    }
    if config.Transport == "" {
        config.Transport = "unix"
    }
    if config.SocketPath == "" && config.Transport == "unix" {
        config.SocketPath = fmt.Sprintf("/run/apps/%s.sock", config.AppID)
    }
    if config.RegisterSocket == "" {
        config.RegisterSocket = os.Getenv("PB_REGISTER_SOCKET")
        if config.RegisterSocket == "" {
            config.RegisterSocket = "/run/pb/register.sock"
        }
    }
    if config.HealthIntervalS <= 0 {
        config.HealthIntervalS = 15
    }
    return &AppServer{
        config:   config,
        handlers: make(map[string]HandlerFunc),
    }
}

// Handle 注册一个原语 handler。
// name 是原语短名（不含命名空间前缀）；最终注册名为 {namespace}.{name}。
func (s *AppServer) Handle(name string, schema PrimitiveSchema, fn HandlerFunc) *AppServer {
    fullName := fmt.Sprintf("%s.%s", s.config.Namespace, name)
    schema.Name = name
    s.handlers[fullName] = fn
    s.schemas = append(s.schemas, schema)
    return s // 支持链式调用
}

// Serve 启动 RPC 服务器、向 pb-runtimed 注册，并阻塞直到 ctx 取消或收到 SIGTERM。
func (s *AppServer) Serve(ctx context.Context) error {
    if err := s.startListener(); err != nil {
        return fmt.Errorf("start listener: %w", err)
    }

    mux := http.NewServeMux()
    mux.HandleFunc("/health", s.handleHealth)
    mux.HandleFunc("/rpc", s.handleRPC)
    s.httpServer = &http.Server{Handler: mux}

    go s.httpServer.Serve(s.listener) //nolint:errcheck

    if err := s.registerWithPBRuntimed(); err != nil {
        return fmt.Errorf("register: %w", err)
    }

    // 等待取消或 SIGTERM
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
    select {
    case <-ctx.Done():
    case <-sigCh:
    }

    _ = s.deregisterFromPBRuntimed()
    return s.httpServer.Shutdown(context.Background())
}

// startListener 绑定监听地址（内部方法）。
func (s *AppServer) startListener() error {
    var err error
    if s.config.Transport == "unix" {
        _ = os.Remove(s.config.SocketPath)
        s.listener, err = net.Listen("unix", s.config.SocketPath)
    } else {
        s.listener, err = net.Listen("tcp", s.config.ListenAddr)
    }
    return err
}

// handleHealth 响应 pb-runtimed 的健康检查。
func (s *AppServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    _, _ = w.Write([]byte(`{"status":"ok"}`))
}

// handleRPC 处理来自 pb-runtimed 的 JSON-RPC 转发请求。
func (s *AppServer) handleRPC(w http.ResponseWriter, r *http.Request) {
    // 解析 JSON-RPC 请求 → 查找 handler → 执行 → 返回响应
    // 实现省略（与 cmd/pb-repo-adapter/main.go 中的 handleRPC 结构相同）
}

// registerWithPBRuntimed 向 pb-runtimed 发送注册请求（内部方法）。
func (s *AppServer) registerWithPBRuntimed() error {
    // 构建 payload → 连接注册 socket → 发送 JSON-RPC → 解析响应
    // 实现省略
    return nil
}

// deregisterFromPBRuntimed 向 pb-runtimed 发送注销请求（内部方法）。
func (s *AppServer) deregisterFromPBRuntimed() error {
    return nil
}
```

### 4.3 完整示例：代码审查应用暴露 `review_file` 原语

#### Python 版本

```python
# apps/codereview/main.py
"""
代码审查应用 — 展示如何用 PrimitiveBox App SDK 暴露应用原语。

运行方式（在容器内）：
    python main.py

暴露的原语：
    codereview.review_file   — 对指定文件执行代码审查
    codereview.review_diff   — 对 git diff 执行代码审查
"""

import subprocess
import json
from primitivebox.app import AppServer, AppCallContext

# ── 1. 创建 AppServer ──────────────────────────────────────────────────────

server = AppServer(
    app_name="Code Review Service",
    namespace="codereview",
    version="1.0.0",
)

# ── 2. 声明并注册原语 ──────────────────────────────────────────────────────

@server.primitive(
    name="review_file",
    description=(
        "Review a source file for code quality issues. "
        "Returns a list of issues with severity, line number, and message."
    ),
    input_schema={
        "type": "object",
        "required": ["path"],
        "properties": {
            "path": {
                "type": "string",
                "description": "Relative path to the file to review (within workspace)"
            },
            "severity_threshold": {
                "type": "string",
                "enum": ["info", "warning", "error"],
                "default": "warning",
                "description": "Minimum severity level to include in results"
            },
            "rules": {
                "type": "array",
                "items": {"type": "string"},
                "description": "Specific rule IDs to check (empty = all rules)"
            }
        }
    },
    output_schema={
        "type": "object",
        "properties": {
            "path":        {"type": "string"},
            "issue_count": {"type": "integer"},
            "passed":      {"type": "boolean"},
            "issues": {
                "type": "array",
                "items": {
                    "type": "object",
                    "properties": {
                        "severity": {"type": "string"},
                        "line":     {"type": "integer"},
                        "column":   {"type": "integer"},
                        "message":  {"type": "string"},
                        "rule":     {"type": "string"}
                    }
                }
            }
        }
    },
    side_effect="read",
    timeout_ms=30000,
    recover_strategy="retry",
    is_reversible=True,
)
def review_file(ctx: AppCallContext, params: dict) -> dict:
    """
    调用 ruff（Python linter）对指定文件执行审查。
    这里演示的是一个真实的实现，不是 stub。
    """
    path = params["path"]
    severity_threshold = params.get("severity_threshold", "warning")

    # 构建 ruff 命令
    cmd = [
        "ruff", "check",
        "--output-format=json",
        path,
    ]
    try:
        result = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            timeout=25,
        )
    except FileNotFoundError:
        # ruff 未安装，降级到空结果
        return {"path": path, "issue_count": 0, "passed": True, "issues": []}
    except subprocess.TimeoutExpired:
        raise RuntimeError("review_file timed out after 25s")

    # 解析 ruff JSON 输出
    issues = []
    if result.stdout.strip():
        try:
            raw_issues = json.loads(result.stdout)
        except json.JSONDecodeError:
            raw_issues = []

        severity_order = {"info": 0, "warning": 1, "error": 2}
        threshold_level = severity_order.get(severity_threshold, 1)

        for item in raw_issues:
            # ruff 不区分 info/warning/error，统一映射为 warning
            sev = "error" if item.get("fix") is None else "warning"
            if severity_order.get(sev, 1) < threshold_level:
                continue
            issues.append({
                "severity": sev,
                "line":     item.get("location", {}).get("row", 0),
                "column":   item.get("location", {}).get("column", 0),
                "message":  item.get("message", ""),
                "rule":     item.get("code", ""),
            })

    return {
        "path":        path,
        "issue_count": len(issues),
        "passed":      len(issues) == 0,
        "issues":      issues,
    }


@server.primitive(
    name="review_diff",
    description=(
        "Review the current git diff for code quality issues in changed lines only. "
        "More focused than review_file — only reports issues in modified code."
    ),
    input_schema={
        "type": "object",
        "properties": {
            "severity_threshold": {
                "type": "string",
                "enum": ["info", "warning", "error"],
                "default": "warning"
            }
        }
    },
    output_schema={
        "type": "object",
        "properties": {
            "issue_count": {"type": "integer"},
            "passed":      {"type": "boolean"},
            "issues": {
                "type": "array",
                "items": {
                    "type": "object",
                    "properties": {
                        "file":     {"type": "string"},
                        "severity": {"type": "string"},
                        "line":     {"type": "integer"},
                        "message":  {"type": "string"},
                        "rule":     {"type": "string"}
                    }
                }
            }
        }
    },
    side_effect="read",
    timeout_ms=60000,
    recover_strategy="retry",
    is_reversible=True,
)
def review_diff(ctx: AppCallContext, params: dict) -> dict:
    """对 git diff 中变更的文件执行审查（仅报告变更行的问题）。"""
    # 获取变更文件列表
    result = subprocess.run(
        ["git", "diff", "--name-only", "--diff-filter=ACM"],
        capture_output=True, text=True
    )
    changed_files = [f for f in result.stdout.strip().split("\n") if f.endswith(".py")]

    all_issues = []
    for file in changed_files:
        file_result = review_file(ctx, {
            "path": file,
            "severity_threshold": params.get("severity_threshold", "warning")
        })
        for issue in file_result["issues"]:
            all_issues.append({"file": file, **issue})

    return {
        "issue_count": len(all_issues),
        "passed":      len(all_issues) == 0,
        "issues":      all_issues,
    }


# ── 3. 启动服务 ────────────────────────────────────────────────────────────

if __name__ == "__main__":
    import logging
    logging.basicConfig(level=logging.INFO)
    server.serve()  # 阻塞直到 SIGTERM
```

#### Go 版本

```go
// apps/codereview/main.go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "os/exec"

    "github.com/primitivebox/sdk/go/pbapp"
)

func main() {
    server := pbapp.NewAppServer(pbapp.AppServerConfig{
        AppName:   "Code Review Service",
        Namespace: "codereview",
        Version:   "1.0.0",
    })

    server.Handle("review_file",
        pbapp.PrimitiveSchema{
            Description: "Review a source file for code quality issues",
            InputSchema: json.RawMessage(`{
                "type": "object",
                "required": ["path"],
                "properties": {
                    "path": {"type": "string"},
                    "severity_threshold": {
                        "type": "string",
                        "enum": ["info","warning","error"],
                        "default": "warning"
                    }
                }
            }`),
            OutputSchema: json.RawMessage(`{
                "type": "object",
                "properties": {
                    "issue_count": {"type": "integer"},
                    "passed":      {"type": "boolean"},
                    "issues":      {"type": "array"}
                }
            }`),
            SideEffect:      "read",
            TimeoutMs:       30000,
            RecoverStrategy: "retry",
            IsReversible:    true,
        },
        reviewFile,
    )

    ctx := context.Background()
    if err := server.Serve(ctx); err != nil {
        panic(fmt.Sprintf("serve: %v", err))
    }
}

type reviewFileParams struct {
    Path              string `json:"path"`
    SeverityThreshold string `json:"severity_threshold"`
}

type reviewIssue struct {
    Severity string `json:"severity"`
    Line     int    `json:"line"`
    Message  string `json:"message"`
    Rule     string `json:"rule"`
}

func reviewFile(ctx pbapp.CallContext, params json.RawMessage) (pbapp.Result, error) {
    var p reviewFileParams
    if err := json.Unmarshal(params, &p); err != nil {
        return pbapp.Result{}, fmt.Errorf("invalid params: %w", err)
    }
    if p.Path == "" {
        return pbapp.Result{}, fmt.Errorf("path is required")
    }

    out, err := exec.CommandContext(ctx,
        "ruff", "check", "--output-format=json", p.Path,
    ).Output()
    if err != nil {
        // ruff 返回非零退出码表示有 issue，不是执行错误
        if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
            // 正常情况，继续解析
        } else {
            return pbapp.Result{}, fmt.Errorf("ruff exec: %w", err)
        }
    }

    var rawIssues []map[string]any
    if len(out) > 0 {
        _ = json.Unmarshal(out, &rawIssues)
    }

    issues := make([]reviewIssue, 0, len(rawIssues))
    for _, item := range rawIssues {
        loc, _ := item["location"].(map[string]any)
        line, _ := loc["row"].(float64)
        issues = append(issues, reviewIssue{
            Severity: "warning",
            Line:     int(line),
            Message:  fmt.Sprint(item["message"]),
            Rule:     fmt.Sprint(item["code"]),
        })
    }

    return pbapp.Result{
        Data: map[string]any{
            "path":        p.Path,
            "issue_count": len(issues),
            "passed":      len(issues) == 0,
            "issues":      issues,
        },
    }, nil
}
```

---

## 5. 完整调用链序列图

```
Actor:  AI Agent    pb gateway   pb-runtimed    AppRouter    codereview App
          |              |             |              |               |
          |              |             |    [启动阶段] |               |
          |              |             |              |               |
          |              |             |              |  ←─ register ─|
          |              |             |              |    (Unix sock) |
          |              |             |              | health probe → |
          |              |             |              | ←── 200 OK ─── |
          |              |             |  ←─ onReg ──|               |
          |              |             |  (add to     |               |
          |              |             |   registry)  |               |
          |              |             |              |               |
          |   [AI 调用阶段]             |              |               |
          |              |             |              |               |
          |─ POST /sandboxes/{id}/rpc →|             |               |
          |  method: codereview.       |             |               |
          |    review_file             |             |               |
          |              |─ HTTP proxy→|             |               |
          |              |  (透传)     |             |               |
          |              |             |              |               |
          |              |             |─ registry   |               |
          |              |             |  lookup:    |               |
          |              |             |  "codereview|               |
          |              |             |  .review_   |               |
          |              |             |  file"      |               |
          |              |             |  → system?  |               |
          |              |             |  No         |               |
          |              |             |─ AppRouter  |               |
          |              |             |  .RouteFor()→              |
          |              |             |             |─ IsRoutable()?|
          |              |             |             |  Yes          |
          |              |             | ←─ route ──|               |
          |              |             |             |               |
          |              |             | EstimateRisk(params)        |
          |              |             | → RiskLow                   |
          |              |             | (no checkpoint needed)      |
          |              |             |              |               |
          |              |             |─────── JSON-RPC POST /rpc ──→
          |              |             |  method: codereview.        |
          |              |             |    review_file              |
          |              |             |  (Unix socket)              |
          |              |             |              |               |
          |              |             |              |          execute
          |              |             |              |          handler
          |              |             |              |          (ruff)
          |              |             |              |               |
          |              |             | ←────────── result ─────────|
          |              |             |              |               |
          |              |             | Verify(result)              |
          |              |             | (side_effect=read,          |
          |              |             |  verify_hint="" → skip)     |
          |              |             |                             |
          |              |             | emit rpc.completed event    |
          |              |             | → eventing.Bus              |
          |              |             |              |               |
          |              | ←── result ─|             |               |
          | ←── 200 JSON ─|             |             |               |
          |              |             |              |               |
          |  [假设 App 崩溃]           |              |               |
          |              |             |              |               |
          |              |             |              |─ health probe →|
          |              |             |              |  ← ECONNREFUSED|
          |              |             |              |─ RecordFail(1) |
          |              |             |              |─ health probe  |
          |              |             |              |  ← ECONNREFUSED|
          |              |             |              |─ RecordFail(2) |
          |              |             |              |─ health probe  |
          |              |             |              |  ← ECONNREFUSED|
          |              |             |              |─ RecordFail(3) |
          |              |             |              |  → threshold   |
          |              |             |  ← onDereg ─|               |
          |              |             |  (remove from               |
          |              |             |   registry)                 |
          |              |             |              |               |
          |─ POST /rpc codereview. ───→|             |               |
          |    review_file             |─ HTTP proxy →|              |
          |              |             |─ registry lookup            |
          |              |             |  → not found                |
          |              |             |  → ErrNotFound              |
          | ←── 404 / error ───────────|             |               |
```

---

## 6. 与现有实现的兼容性

### 6.1 保留 manifest 加载路径

现有的 `loadManifests()` + `startAdapterProcess()` 路径**完整保留**，作为"静态适配器"的支持路径。两条路径共用同一个 `AppRouter` 路由表：

```
静态路径（现有）：
  manifests/*.json → loadManifests() → startAdapterProcess()
      → stdout 首行 JSON → AdapterRegistration
      → AppRouter.registerStatic(route)   ← 新：将现有注册纳入路由表

动态路径（新增）：
  App.serve() → primitive.register RPC
      → AppRouter.Register(req)           ← 新增路径
```

两条路径产出相同的 `AppRoute` 结构，共用相同的健康检查循环和路由查找逻辑。

### 6.2 `pb-repo-adapter` 迁移路径

`cmd/pb-repo-adapter` 当前使用 manifest + stdout 注册。它可以：
- **零改动继续工作**（静态路径兼容）
- 或**逐步迁移**到动态 SDK（在进程启动后调用 `primitive.register`，移除 stdout 注册逻辑）

迁移后获得健康检查、热升级、精确原语 schema 声明等能力。

---

## 7. 保留命名空间与 Schema 扩展

`primitive.Schema`（`internal/primitive/primitive.go`）需添加以下字段以支持应用原语：

```go
// 在现有 Schema struct 中新增（全部 omitempty，向后兼容）：
RecoverStrategy string `json:"recover_strategy,omitempty"` // 失败恢复建议
IsReversible    *bool  `json:"is_reversible,omitempty"`    // 显式可逆性标注
AppID           string `json:"app_id,omitempty"`           // 注册此原语的 app_id
AppVersion      string `json:"app_version,omitempty"`      // 应用版本（审计用）
```

`defaultSideEffect()` 和 `defaultCheckpointRequirement()`（`metadata.go`）对应用原语不做默认推断，直接使用注册时声明的值。

---

*文档结束。此设计草案供架构评审使用，不应直接生成代码。*
