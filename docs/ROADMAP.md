# Roadmap

---

## Now · 当前阶段（进行中）

### 集成联调

目标：后端 + Python SDK + `cmd/pb-ui` 前端三者拉通，完整调用链路可演示

验收：
- CORS 正常（当前已限制 `localhost:5173`），前端可调用后端所有 API
- SSE 事件流在前端实时渲染（`/api/v1/events/stream`，最新在前，最多 200 条）
- `style.*` / `verify.*` 原语可通过 `HTMLLayoutServer` 完整调用
- `/tmp/integration_report.txt` 无 blocking 遗留问题

---

## Next · 下一阶段

优先级排序依据：验证机制成熟度 > 用户价值 > 实现复杂度

### N1 · 文档原语（doc.*）

优先级：高

理由：验证机制可以写成结构规则（章节完整性、引用未断裂），
  不依赖 AI 判断，是继代码原语之后第二个能跑通完整 CVR 闭环的领域。

原语集合：

| 原语 | 说明 |
|------|------|
| `doc.read` | 读取结构化文档（标题树、段落、表格） |
| `doc.insert` | 在指定章节插入内容 |
| `doc.replace` | 替换指定片段（`Reversible=false`，强制 checkpoint） |
| `doc.verify_struct` | 验证文档结构完整性（Layer A 自验证） |
| `doc.export` | 格式转换 |

支持格式：Markdown（优先）、DOCX（python-docx）

验收：`doc.replace` 执行后测试失败能自动 rollback，完整 CVR 闭环可演示

### N2 · 邮件代理（email.*）

优先级：中

理由：`email.send` 是第一个需要 human-in-the-loop 的原语，
  能演示 `CVRCoordinator` 的 `escalate` 恢复路径（`RecoveryAction` 枚举已含此值），
  是"AI 原生"最有说服力的演示场景之一。

原语集合：

| 原语 | 说明 |
|------|------|
| `email.draft` | 写入草稿，不发送（`Reversible=true`） |
| `email.review` | AI 自检（`VerifyStrategy`） |
| `email.send` | `Reversible=false` + `RiskHigh`，强制人工确认后执行 |
| `email.schedule` | 延迟发送，窗口期内可撤回 |

关键设计：`email.send` 触发 `escalate` 恢复动作，前端展示确认弹窗，
  用户批准后才继续（需前端配合 N3）

### N3 · cmd/pb-ui 前端完善

优先级：中

依赖：集成联调完成

剩余工作：
- Sandbox 创建/删除操作连接后端 API（当前为只读视图）
- App Primitive 面板：展示已注册原语列表（`/app-primitives`）
- Primitive 调用面板：基于 `input_schema` 渲染可交互表单
- CVR trace 详情展示（`CheckpointID` / `LayerAOutcome` / `RecoveryPath`）
- human-in-the-loop 确认弹窗（N2 依赖）

---

## Later · 暂缓项

以下方向已评估，暂不推进，原因已记录。

### 设计原语（design.*）

暂缓原因：视觉语义没有客观验证机制，"好不好看"无法写成 `VerifyStrategy`。
  `verify.contrast` 已覆盖了有客观标准的视觉验证部分。
  等 AI 视觉评估能力成熟后重新评估。

### Kubernetes runtime

暂缓原因：Docker 后端尚未达到稳定标准，K8s 是架构意图而非当前需求。
  `internal/sandbox/kubernetes.go` 已有骨架实现，`TestKubernetesDriver*` 为已知失败。
  等 Docker 路径完全稳定后再推进。

### 多语言 SDK（TypeScript / Rust）

暂缓原因：Python SDK 仍在收敛，过早扩展语言会分散维护精力。
  前端通过 HTTP RPC 直接调用，不需要 TypeScript SDK。
  等 Python SDK 接口稳定（v1.0）后再考虑。

### 普通用户界面

暂缓原因：需要在工具链设计层面增加"翻译层"（技术概念 → 用户概念），
  这是独立的产品工程工作。
  当前阶段专注开发者工具，普通用户界面作为独立项目规划。

### Application Primitive 持久化注册

暂缓原因：当前 `inMemoryAppRegistry` 重启后丢失，但对开发调试场景影响可接受。
  SQLite 持久化作为 M3 后续迭代项，不阻塞当前工作。
  `CheckpointManifestStore` 的 SQLite 实现已验证可行，迁移成本低。

### AIJudgeStrategy

暂缓原因：`VerifyStrategy` 接口已就绪，`CVRDepth` 递归防护（`MaxCVRDepth=5`）已实现。
  AI judge 调用本身需要稳定的 LLM 集成层，不宜在基础设施尚不稳定时引入。
  等 `TestSuiteStrategy` 路径稳定后再实现。
