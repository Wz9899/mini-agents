# mini-agents V2 设计蓝图：多 Agent 团队协调工具

> 日期：2026-08-12
> 来源：/grill-me 会话逐决策产出（用户确认）
> 状态：设计已共识，待按 M4–M7 实施

## 1. 背景与目标

V1（M1–M3）是一个"单 worker 调 claude"的最简闭环。用户提出新需求：把 mini-agents 变成一个**能协调多个 Agent 进行工作的工具**：

- 每个 Agent 处理**不同的工作内容**（分工）
- 允许**同时启动多个 Agent**（并行）
- 每个 Agent 是一个**团队员工**，整体像一个**项目团队**

本次设计通过逐决策拷问（grilling）敲定全部骨架，本文档是后续 M4–M7 课程的纲领。

## 2. 核心决策记录（grilling 产出）

| # | 决策点 | 选择 | 一句话 |
|---|---|---|---|
| 1 | 多 Agent 协调模型 | **显式指派** | 任务创建时写明派给谁，无"项目经理"，人类通过 API 协调 |
| 2 | Agent 运行形态 | **每 Agent 一个进程** | `cmd/agent -name 小王`，多开终端 = 并行团队 |
| 3 | Agent 身份存储 | **数据库 `agents` 表** | name/role/description，可校验、可列出 |
| 4 | 记忆组织 | **分层（团队公共 + 个人私有）** | `memory.scope`：`team` / `agent_<id>` |
| 5 | 任务指派粒度 | **指派到具体 Agent** | `issues.assignee_id` 关联 agents |
| 6 | 失败处理 | **自动重试 N 次** | `attempts` 字段，`failed→queued` 回退 |
| 7 | 问题上报触发 | **两者结合** | 重试耗尽自动上报 + claude 主动"我无法完成"上报 |
| 8 | 上报通知渠道 | **数据库留痕 + 终端告警** | `blocked` 状态 + `reason` + 红色横幅 |
| 9 | 前端形态 | **团队看板**（目标形态） | Agent 卡片 + 任务分组 + blocked 标红 + 创建指派下拉 |
| 10 | 实时实现 | **前端 2 秒轮询** | `GET /api/team`，任务级 4 段进度条 + 团队统计 |
| 11 | 实施顺序 | **A：核心→可靠→前端→记忆** | M4→M5→M6→M7 |

## 3. 架构

### 3.1 运行形态

```
┌──────────────────────────────────────────────────────────┐
│ 人类（协调者）                                             │
│   POST /api/issues {assignee:"小王"}  ← 指派任务给具体 Agent │
└──────────────────────────┬───────────────────────────────┘
                           │
        ┌──────────────────┼──────────────────┐
        │                  │                  │
┌───────▼──────┐  ┌───────▼──────┐  ┌───────▼──────┐
│ cmd/agent     │  │ cmd/agent     │  │ cmd/agent     │
│ -name 小王    │  │ -name 小李    │  │ -name 小赵    │
│ 前端工程师     │  │ 后端工程师    │  │ 测试工程师    │
│ 只认领自己的任务│  │ 只认领自己的任务│  │ 只认领自己的任务│
└───────┬──────┘  └───────┬──────┘  └───────┬──────┘
        │                  │                  │
        └──────────────────┼──────────────────┘
                           ▼
              ┌──────────────────────────┐
              │ SQLite（单文件共享库）     │
              │ agents/issues/task_queue │
              │ agent_runs/memory        │
              └──────────────────────────┘
```

要点：
- 多个 Agent 进程**共享同一个 SQLite 文件**，靠**乐观锁**保证互不抢活（每个 Agent 只认领 `assignee_id=自己` 的任务）
- 并发写库需要 **WAL 模式 + busy_timeout**（M4 落地时教）

### 3.2 目录结构变化

```
mini-agents/
├── cmd/
│   ├── server/        HTTP 服务（任务 + 团队 API）   [改造]
│   ├── worker/        → 改名为 agent/（-name 参数）   [改造]
│   ├── dump/          调试工具（加"待处理"过滤）      [改造]
│   └── agent/         worker 更名后的新入口
├── internal/
│   ├── store/         schema.sql + store.go（+agents/memory 方法）
│   └── agent/         claude 执行器（prompt 注入角色人设） [改造]
│   └── memory/        【M7】记忆层（Recall/Capture/作用域）
├── web/               【M6】团队看板前端（index.html）
└── ...（原文件）
```

## 4. 数据模型（5 张表）

