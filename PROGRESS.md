# mini-agents 学习进度

> 逐行学习构建单机版 Agent 编排工具，用于理解 Multica 架构。
> 教学节奏：一次一小步 · 逐行解释 · 中文 · 确认后再继续

## 环境（第 1 课完成 ✅）

| 组件 | 状态 | 说明 |
|---|---|---|
| Go | ✅ 1.26.5 | 完整路径 `C:\Program Files\Go\bin\go.exe`（winget 装的） |
| Claude CLI | ✅ 2.1.169 | 用于第 8 课（M3）真实执行 |
| Git | ✅ | 2.54.0 |
| Go 镜像 | ✅ 已设 | `GOPROXY=https://goproxy.cn,direct`（官方源太慢，改用国内镜像） |

> ⚠️ Go 是新装的，**新开终端**才会出现在 PATH 里。当前会话要用完整路径 `"/c/Program Files/Go/bin/go.exe"`。

## 课程进度

| 课 | 主题 | 状态 | 日期 | 备注 |
|---|---|---|---|---|
| 第 1 课 | 环境准备（装 Go） | ✅ 完成 | 2026-08-05 | |
| 第 2 课 | 项目骨架 + go mod init | ✅ 完成 | 2026-08-05 | 目录结构 + go.mod 讲解 |
| 第 3 课 | schema.sql 三张表 | ✅ 完成 | 2026-08-05 | 逐行讲了 SQL 语法；**外键教学点** |
| 第 4 课 | store.go 数据层 | ✅ 完成 | 2026-08-05 | Open+建表+NewID+CreateIssue+测试全通过 |
| 第 5 课 | main.go + issue CRUD（M1） | ✅ 完成 | 2026-08-06 | ListIssues + HTTP 层（路由/handler/JSON）编译通过 |
| 第 6 课 | 运行并 curl 验证 M1 | ✅ 完成 | 2026-08-06 | **M1 达成**：服务跑通，POST/GET 验证，数据持久化，4 条任务入库 |
| 第 7 课 | task_queue + worker（M2） | ✅ 完成 | 2026-08-10 | **M2 达成**：入队+认领乐观锁+幂等完成/失败；worker 自动处理 3 条任务，状态流转验证 |
| 第 8 课 | 接入 claude -p（M3） | ✅ 完成 | 2026-08-10 | **M3 达成**：worker 真调 claude.exe，回答存 agent_runs；修复中文乱码编码 bug |

## 后续计划（可选，未排期）

| 项 | 内容 | 状态 |
|---|---|---|
| **第 9 课** | **web/index.html 前端页面**（任务列表 + 创建表单 + 状态展示） | ⬜ 计划中 |
| 第 10 课 | 多 worker 并发验证（乐观锁实战检验） | ⬜ 计划中 |
| 第 11 课 | 重试机制（`attempts` 字段 + 失败自动重试） | ⬜ 计划中 |

## 关键教学点（边学边记）

- **外键**：mini 版用了外键方便理解关系；**Multica 刻意不用外键**（多态 + Agent 异步写入难维护），这个反差是理解点
- **三张表分工**：issues=待办清单（人类看）、task_queue=流水线（机器看）、agent_runs=记账本（事后查）
- **状态机**：`queued → dispatched → running → completed/failed` ✅ 已实现
- **认领乐观锁**：`UPDATE ... WHERE status='queued'` + 检查 RowsAffected ✅ 已实现（两条 SQL 合一，防并发重复认领）
- **幂等完成**：`WHERE claim_token=? AND status IN ('dispatched','running')`——重复调用不会搞坏状态 ✅
- **NULL ≠ 空字符串**：数据库可空列 Scan 必须用 `sql.NullString`/`sql.NullTime`（`converting NULL to string` 真实 bug 教训）
- **Windows 编码坑**：npm 的 claude shim 走 cmd 会把中文参数按 GBK 破坏；解法是直连 `claude.exe`（Go 用 UTF-16 传参给 CreateProcess，中文无损）
- **失败也记账**：claude 执行失败也要写 agent_runs（留痕），再 FailTask
- **超时保护**：`context.WithTimeout` + `exec.CommandContext` 防止 claude 卡死 worker
- **幂等建表**：`CREATE TABLE IF NOT EXISTS`（第 4 课真实 bug 教训）
- **资源释放**：连接用完 Close()；获取后中途出错也要关（Open 建表失败关 db）
- **测试用 t.TempDir()**：比手动删文件可靠（Windows 句柄占用坑）
- **中文乱码是发送端编码问题**：服务器按 UTF-8 解析正常；终端 curl 发中文要用 UTF-8 文件（`--data-binary @file`）

## 文件清单（当前）

```
mini-agents/
  go.mod                      ✅ go 1.26.5
  internal/store/schema.sql   ✅ 三张表（已讲解）
  internal/store/store.go     ✅ Open+建表+NewID+Issue+CreateIssue
  internal/store/store_test.go ✅ CreateIssue 测试通过（第一条真实数据入库）
  cmd/server/main.go          ✅ HTTP 服务（POST/GET /api/issues，编译通过）
  cmd/worker/main.go          ✅ worker 主程序（认领→读任务→真调 claude→记账→汇报，已验证）
  cmd/dump/main.go            ✅ 查看任务+队列+执行日志的小工具（调试用）
  internal/agent/runner.go    ✅ claude 执行器（Windows 直连 claude.exe 避编码坑）
  web/index.html              ⬜ 待写（可选）
```
