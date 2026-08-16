# mini-agents 优化 RD 文档

| 项目 | mini-agents |
|---|---|
| 文档版本 | v1.0 |
| 日期 | 2026-08-16 |
| 状态 | 已实施（代码已按本文档修改，待测试验证） |
| 作者 | 项目组 |
| 范围 | M1–M8-1 已完成功能的质量优化 |

---

## 1. 背景与目标

mini-agents 是一个用 Go + SQLite 实现的单机多 Agent 协调工具。当前已完成：

- M1–M3：任务 CRUD、队列、worker 执行 Claude
- M4：多 Agent 核心（`agents` 表 + `cmd/agent -name`）
- M4.5：多引擎（`Engine` 接口 + Pi/DeepSeek/Fake）
- M4.5-2/3/4：协作任务、会话与消息、单聊闭环
- M5：可靠性（重试 + blocked 上报 + 级联阻塞）
- M6：飞书式前端
- M8-1：员工管理 UI、无职责入职

随着功能快速叠加，项目出现了**数据一致性、事务边界、文档同步、运行体验**方面的欠账。本文档对这些问题进行统一梳理，给出可执行的技术方案和验收标准。

### 1.1 目标

1. 修复已发现的数据一致性问题，保证任务状态可靠。
2. 补齐关键事务边界，避免半截写。
3. 降低日志噪音，改善多 Agent 运行体验。
4. 补齐基础工程化设施（依赖整理、测试、索引），并确认运行产物已被忽略。
5. 形成可执行的实施批次和回归验收清单。

### 1.2 非目标

- M7 双层知识库已按项目计划落地（`memory` 表 + `internal/memory` + `/api/memory`），不再作为本文档的优化项。
- 不将轮询升级为 WebSocket/SSE（列入后续演进，不在本次范围）。
- 不重构整体架构（保持 `server / agent / store` 三层骨架不变）。

---

## 2. 现状架构摘要

```
cmd/server    HTTP API + 静态文件
cmd/agent     每员工一个进程，认领任务并调用引擎
cmd/dump      只读调试工具
internal/store   所有 SQL
internal/agent   Engine 接口与各引擎实现
web/index.html   飞书式前端
```

当前源码规模已经从 CODE_MAP 记录的 769 行增长到 2500 行级别，`internal/store/store.go` 单文件已达 919 行，`cmd/server/main.go` 达 389 行。

---

## 3. 问题清单

| 编号 | 优先级 | 分类 | 问题 | 风险 |
|---|---|---|---|---|
| P0-1 | P0 | 数据一致性 | `issues.status` 永远停在 `todo`，不回写 | API/前端展示错误状态 |
| P0-2 | P0 | 事务 | `CreateIssue` 与 `EnqueueTask` 非原子 | 半截写导致任务无法入队 |
| P0-3 | P0 | 事务 | 消息闭环（发消息 + 关联任务）非原子 | 任务无法反查来源消息 |
| P0-4 | P0 | 状态机 | `StartTask` 失败直接 return | 任务卡死在 `dispatched` |
| P0-5 | P0 | 状态机 | 进程被杀后 `dispatched/running` 任务无回收 | 任务永久卡死 |
| P0-6 | P0 | 安全 | 服务监听 `:8080` 且无鉴权 | 局域网可任意访问 |
| P1-1 | P1 | 工程化 | `go.mod` 直接依赖标记为 indirect | 依赖关系失真 |
| P1-2 | P1 | 工程化 | README/CODE_MAP 与代码不同步 | 新人误导 |
| P1-3 | P1 | 工程化 | 工作区存在运行产物（`.gitignore` 已存在，需确认未被跟踪） | 若历史误提交则污染仓库 |
| P1-4 | P1 | 测试 | 仅 store 有测试，handler/engine 无测试 | 回归风险高 |
| P2-1 | P2 | 体验 | agent 空闲日志每 2 秒刷屏 | 日志爆炸 |
| P2-2 | P2 | 性能 | 缺业务索引 | 数据量增长后查询变慢 |
| P2-3 | P2 | 正确性 | “我无法完成”检测不 TrimSpace | 漏判导致假完成 |
| P2-4 | P2 | 正确性 | 群聊 @ 解析用子串匹配 | 误触发 |
| P2-5 | P2 | 体验 | 前端全量重绘 | 消息多时抖动 |

