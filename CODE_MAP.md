# mini-agents 代码地图

> 当前进度：**M1 + M2 + M3 + M4 + M4.5 + M5 + M6 + M8-1 全部达成**。此文档是代码框架的总览，配合 PROGRESS.md 学习进度一起看。

---

## 一、目录总览（源码约 3100 行，3 个可执行程序）

```
mini-agents/
├── go.mod / go.sum             模块定义 + 依赖锁定（sqlite 驱动、ulid）
├── CODE_MAP.md                 本文件：代码地图
├── PROGRESS.md                 学习进度清单（课程表 + 教学点）
├── README.md                   项目说明 + 快速开始
│
├── cmd/                        「可执行程序」入口目录
│   ├── server/main.go 约400行   HTTP 服务：路由 + handler + JSON + 静态文件
│   ├── server/main_test.go 约90行 HTTP handler 测试
│   ├── agent/main.go  约283行   agent 进程：认领→读任务→调引擎→记账→汇报（含孤儿回收）
│   └── dump/main.go   88行      调试工具：打印任务/队列/执行日志
│
├── internal/                   「内部业务代码」目录（external 不可引用）
│   ├── store/                  数据层（只管数据，不碰 HTTP）
│   │   ├── schema.sql  约106行  9 张表建表语句（幂等）+ 业务索引
│   │   ├── store.go   约1300行  数据操作 + 事务 + 状态同步 + 孤儿回收 + 增量消息 + 知识库
│   │   └── store_test.go 约620行 测试：任务/会话消息/重试/blocked/级联/团队状态/状态同步/孤儿回收/消息事务/知识库
│   └── agent/                  Agent 执行器
│       ├── runner.go 79行       ClaudeEngine：封装 claude -p 调用
│       ├── pi.go     91行       PiEngine：pi --mode rpc JSONL 流式执行
│       ├── deepseek.go 22行     DeepSeekEngine：预留 stub
│       ├── fake.go   24行       FakeEngine：干跑测试用
│       ├── engine.go 18行       NewEngine 工厂（按名字选实现）
│       └── engine_test.go 约40行 引擎测试（FakeEngine + NewEngine 工厂）
│   ├── memory/                  M7 双层知识库门面
│   │   └── memory.go 约40行      Capture/Recall/RecallForAgent
│
└── web/                        前端目录（M6 飞书式「夜间调度台」）
    └── index.html 约613行       会话列表 + 消息流 + 链式轮询 + 增量拉取 + 无障碍
```

## 二、分层架构（三层，单向依赖）

```
┌─────────────────────────────────────────────────────┐
│ 第 1 层：HTTP 层（cmd/server/main.go）               │
│   职责：接待请求、翻译 JSON、给状态码               │
│   依赖：→ 数据层                                    │
└────────────────────────┬────────────────────────────┘
                         │ 调用
┌────────────────────────▼────────────────────────────┐
│ 第 2 层：数据层（internal/store/）                   │
│   职责：所有"存/取"操作，SQL 只在这一层             │
│   依赖：→ SQLite 驱动                                │
└────────────────────────┬────────────────────────────┘
                         │ 执行 SQL
┌────────────────────────▼────────────────────────────┐
│ 第 3 层：数据库（SQLite 文件）                       │
│   职责：真正持久化数据                              │
└─────────────────────────────────────────────────────┘
```

Agent 执行器（`internal/agent/`）与数据层**平级**，被 `cmd/agent` 依赖注入——runner 只管执行，不碰数据库。

## 三、数据层：store.go 的方法（工具箱）

