# mini-agents 🤖

> 一个用 Go + SQLite 实现的单机版 Agent 编排工具：任务进队列，worker 自动认领并调用 Claude CLI 执行，全程记账。

[![Go Version](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go)](https://go.dev/)
[![SQLite](https://img.shields.io/badge/SQLite-modernc.org-003B57?logo=sqlite)](https://gitlab.com/cznic/sqlite)
[![Status](https://img.shields.io/badge/status-M3%20complete-brightgreen)]()
[![License](https://img.shields.io/badge/License-MIT-blue)](./LICENSE)

---

## ✨ 特性

- **任务管理**：HTTP API 创建 / 查询任务
- **任务队列 + 状态机**：`queued → dispatched → running → completed / failed / blocked`
- **业务状态同步**：`issues.status` 随流水线实时更新（`todo / in_progress / done / blocked`）
- **乐观锁认领**：一条 `UPDATE ... WHERE status='queued'` 原子完成"抢单 + 标记"，多 worker 并发也不重复执行
- **幂等汇报**：`claim_token` 凭证 + 状态校验，重复调用不会搞坏状态
- **多 Agent**：`cmd/agent -name 小王` 启动员工进程，只认领指派给自己的任务
- **真实 Agent 执行**：worker 调用 `claude -p` 完成任务，带超时保护；支持多引擎（claude / pi / fake）
- **可靠性**：失败自动重试，耗尽 `blocked` 上报；孤儿任务自动回收
- **全程记账**：每次执行的结果 / 耗时 / 退出码写入 `agent_runs`，可追溯
- **零 CGO**：纯 Go 驱动（`modernc.org/sqlite`），跨平台，单机一键运行

## 🏗️ 架构

三层单向依赖，职责分离——HTTP 层不知道 SQL，数据层不知道 HTTP：

```
┌────────────────────────────────────────────────┐
│ HTTP 层  cmd/server/main.go                    │
│   接待请求 · 翻译 JSON · 给状态码              │
└───────────────────────┬────────────────────────┘
                        │ 调用
┌───────────────────────▼────────────────────────┐
│ 数据层  internal/store/                        │
│   所有 SQL 只在这一层 · 含事务 / 状态同步      │
└───────────────────────┬────────────────────────┘
                        │ 读写
┌───────────────────────▼────────────────────────┐
│ 数据库  SQLite（issues / task_queue / agent_runs │
│         agents / conversations / messages ...） │
└────────────────────────────────────────────────┘

  Agent 执行器 internal/agent/   ← 与数据层平级，由 worker 依赖注入
    agent 循环：认领 → 开工 → 读任务 → 调引擎 → 记账 → 汇报
     每轮先回收孤儿任务（dispatched/running 超时回退）
```

## 📁 目录结构

```
mini-agents/
├── cmd/
│   ├── server/        HTTP 服务（任务 API，默认监听 127.0.0.1:8080）
│   ├── agent/         agent 进程（-name 员工名，自动干活）
│   └── dump/          调试工具（查看队列 / 执行日志）
├── internal/
│   ├── store/         数据层（schema.sql + store.go + 测试）
│   └── agent/         claude 执行器（os/exec 封装）
├── go.mod             模块定义
├── PROGRESS.md        学习进度（课程表 + 教学点）
└── CODE_MAP.md        代码地图（架构详解）
```

## 🚀 快速开始

### 前置要求

| 组件 | 说明 |
|---|---|
| **Go 1.26+** | [go.dev](https://go.dev/dl/) |
| **Claude CLI / Pi**（可选） | agent 真实执行需要；装 [Claude Code](https://docs.anthropic.com/en/docs/claude-code/setup)。不装可把引擎设为 `fake` 跑通流程 |

### 安装与运行

```bash
# 1. 拉取代码
git clone <你的仓库地址>
cd mini-agents

# 2. 编译（首次会自动下载依赖）
go build ./...

# 3. 终端 A：启动 HTTP 服务（默认只监听本机）
go run ./cmd/server
# → 服务已启动: http://127.0.0.1:8080

# 4. 终端 B：让员工小王入职并启动它的 agent 进程（自动认领任务并调用引擎）
curl -X POST http://127.0.0.1:8080/api/agents \
  -H "Content-Type: application/json" \
  -d '{"name":"小王","role":"前端工程师","engine":"fake"}'

go run ./cmd/agent -name 小王
```

## 🧪 使用示例

```bash
# 创建一条任务（assignee 填员工名字，不是 id）
curl -X POST http://127.0.0.1:8080/api/issues \
  -H "Content-Type: application/json" \
  -d '{"title":"用一句话介绍 Go 语言","description":"交给小王","assignee":"小王"}'

# 查看所有任务
curl http://127.0.0.1:8080/api/issues

# 查看队列和执行日志（调试工具）
go run ./cmd/dump
```

agent 运行日志示例：

```
✅ 认领到任务: 队列=01KZP50W... 任务=01KZP50W...
🔨 调引擎执行: 用一句话介绍 Go 语言
🏁 任务完成: 用一句话介绍 Go 语言 (执行记录 01KZP514...)
   引擎回答: Go 语言（又称 Golang）是谷歌于 2009 年开源的一种静态类型、编译型编程语言……
```

## 📊 数据模型

| 表 | 角色 | 关键字段 |
|---|---|---|
| `issues` | 待办清单（给人看） | id, title, description, status, assignee_type/id |
| `task_queue` | 执行流水线（给机器看） | id, issue_id, status, attempts, claim_token, worker_id |
| `agent_runs` | 执行日志（事后查） | id, task_id, exit_code, output, duration_ms |

三张表通过 `issue_id` / `task_id` 关联（参照 Multica 的教训，生产环境刻意不用数据库外键，关系在应用层处理）。

## ✅ 测试

```bash
go test ./...
go vet ./...
```

## 🎓 学习里程碑

这个项目是**逐行教学**的产物，核心闭环分三个里程碑完成（进度与教学点见 [PROGRESS.md](./PROGRESS.md)）：

| 里程碑 | 内容 | 状态 |
|---|---|---|
| **M1** | 任务 CRUD（HTTP + SQLite 持久化） | ✅ |
| **M2** | 队列 + worker + 乐观锁认领 + 幂等汇报 | ✅ |
| **M3** | worker 真调 `claude -p`，结果记账 | ✅ |
| 后续 | web 前端 / 多 worker 并发 / 失败重试 | ⬜ 计划中 |

## 📜 许可证

[MIT](./LICENSE)