---

## 4. 详细方案

### P0-1：同步 `issues.status`

**现状**：`CreateIssue` 写入 `issues.status='todo'`，后续 `StartTask / CompleteTask / FailTask / BlockTask` 只更新 `task_queue`。

**方案**：把 `issues.status` 视为业务视图的派生状态，与 `task_queue` 状态在同一事务中同步。所有修改点：

- `StartTask`：`task_queue → running`，同事务将 `issues.status → in_progress`。
- `CompleteTask`：`task_queue → completed`，同事务将 `issues.status → done`。
- `FailTask`：根据 SQL `CASE WHEN` 的最终状态同步：
  - 回退重试（`task_queue → queued`）→ `issues.status = in_progress`（任务仍在处理流程中）
  - 重试耗尽（`task_queue → blocked`）→ `issues.status = blocked`
- `BlockTask`：`task_queue → blocked`，同事务将 `issues.status → blocked`。
- `CascadeBlock`：级联标记 `blocked` 时，同事务将对应的 `issues.status → blocked`。

**状态映射**：

| task_queue 状态 | issues.status |
|---|---|
| queued（首次入队） | todo |
| running | in_progress |
| queued（失败回退重试） | in_progress |
| completed | done |
| blocked | blocked |

**代码示意**（`internal/store/store.go`）：

`CompleteTask`：

```go
func (s *Store) CompleteTask(ctx context.Context, task *QueuedTask) error {
    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil {
        return fmt.Errorf("开启事务失败: %w", err)
    }
    defer tx.Rollback()

    res, err := tx.ExecContext(ctx,
        `UPDATE task_queue
         SET status = 'completed', finished_at = ?
         WHERE id = ? AND claim_token = ? AND status IN ('dispatched', 'running')`,
        time.Now(), task.ID, task.ClaimToken,
    )
    if err != nil {
        return fmt.Errorf("标记完成失败: %w", err)
    }
    affected, err := res.RowsAffected()
    if err != nil {
        return fmt.Errorf("读取完成结果失败: %w", err)
    }
    if affected == 0 {
        return fmt.Errorf("完成失败: 凭证不匹配或任务已被处理 (id=%s)", task.ID)
    }

    if _, err := tx.ExecContext(ctx,
        `UPDATE issues SET status = 'done', updated_at = ? WHERE id = ?`,
        time.Now(), task.IssueID,
    ); err != nil {
        return fmt.Errorf("同步任务状态失败: %w", err)
    }

    return tx.Commit()
}
```

`FailTask`（重试回退与 blocked 都要同步）：

```go
func (s *Store) FailTask(ctx context.Context, task *QueuedTask, errMsg string) (string, error) {
    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil {
        return "", fmt.Errorf("开启事务失败: %w", err)
    }
    defer tx.Rollback()

    res, err := tx.ExecContext(ctx,
        `UPDATE task_queue
         SET status = CASE WHEN attempts + 1 < ? THEN 'queued' ELSE 'blocked' END,
             attempts = attempts + 1,
             claim_token = NULL,
             worker_id = NULL,
             error = ?,
             finished_at = ?
         WHERE id = ? AND claim_token = ? AND status IN ('dispatched', 'running')`,
        MaxAttempts, errMsg, time.Now(), task.ID, task.ClaimToken,
    )
    if err != nil {
        return "", fmt.Errorf("标记失败失败: %w", err)
    }
    affected, err := res.RowsAffected()
    if err != nil {
        return "", fmt.Errorf("读取失败结果失败: %w", err)
    }
    if affected == 0 {
        return "", fmt.Errorf("标记失败失败: 凭证不匹配或任务已被处理 (id=%s)", task.ID)
    }

    // 与 SQL CASE 同一句判断：决定 task_queue 最终状态，并同步 issues.status
    finalStatus := "queued"
    issueStatus := "in_progress"
    if task.Attempts+1 >= MaxAttempts {
        finalStatus = "blocked"
        issueStatus = "blocked"
    }

    if _, err := tx.ExecContext(ctx,
        `UPDATE issues SET status = ?, updated_at = ? WHERE id = ?`,
        issueStatus, time.Now(), task.IssueID,
    ); err != nil {
        return "", fmt.Errorf("同步任务状态失败: %w", err)
    }

    if err := tx.Commit(); err != nil {
        return "", fmt.Errorf("提交事务失败: %w", err)
    }
    return finalStatus, nil
}
```