| 表 | 角色 | 字段 | 变化 |
|---|---|---|---|
| `agents` | 员工档案 | id, name, role, description, created_at | **新增（M4）** |
| `issues` | 待办清单 | id, title, description, status, **assignee_id**, ... | +assignee_id（M4） |
| `task_queue` | 执行流水线 | id, issue_id, status, **attempts**, claim_token, worker_id, error | +attempts（M5） |
| `agent_runs` | 记账本 | id, task_id, exit_code, output, duration_ms | 不变 |
| `memory` | 记忆库 | id, **scope**, content, agent_id?, created_at | **新增（M7）** |

### 4.1 状态机（扩展版）

```
                  认领            开工          完成
queued ──────► dispatched ────► running ────► completed
  │  ▲                               │
  │  │ attempts<上限                  │ attempts 耗尽
  ▼  │                               ▼
failed ◄── 失败                    blocked ← 需人介入
  │                                    │
  └─ 回退（failed→queued）             └─ reason + 终端告警
```

- **failed**：技术性失败。`attempts < 上限` → 回退 `queued` 自动重试；耗尽 → 保持 failed
- **blocked**：需要人类介入。来源：① claude 输出"我无法完成：<原因>" ② 重试耗尽。带 `reason` 字段
- 认领 SQL 变化：`WHERE status='queued' AND assignee_id=?`

## 5. API 设计

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | /api/agents | 员工入职：{name, role, description} |
| GET | /api/agents | 列出团队 |
| POST | /api/issues | 创建任务：{title, description, assignee}（校验 assignee 存在） |
| GET | /api/issues | 任务列表 |
| GET | /api/team | **轮询接口**：所有 Agent + 各自任务 + 状态统计 |
| GET | /api/issues?status=blocked | 待处理列表（dump/前端用） |

## 6. 前端团队看板（M6）

- **布局**：每个 Agent 一张卡片（名字/角色/名下任务）
- **进度条**：
  - 任务级：`queued → dispatched → running → completed` 4 段横条
  - 团队级：完成比例条 + 待处理（blocked）计数
- **blocked 醒目标红**
- **创建表单**：指派下拉（数据来自 /api/agents）
- **实时**：`setInterval` 2 秒 fetch `/api/team` 轮询（零 token 消耗，只查本地库）

## 7. 记忆分层（M7）

- `memory` 表带 `scope` 字段
  - `scope='team'`：团队公共知识（项目背景、规范），所有 Agent 共享读
  - `scope='agent_<id>'`：个人经验，只有本人读写
- 方法：`Capture(scope, content)` / `Recall(scope)`（按作用域过滤）
- 执行任务时 prompt 注入：先 `Recall('team')` + `Recall('agent_<id>')`，再拼任务内容
- 解决的核心问题：Claude Code auto-memory 按仓库隐式共享导致的跨 Agent 记忆泄漏

## 8. 参考映射（哪些部分参考了谁）

| 新设计部分 | 参考项目 | 借鉴点 |
|---|---|---|
| agents 表 + 指派 | Multica | polymorphic assignee、agent 一等公民、指派可校验 |
| 状态机 + blocked | Multica / LoopX | agent_task_queue 状态机；operator_gate（需要人决策、带具体问题） |
| 认领乐观锁 | Multica / LoopX | daemon 认领；claim+lease |
| 自动重试回退 | Multica | task_queue 扩展 |
| 记忆分层 + scope | TencentDB-Agent-Memory | L0–L3 分层、session_key 作用域、4 级可见性 |
| Recall/Capture | TencentDB-Agent-Memory | capture/search/recall API 语义 |
| 显式共享解泄漏 | TencentDB-Agent-Memory | 作用域+可见性替代隐式共享 |
| 2 秒轮询 | Multica（对照） | WebSocket 是轮询升级版，留作进阶 |
| 不用外键对照 | Multica | Multica 硬规则不用外键（多态+应用层）；mini 版教学点保留对照 |

## 9. 实施计划（M4–M7）

### M4：多 Agent 核心（第 9 课）
- schema：新建 `agents` 表；`issues` 加 `assignee_id`
- store.go：`CreateAgent/ListAgents`；`CreateIssue` 支持 assignee；`ClaimTask` 按 assignee 过滤
- cmd/worker → `cmd/agent -name 小王`：启动时查 agents 表拿 role/description
- server：`POST/GET /api/agents`；`POST /api/issues` 校验 assignee
- 验证：开 2–3 个 Agent 进程，各自只做自己的活
- 教学点：SQLite 多进程写（WAL + busy_timeout）、进程身份识别

