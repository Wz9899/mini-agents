# mini-agents 项目指南

## 项目定位

学习项目：用 Go 从零**逐行**构建一个**多 Agent 团队协调工具**（V2）。单机、SQLite、`claude -p` 执行。目标是复刻并理解三类真实系统的设计：**Multica**（Agent 平台架构）、**TencentDB-Agent-Memory**（记忆作用域隔离）、**LoopX**（人类闸门/状态内核）。

## 教学约定（务必遵守）

用户是**编程新手**（正在学 Go），采用**逐行教学**节奏：

- 一次一小步，**逐行解释每行代码**，用**中文**
- 每步等用户确认后再继续下一步
- 开始工作前**先读 PROGRESS.md** 接上进度；改动代码前先读相关文件
- 保持"已实现"与"计划中"的区分（见 PROGRESS.md 文件清单）

## V2 设计核心（2026-08-12 定稿）

- **多 Agent = 每 Agent 一个进程**：`cmd/agent -name 小王`，Agent 有名字/角色（`agents` 表）
- **任务指派给具体 Agent**（`issues.assignee_id`）；Agent 只认领指派给自己的任务（乐观锁不变）
- **状态机扩展**：`failed` 自动重试（`attempts`）→ `blocked` 上报人类（重试耗尽 / claude"我无法完成"），带 `reason` + 终端告警
- **记忆分层**（M7）：`memory` 表 `scope` 字段（`team` 共享 / `agent_<id>` 私有）
- **前端团队看板**（M6）：Agent 卡片 + 进度条 + `blocked` 标红 + 2 秒轮询（轮询零 token 消耗，token 只花在 claude 调用）

完整蓝图 + 参考映射表：`docs/v2-design.md`

## 课程计划（M4–M7）

| 课 | 里程碑 | 内容 |
|---|---|---|
| 第 9 课 | M4 多 Agent 核心 | `agents` 表 + `issues.assignee_id` + `cmd/agent -name` + 多进程并行验证 |
| 第 10 课 | M5 可靠性 | `attempts` 自动重试（failed→queued）+ `blocked` 上报 + 终端告警 |
| 第 11 课 | M6 前端团队看板 | `GET /api/team` + Agent 卡片 + 指派下拉 + 进度条 + 2 秒轮询 |
| 第 12 课 | M7 记忆分层 | `memory` 表 scope + `internal/memory/` Capture/Recall + 执行时注入 |

## 技术要点（避免踩坑）

- **Windows 中文编码**：执行 claude 必须**直连 claude.exe**（勿走 npm shim / cmd，GBK 会破坏中文参数）；Go 用 UTF-16 传参
- **认领乐观锁**：`UPDATE ... WHERE status='queued'` + `RowsAffected()` 检查，防并发重复认领
- **幂等完成**：`WHERE claim_token=? AND status IN ('dispatched','running')`
- **SQLite 可空列**：必须用 `sql.NullString` / `sql.NullTime`（NULL ≠ 空字符串）
- **多进程并发写 SQLite**：需要 WAL 模式 + busy_timeout（M4 落地时处理）
- **失败也记账**：claude 执行失败也要写 `agent_runs`，再走重试/上报逻辑
- **Go 命令用完整路径**：`"/c/Program Files/Go/bin/go.exe"`（新装 Go 不一定在当前 PATH）