`StartTask` / `BlockTask` 同法：分别将 `issues.status` 更新为 `in_progress` / `blocked`。

**验证**：`go test ./...`；新增测试断言 `ListIssues` 返回的状态随任务 start / complete / fail 回退 / block 正确变化。

---

### P0-2：`CreateIssue` 事务化

**现状**：`CreateIssue` 先 `INSERT issues`，再调用 `EnqueueTask`，两步非原子。

**方案**：将 `INSERT issues` 与 `INSERT task_queue` 合并到一个事务中。保留 `EnqueueTask` 作为兼容包装，内部调用事务版本。

**代码示意**：

```go
func (s *Store) CreateIssue(ctx context.Context, title, description, assigneeID, dependsOn string) (*Issue, error) {
    now := time.Now()
    issue := &Issue{
        ID:           NewID(),
        Title:        title,
        Description:  description,
        Status:       "todo",
        AssigneeType: "agent",
        AssigneeID:   assigneeID,
        DependsOn:    dependsOn,
        CreatedAt:    now,
        UpdatedAt:    now,
    }

    var dependsOnArg any
    if dependsOn != "" {
        dependsOnArg = dependsOn
    }

    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil {
        return nil, fmt.Errorf("开启事务失败: %w", err)
    }
    defer tx.Rollback()

    if _, err := tx.ExecContext(ctx,
        `INSERT INTO issues (id, title, description, status, assignee_type, assignee_id, depends_on, created_at, updated_at)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
        issue.ID, issue.Title, issue.Description, issue.Status,
        issue.AssigneeType, issue.AssigneeID, dependsOnArg, issue.CreatedAt, issue.UpdatedAt,
    ); err != nil {
        return nil, fmt.Errorf("创建任务失败: %w", err)
    }

    if _, err := tx.ExecContext(ctx,
        `INSERT INTO task_queue (id, issue_id, status, dedup_sha)
         VALUES (?, ?, 'queued', ?)`,
        NewID(), issue.ID, issue.ID,
    ); err != nil {
        return nil, fmt.Errorf("任务入队失败: %w", err)
    }

    if err := tx.Commit(); err != nil {
        return nil, fmt.Errorf("提交事务失败: %w", err)
    }
    return issue, nil
}
```

**验证**：临时让 `task_queue` 插入失败，确认 `issues` 无残留。

---

### P0-3：消息闭环事务化

**现状**：`handleCreateMessage` 中 `SendMessage` 与 `AttachTasks` 分两步。

**方案**：新增 `SendMessageWithTasks`，在一个事务中完成：

1. `INSERT messages`
2. `INSERT OR IGNORE INTO message_tasks`

**代码示意**（`internal/store/store.go`）：

```go
func (s *Store) SendMessageWithTasks(
    ctx context.Context,
    conversationID, senderType, senderID, content string,
    taskIDs []string,
) (*Message, error) {
    m := &Message{
        ID:             NewID(),
        ConversationID: conversationID,
        SenderType:     senderType,
        SenderID:       senderID,
        Content:        content,
        CreatedAt:      time.Now(),
    }
    if len(taskIDs) > 0 {
        m.TaskID = taskIDs[0]
    }

    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil {
        return nil, fmt.Errorf("开启事务失败: %w", err)
    }
    defer tx.Rollback()

    if _, err := tx.ExecContext(ctx,
        `INSERT INTO messages (id, conversation_id, sender_type, sender_id, content, task_id, created_at)
         VALUES (?, ?, ?, ?, ?, ?, ?)`,
        m.ID, m.ConversationID, m.SenderType, m.SenderID, m.Content, m.TaskID, m.CreatedAt,
    ); err != nil {
        return nil, fmt.Errorf("发送消息失败: %w", err)
    }

    for _, taskID := range taskIDs {
        if _, err := tx.ExecContext(ctx,
            `INSERT OR IGNORE INTO message_tasks (message_id, task_id) VALUES (?, ?)`,
            m.ID, taskID,
        ); err != nil {
            return nil, fmt.Errorf("关联任务失败: %w", err)
        }
    }

    if err := tx.Commit(); err != nil {
        return nil, fmt.Errorf("提交事务失败: %w", err)
    }
    return m, nil
}
```

**上层调用**（`cmd/server/main.go`）：

```go
msg, err := s.SendMessageWithTasks(ctx, convID, "user", "me", req.Content, taskIDs)
```

**验证**：模拟 `message_tasks` 插入失败，确认 `messages` 也回滚。

---

### P0-4：`StartTask` 失败回退

**现状**：

```go
if err := s.StartTask(ctx, task); err != nil {
    log.Printf("⚠️  开工失败: %v", err)
    return
}
```

**方案**：`StartTask` 失败时，走 `reportFailure` 让任务回退 `queued` 或上报 `blocked`：

```go
if err := s.StartTask(ctx, task); err != nil {
    log.Printf("⚠️  开工失败: %v", err)
    reportFailure(s, task, err.Error())
    return
}
```

**验证**：手动将任务置为 `dispatched`，启动 agent 后确认任务回到 `queued` 或被 `blocked`。

---

### P0-5：孤儿任务回收

**现状**：agent 进程被杀后，`dispatched/running` 任务无人处理。其中有两类孤儿：

1. `running`：`StartTask` 已执行，`started_at` 有值，但进程在汇报前被杀。
2. `dispatched` 且 `started_at IS NULL`：进程在认领后、`StartTask` 前被杀。这类任务没有 `started_at`，不能靠旧方案回收。

**方案**：

1. `schema.sql` 的 `task_queue` 增加 `dispatched_at TIMESTAMP`，记录认领时间。
   - 新库：直接更新 `schema.sql` 即可。
   - 旧库迁移：`Open` 中已加入 `ensureColumn` 自动迁移（`PRAGMA table_info` 检查 + `ALTER TABLE` 补列），打开旧库时自动完成。
2. `ClaimTask` 的乐观锁 `UPDATE` 同时写入 `dispatched_at`。
3. 新增 `RequeueStaleTasks(ctx, olderThan)`，分别处理两类孤儿：

| 孤儿类型 | 回收条件 |
|---|---|
| `dispatched` 未开工 | `dispatched_at IS NOT NULL AND dispatched_at < cutoff` |
| `running` 已开工 | `started_at IS NOT NULL AND started_at < cutoff` |

**兼容旧数据**：历史遗留的 `dispatched` 且 `dispatched_at IS NULL AND started_at IS NULL` 记录，建议一次性手动 SQL 回收（见验证部分）。

**代码示意**：

`ClaimTask` 更新语句改为：

```go
res, err := s.db.ExecContext(ctx,
    `UPDATE task_queue
     SET status = 'dispatched', claim_token = ?, worker_id = ?, dispatched_at = ?
     WHERE id = ? AND status = 'queued'`,
    claimToken, workerID, time.Now(), id,
)
```

`RequeueStaleTasks`：

```go
func (s *Store) RequeueStaleTasks(ctx context.Context, olderThan time.Duration) (int64, error) {
    cutoff := time.Now().Add(-olderThan)

    // 类型 1：已开工但超时未汇报
    res1, err := s.db.ExecContext(ctx,
        `UPDATE task_queue
         SET status = 'queued',
             claim_token = NULL,
             worker_id = NULL,
             dispatched_at = NULL,
             started_at = NULL,
             finished_at = NULL,
             error = 'worker 超时未汇报，已回退重试'
         WHERE status = 'running'
           AND started_at IS NOT NULL
           AND started_at < ?`,
        cutoff,
    )
    if err != nil {
        return 0, fmt.Errorf("回收 running 孤儿任务失败: %w", err)
    }

    // 类型 2：已认领但一直没开工
    res2, err := s.db.ExecContext(ctx,
        `UPDATE task_queue
         SET status = 'queued',
             claim_token = NULL,
             worker_id = NULL,
             dispatched_at = NULL,
             error = 'worker 认领后未开工，已回退重试'
         WHERE status = 'dispatched'
           AND started_at IS NULL
           AND dispatched_at IS NOT NULL
           AND dispatched_at < ?`,
        cutoff,
    )
    if err != nil {
        return 0, fmt.Errorf("回收 dispatched 孤儿任务失败: %w", err)
    }

    n1, _ := res1.RowsAffected()
    n2, _ := res2.RowsAffected()
    return n1 + n2, nil
}
```

`cmd/agent/main.go` 启动后、每轮 `runOnce` 前调用：

```go
if n, err := s.RequeueStaleTasks(ctx, 10*time.Minute); err != nil {
    log.Printf("⚠️  孤儿任务回收失败: %v", err)
} else if n > 0 {
    log.Printf("🧹 回收孤儿任务 %d 条", n)
}
```

**验证**：

1. 插入一条 `started_at` 超过 10 分钟的 `running` 任务，确认被回退为 `queued`。
2. 插入一条 `dispatched_at` 超过 10 分钟、`started_at IS NULL` 的 `dispatched` 任务，确认被回退为 `queued`。
3. 历史遗留数据一次性清理（执行前先人工确认）：

```sql
UPDATE task_queue
SET status = 'queued',
    claim_token = NULL,
    worker_id = NULL,
    dispatched_at = NULL,
    error = '历史孤儿任务，人工回退重试'