### M5：可靠性（第 10 课）
- schema：`task_queue` 加 `attempts`
- 重试：失败 → attempts+1 → `<上限` 回退 queued → 重新认领
- blocked：prompt 要求 claude"无法完成时明确回答'我无法完成：<原因>'"
  - 检测到 → 直接 blocked，不重试（省 token）
  - 重试耗尽 → blocked
- 终端红色告警 + error/reason 落库
- 教学点：状态机回退、可重试失败 vs 不可恢复问题

### M6：前端团队看板（第 11 课）
- `GET /api/team` 聚合接口
- web/index.html：Agent 卡片 + 任务分组 + 指派下拉
- 进度条：任务级 4 段 + 团队统计
- blocked 标红
- 2 秒轮询
- 教学点：轮询 vs 推送、JSON 聚合查询、前端 fetch/setInterval

### M7：记忆分层（第 12 课）
- schema：`memory` 表带 `scope`
- internal/memory/：Capture/Recall 按作用域过滤
- 执行任务时注入 team + 个人记忆
- 教学点：作用域隔离、显式共享、auto-memory 泄漏问题

## 10. 与 V1 的差异总结

| 维度 | V1 | V2 |
|---|---|---|
| Agent 形态 | 1 个无名 worker | 多进程、有名字/角色 |
| 任务归属 | worker 抢任意任务 | 指派给具体 Agent |
| 状态机 | 4 态，无回退 | +failed→queued 重试、+blocked 上报 |
| 记忆 | 无 | team/agent 分层 + 作用域 |
| 前端 | 无 | 团队看板 + 进度条 + 轮询 |
| 表 | 3 张 | 5 张 |

## 11. 多引擎演进路径（M4.5 补充，2026-08-15）

**当前实现**（M4.5）：`agents.engine` 内联字段（`claude` / `pi` / `deepseek`），配合 `internal/agent` 的 `Engine` 接口（ClaudeEngine / PiEngine / DeepSeekEngine / FakeEngine）。调用方（cmd/agent）只认 `Engine` 接口——多态。

**Multica 参考**（多引擎的更完整抽象，来自其 `agent_runtime` 表）：

| 维度 | Multica | mini-agents（当前） |
|---|---|---|
| provider（谁提供模型） | `agent_runtime.provider` | `agents.engine` |
| runtime_mode（在哪跑） | `local` / `cloud` | 只有 local，无此维度 |
| 资源复用 | agent 通过 `runtime_id` 引用独立 `agent_runtime` 表（多 agent 共享 daemon+provider） | engine 内联在 agents 表 |

**演进触发条件**（满足时才升级为 Multica 模型）：
1. 需要"云端跑 agent"（`runtime_mode='cloud'`）——本机 daemon 之外出现第二类执行场所
2. 需要"多个 agent 共享同一运行时"——engine 重复写在每行变成负担
3. 需要动态发现 provider 支持的模型（Multica 的 daemon 上报 model discovery）

**升级路径**：把 `agents.engine` 抽成独立 `agent_runtime` 表（`daemon_id` + `runtime_mode` + `provider`），agents 表加 `runtime_id` 外键。`Engine` 接口不变——换实现不换调用方，这正是接口抽象的意义。

## 12. 飞书式演进（M4.5-3/4 补充，2026-08-15）

**用户最终愿景**：做成类似飞书的软件——每个 agent 是下属，拉群安排工作，单独跟人沟通，根据想法迭代修改。

**核心转变**：从"任务驱动"到"消息驱动"。消息是入口，会话是上下文，任务（issues）只是中间的"工作单元"。

**已落地**（M4.5-3/4）：
- 三张新表：`conversations`（direct 单聊 / group 群聊）、`conversation_members`（组合主键：一个会话一个 agent 一行）、`messages`（带 `task_id` 关联触发的工作）
- API：建会话 / 发消息 / 消息流，2 秒轮询（M6 前端用）
- @解析触发：消息里 `@员工名`（`strings.Contains` 匹配）→ 创建 issues（assignee=被 @ 者）→ 入队 → agent 执行 → 输出作为回复消息写回会话（**副作用**，不改任务状态）

**关键决策**（用户确认）：@提及触发 · 消息落 issues 复用现有流水线 · 先单聊闭环再群聊。

**待做**：M6 飞书式前端（会话列表 + 消息流 + 输入框）、M7 记忆 + 会话上下文（"改蓝色"不丢上下文）、群聊多 agent @（成员校验）。