| 分类 | 方法 | 作用 |
|---|---|---|
| 生命周期 | `Open` / `Close` | 打开（自动建表 + 旧库迁移）/ 关闭数据库 |
| 工具 | `NewID` | 生成 ULID 主键 |
| 员工 | `CreateAgent` / `ListAgents` / `GetAgent` / `GetAgentByID` / `UpdateAgent` / `DeleteAgent` | 入职 / 列员工 / 按名字查 / 按 id 查 / 改名 / 硬删除 |
| 任务 | `CreateIssue` / `ListIssues` / `GetIssue` | 建任务（事务写入 issues + task_queue）/ 列任务 / 按 id 取任务 |
| 队列 | `EnqueueTask` / `ClaimTask` / `StartTask` | 入队 / 乐观锁认领（写 dispatched_at）/ 标记开工 |
| 汇报 | `CompleteTask` / `FailTask` / `BlockTask` | 幂等完成 / 幂等失败（重试或 blocked）/ 直接 blocked，均同步 issues.status |
| 回收 | `RequeueStaleTasks` | 回收超时的 dispatched/running 孤儿任务 |
| 记账 | `RecordRun` / `GetLatestRun` / `ListRuns` | 写执行日志 / 读上游最近输出 / 看执行日志 |
| 会话 | `CreateConversation` / `ListConversationsWithMembers` / `GetConversation` / `RenameConversation` / `ListConversationMembers` | 会话与成员、群聊改名 |
| 消息 | `SendMessage` / `SendMessageWithTasks` / `AttachTasks` / `ListMessages` / `ListMessagesAfter` / `GetMessageByTask` / `GetConversationContext` | 消息与任务关联、增量拉取 |
| 查看 | `ListQueue` / `TeamStatus` | 看队列 / 看团队状态 |
| 知识库 | `CaptureMemory` / `RecallMemory` / `RecallMemoryForAgent` | M7 双层知识库：写入 / 按作用域读取 / 拼装注入文本 |

**核心技巧**：`ClaimTask` 用一条 `UPDATE ... WHERE status='queued'` 实现"抢 + 标记"原子操作（乐观锁），配合 `claim_token` 凭证和 `status IN (...)` 校验实现幂等。

## 四、完整数据流（一条任务的生命周期，M3）

```
curl -X POST /api/issues  {"title":"用一句话介绍 Go 语言","assignee":"小王"}
   │
   ▼ main.go: handleCreateIssue
   │   读 body → 校验 → CreateIssue（事务写入 issues 表 + task_queue 入队）
   ▼ store.go
   │   issues 多一行，task_queue 多一行（queued）
   ▼ cmd/agent -name 小王（每 2 秒一轮，先回收孤儿任务）
   │   ClaimTask（乐观锁抢到 → dispatched，记 dispatched_at）
   │   StartTask（→ running，记 started_at，同步 issues.status=in_progress）
   │   GetIssue（读任务内容）
   │   runner.Execute（按 agents.engine 选 claude/pi/fake 执行，超时保护 2 分钟）
   │   RecordRun（写 agent_runs：回答 + 耗时 + 退出码，成败都记）
   │   CompleteTask / FailTask / BlockTask（→ completed/queued/blocked，同步 issues.status）
   ▼ 结果
   issues / task_queue / agent_runs 三表联动更新
```

## 五、三张表的分工

| 表 | 角色 | 关键列 |
|---|---|---|
| `issues` | 待办清单（人看：是什么） | id, title, status, assignee_type/id |
| `task_queue` | 流水线（机器看：做到哪一步） | id, issue_id, status, attempts, claim_token, worker_id |
| `agent_runs` | 记账本（事后查：干得怎么样） | id, task_id, exit_code, output, duration_ms |

## 六、已学知识（复习索引）

Go 基础：package/import/struct/方法接收者/多返回值/error/切片/append/指针
HTTP：ServeMux 方法路由/handler/JSON 编解码/状态码/闭包/依赖注入
数据库：Exec/Query/QueryRow/Scan/占位符防注入/幂等建表/资源释放/`sql.NullString|NullTime`
并发调度：乐观锁认领/`claim_token` 凭证/幂等完成/状态机
进程调用：os/exec/context 超时/Windows 编码坑（直连 claude.exe 而非 cmd shim）
调试：读错误信息/t.TempDir()/Windows 文件句柄/UTF-8 编码

## 七、后续计划（未排期）

| 项 | 内容 |
|---|---|
| 第 9 课 | web/index.html 前端页面 |
| 第 10 课 | 多 worker 并发验证（乐观锁实战） |
| 第 11 课 | 重试机制（attempts 字段） |
