# mini-agents 代码地图

> 当前进度：第 1~8 课完成（**M1 + M2 + M3 全部达成**）。此文档是代码框架的总览，配合 PROGRESS.md 学习进度一起看。

---

## 一、目录总览（769 行源码，3 个可执行程序）

```
mini-agents/
├── go.mod / go.sum             模块定义 + 依赖锁定（sqlite 驱动、ulid）
├── CODE_MAP.md                 本文件：代码地图
├── PROGRESS.md                 学习进度清单（课程表 + 教学点）
│
├── cmd/                        「可执行程序」入口目录
│   ├── server/main.go 74行     HTTP 服务：路由 + handler + JSON
│   ├── worker/main.go 105行    worker 进程：认领→读任务→真调 claude→记账→汇报
│   └── dump/main.go   87行     调试工具：打印任务/队列/执行日志
│
├── internal/                   「内部业务代码」目录（external 不可引用）
│   ├── store/                  数据层（只管数据，不碰 HTTP）
│   │   ├── schema.sql  36行    三张表建表语句（幂等）
│   │   ├── store.go   339行    14 个数据操作方法
│   │   └── store_test.go 39行  测试：验证 CreateIssue 真实入库
│   └── agent/runner.go 72行    Agent 执行器：封装 claude -p 调用
│
└── web/                        前端目录（第 9 课计划，当前空）
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

Agent 执行器（`internal/agent/`）与数据层**平级**，被 worker 依赖注入——runner 只管执行，不碰数据库。

## 三、数据层：store.go 的 14 个方法（工具箱）

| 分类 | 方法 | 作用 |
|---|---|---|
| 生命周期 | `Open` / `Close` | 打开（自动建表）/ 关闭数据库 |
| 工具 | `NewID` | 生成 ULID 主键 |
| 任务 | `CreateIssue` / `ListIssues` / `GetIssue` | 建任务 / 列任务 / 按 id 取任务 |
| 队列 | `EnqueueTask` / `ClaimTask` / `StartTask` | 入队 / 乐观锁认领 / 标记开工 |
| 汇报 | `CompleteTask` / `FailTask` | 幂等完成 / 幂等失败 |
| 记账 | `RecordRun` | 写执行日志（agent_runs） |
| 查看 | `ListQueue` / `ListRuns` | 看队列 / 看执行日志 |

**核心技巧**：`ClaimTask` 用一条 `UPDATE ... WHERE status='queued'` 实现"抢 + 标记"原子操作（乐观锁），配合 `claim_token` 凭证和 `status IN (...)` 校验实现幂等。

## 四、完整数据流（一条任务的生命周期，M3）

```
curl -X POST /api/issues  {"title":"用一句话介绍 Go 语言"}
   │
   ▼ main.go: handleCreateIssue
   │   读 body → 校验 → CreateIssue（写 issues 表 + 自动 EnqueueTask）
   ▼ store.go
   │   issues 多一行，task_queue 多一行（queued）
   ▼ worker 进程（死循环）
   │   ClaimTask（乐观锁抢到 → dispatched）
   │   StartTask（→ running，记 started_at）
   │   GetIssue（读任务内容）
   │   runner.Execute（真调 claude.exe -p，超时保护 2 分钟）
   │   RecordRun（写 agent_runs：回答 + 耗时 + 退出码，成败都记）
   │   CompleteTask / FailTask（→ completed/failed，记 finished_at）
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