WHERE status = 'dispatched'
  AND started_at IS NULL
  AND dispatched_at IS NULL;
```

---

### P0-6：监听地址与鉴权

**现状**：`http.ListenAndServe(":8080", mux)` 监听所有网卡。

**方案**：增加 `-addr` 参数，默认 `127.0.0.1:8080`：

```go
addr := flag.String("addr", "127.0.0.1:8080", "监听地址")
flag.Parse()

log.Printf("服务已启动: http://%s", *addr)
if err := http.ListenAndServe(*addr, mux); err != nil {
    log.Fatalf("服务启动失败: %v", err)
}
```

**后续可选**：增加 `-token` 参数，非空时要求请求头 `Authorization: Bearer <token>`。

**验证**：启动后默认只允许本机访问。

---

### P1-1：整理依赖

```bash
go mod tidy
```

**验证**：`go build ./...` 通过；`go.mod` 中直接依赖不再标 `indirect`。

---

### P1-2：文档同步

更新以下内容：

- `README.md`：`cmd/worker` → `cmd/agent`，启动命令改为 `go run ./cmd/agent -name 小王`。
- `CODE_MAP.md`：更新源码行数、目录树、数据层方法数量（当前 919 行 store.go 已远超旧记录）。
- `PROGRESS.md`：文件清单已基本更新，补齐 `internal/agent/engine.go`、`pi.go`、`deepseek.go`、`fake.go`。

**验证**：新读者按 README 从零跑通。

---

### P1-3：确认运行产物已被忽略并清理工作区

**现状更正**：项目根目录已有 `.gitignore`，已覆盖 `*.db`、`bin/`、`*.exe`、`*.log` 等运行产物。问题不是“缺少 `.gitignore`”，而是工作区中确实存在 `bin/*.exe`、`mini-agents.db`、`*.log` 等产物，需确认它们未被 Git 跟踪。

**方案**：

1. 确认忽略规则仍然有效：

```bash
git status --short
git status --short --ignored | head -50
```

2. 若历史上有运行产物被误提交过，从索引中移除（不删除本地文件）：

```bash
git rm --cached -r bin 2>/dev/null
git rm --cached *.db *.db-shm *.db-wal *.log 2>/dev/null
```

3. 保持 `.gitignore` 不变，后续运行产物不会再出现在 `git status` 中。

**验证**：`git status` 不出现 `bin/`、`*.db`、`*.log` 的未跟踪或已跟踪变更。

---

### P1-4：补测试

**已实施**：

1. `cmd/server/main_test.go`：使用 `httptest` 覆盖创建任务、员工入职、消息触发任务三个 HTTP handler。
2. `internal/agent/engine_test.go`：覆盖 `FakeEngine.Execute` 与 `NewEngine` 工厂分支。
3. `internal/store/store_test.go`：补充 `SendMessageWithTasks` 事务关联测试。

**仍可继续增强**：

- 将 handler 从 `cmd/server` 迁到 `internal/api`，进一步降低耦合。
- `ClaudeEngine` 用假脚本路径验证输出、退出码、超时。
- `PiEngine` 用假 RPC 进程输出 JSONL，验证 `text_delta` 拼接。
- `cmd/agent` 的 prompt 拼装与 `reportFailure` 分支抽成函数测试。

**验证**：`go test ./...` 覆盖 handler、engine、store。

---

### P2-1：空闲日志降噪

**现状**：每 2 秒打印一次“没有派给我的活”。

**方案**：只在“从有活到没活”或“每 N 次循环”打印一次：

```go
idle := false
for {
    task := runOnce(...)
    if task == nil {
        if !idle {
            log.Println("🕊️  没有派给我的活，歇会儿")
            idle = true
        }
    } else {
        idle = false
    }
    time.Sleep(2 * time.Second)
}
```

**验证**：空跑 10 分钟，日志中该提示不超过 1 条。

---

### P2-2：补业务索引

在 `internal/store/schema.sql` 末尾追加：

```sql
CREATE INDEX IF NOT EXISTS idx_task_queue_status ON task_queue(status);
CREATE INDEX IF NOT EXISTS idx_task_queue_issue ON task_queue(issue_id);
CREATE INDEX IF NOT EXISTS idx_issues_assignee ON issues(assignee_id);
CREATE INDEX IF NOT EXISTS idx_issues_depends_on ON issues(depends_on);
CREATE INDEX IF NOT EXISTS idx_messages_conv_created ON messages(conversation_id, created_at);
CREATE INDEX IF NOT EXISTS idx_message_tasks_task ON message_tasks(task_id);
```

**验证**：数据量 1w+ 时，`ClaimTask` 与 `ListMessages` 响应明显改善。

---

### P2-3：`我无法完成` 检测加 TrimSpace

**现状**：

```go
if strings.HasPrefix(result.Output, "我无法完成") {
```

**方案**：

```go
if strings.HasPrefix(strings.TrimSpace(result.Output), "我无法完成") {
```

**验证**：fake 引擎输出 `"\n我无法完成：缺权限"`，确认任务被 blocked。

---

### P2-4：@ 解析优化

**现状**：`strings.Contains(content, "@"+name)` 子串匹配，会误触发“@小王总”给“小王”。

**方案**：按员工名字长度从长到短排序，先匹配长名字；匹配后检查 `@name` 后面的字符是否为空白、标点或结尾。

```go
sort.Slice(all, func(i, j int) bool { return len(all[i].Name) > len(all[j].Name) })

for i := range all {
    name := all[i].Name
    for _, chunk := range strings.Split(req.Content, "@")[1:] {
        if chunk == name || strings.HasPrefix(chunk, name) && len(chunk) > len(name) &&
            (chunk[len(name)] == ' ' || chunk[len(name)] == '，' || chunk[len(name)] == ',' ||
             chunk[len(name)] == '\n' || chunk[len(name)] == '。') {
            mentioned = append(mentioned, all[i])
            break
        }
    }
}
```

**验证**：创建“小王”和“小王总”，发“@小王总 干活”，只有“小王总”被触发。

---

### P2-5：前端增量拉取

**现状**：每 2 秒全量 `GET /messages` 后 `innerHTML=''` 重建。

**方案（已实施）**：

1. 后端新增 `ListMessagesAfter(ctx, conversationID, afterID)`，`GET /api/conversations/{id}/messages?after=<消息ID>` 只返回游标之后的新消息。
2. 游标使用消息 ULID `id > ?` 判断，避免 SQLite 中 `created_at` 因 Go 单调时钟后缀导致游标消息重复返回。
3. 前端记录 `lastMsg`，轮询时带 `?after=<lastMsg.ID>`，将新消息 `append` 到消息流而不是清空重绘；切换会话时重置游标并全量加载。
4. 若后端因游标失效退回全量，前端通过“首条消息时间 <= 游标时间”识别并改为全量重建，避免重复消息。

**验证**：造 100 条消息，滚动和刷新无明显抖动。

---

## 5. 实施计划

| 批次 | 内容 | 预计改动 | 风险 |
|---|---|---|---|
| 第一批 P0 | P0-1 ~ P0-6 | store.go、server/main.go、agent/main.go | 中（事务重构） |
| 第二批 P1 | P1-1 ~ P1-4 | go.mod、README、.gitignore、测试 | 低 |
| 第三批 P2 | P2-1 ~ P2-5 | agent/main.go、schema.sql、web/index.html、server | 低 |

建议每个 P0 项单独提交，便于回滚和 review。

---

## 6. 验收标准

### 6.1 自动化

```bash
go mod tidy
go build ./...
go test ./...
go vet ./...
```

全部通过。

### 6.2 手动端到端

1. 启动 server（默认 127.0.0.1:8080）。
2. 入职“小王”（fake）与“小李”（fake）。
3. 建群 → 发消息 `@小王 写个周报`。
4. 启动 `cmd/agent -name 小王` 与 `cmd/agent -name 小李`。
5. 观察：
   - 小王的任务 `queued → dispatched → running → completed`
   - `GET /api/issues` 中该任务状态为 `done`
   - 会话中收到小王的回复消息
6. 构造失败：
   - fake 引擎输出“我无法完成：缺权限” → 任务 `blocked`
   - 杀掉 agent 进程后重启 → 孤儿任务被回收

### 6.3 数据一致性验收

- 任意时刻，`task_queue.status` 与 `issues.status` 的映射符合 P0-1 表格。
- 任务不存在于 `issues` 但存在于 `task_queue` 的情况为 0。
- 消息触发多任务时，`message_tasks` 行数与任务数一致。

---

## 7. 风险与应对

| 风险 | 影响 | 应对 |
|---|---|---|
| 事务重构改动面大 | 可能影响现有流程 | 保留旧方法签名作为兼容包装，逐项提交 |
| `issues.status` 语义变化 | 旧测试可能失败 | 同步更新测试断言 |
| 索引占用写入性能 | 写入轻微变慢 | 索引只加在高频查询字段，数据量小可接受 |
| 孤儿回收误伤长任务 | 超过 10 分钟的正常 claude 任务被回退 | 阈值可配置；超时阈值大于执行超时（当前 2 分钟） |
| 前端增量拉取引入顺序 bug | 消息重复/丢失 | 用 `created_at` + 消息 ID 去重，保留全量刷新兜底 |

---

## 8. 后续演进（不在本次范围）

- M7 双层知识库：`memory` 表 + `scope` 隔离 + 执行时注入。
- M7 记忆注入预算控制（当前是"作用域过滤后的全量注入"）：后续加**条数上限 + 最大字符截断**（如 L1 ≤ 5 条、`maxTotalRecallChars`），并按任务内容做**相关性召回**（关键词 FTS / 向量检索，只注入相关条目）——参考 TencentDB-Agent-Memory 的 Recall Budget 与混合检索。
- M7 注入分块优化：区分**动态上下文**（每轮变化的记忆，前插到用户消息前）与**稳定上下文**（画像/规范等长期内容，追加到 system 末尾），利用 Prompt Caching 缓存稳定块省 token——参考 TencentDB 的 RecallResult 分块设计。
- 轮询升级 SSE/WebSocket：参考 Multica 推送模型。
- `agents.engine` 抽象为独立 `agent_runtime` 表，支持多 agent 共享运行时与云端执行。
- 消息增量拉取完成后，可增加已读游标与未读计数。
