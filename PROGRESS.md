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
| 第 9 课 | M4 多 Agent 核心 | ✅ 完成 | 2026-08-15 | **M4 达成**：agents 表 + assignee 指派 + `cmd/agent -name` + 团队 API，3 进程并行验证 |
| M4.5 | 多引擎：Engine 接口 + pi | ✅ 完成 | 2026-08-15 | **M4.5 达成**：agents.engine + Engine 接口 + PiEngine/DeepSeek stub + NewEngine 工厂，pi 真跑验证 |
| M4.5-2 | 协作任务 depends_on | ✅ 完成 | 2026-08-15 | **M4.5-2 达成**：issues.depends_on + 认领 EXISTS 过滤 + 上游成果注入 prompt，双进程协作验证 |
| M4.5-3 | 会话与消息（飞书地基） | ✅ 完成 | 2026-08-15 | **M4.5-3 达成**：conversations/conversation_members/messages 三表 + 建会话/发消息/消息流 API（事务+组合主键） |
| M4.5-4 | 单聊闭环 | ✅ 完成 | 2026-08-15 | **M4.5-4 达成**：@解析→创建 issues + agent 完成→输出回消息，消息驱动闭环验证 |

## V2 重新设计（2026-08-12）

**新需求**：把 mini-agents 变成**多 Agent 团队协调工具**——每个 Agent 是一个团队员工（有名字/角色），任务**指派给具体 Agent**，多个 Agent 进程**并行干活**。

**V2 核心决策**（grilling 逐决策产出）：显式指派 · 每 Agent 一进程（`cmd/agent -name`）· agents 表 · 记忆分层（team 共享 + 个人私有）· 自动重试 · blocked 上报 · 团队看板 · 2 秒轮询。

> 方向演进（2026-08-15）：最终愿景定为**飞书式**（消息驱动协作）——发消息 @ 下属 → 干活 → 回消息。原"团队看板"前端改为"飞书式会话界面"；知识库定稿为**双层**（team 共享 + 成员私有，只装文档+代码，交流不沉淀，靠会话历史临时查）。见 docs/v2-design.md 第 12 节。

> 完整蓝图 + 参考映射（Multica / TencentDB-Agent-Memory / LoopX）见 `docs/v2-design.md`

## V2 课程计划（未排期）

| 课 | 里程碑 | 内容 | 状态 |
|---|---|---|---|
| 第 9 课 | **M4 多 Agent 核心** | agents 表 + issues.assignee_id + `cmd/agent -name` + 多进程并行验证 | ✅ 已达成 |
| M4.5 | **多引擎** | agents.engine + Engine 接口 + PiEngine/DeepSeek stub + NewEngine 工厂 | ✅ 已达成 |
| M4.5-2 | **协作任务** | issues.depends_on + 认领 EXISTS 过滤 + 上游成果注入 prompt | ✅ 已达成 |
| M4.5-3 | **会话与消息** | conversations/conversation_members/messages 三表 + 建会话/发消息/消息流 API | ✅ 已达成 |
| M4.5-4 | **单聊闭环** | @解析→issues + agent 完成→回消息 | ✅ 已达成 |
| 第 10 课 | **M5 可靠性** | `attempts` 自动重试（failed→queued）+ blocked 上报（重试耗尽 / claude"我无法完成"）+ 终端告警 | ⬜ 计划中 |
| 第 11 课 | **M6 飞书式前端** | 会话列表 + 消息流 + 输入框 + 2 秒轮询（原"团队看板"改造） | ⬜ 计划中 |
| 第 12 课 | **M7 双层知识库** | `memory` 表 scope（team/agent_id）+ 文档上传 + 代码归档 + 注入；**交流不沉淀**（会话历史临时查） | ⬜ 计划中 |
| 后 | **群聊 + 多 agent @** | 群成员各自领活（@提及 + 成员校验） | ⬜ 计划中 |

> 建议推进顺序（未确认）：M5 可靠性 → M6 飞书式前端 → M7 双层知识库

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
- **可重试失败 vs 不可恢复问题**（V2 M5）：技术失败回退重试；claude"我无法完成"直接 blocked 上报人——区分二者才是省钱关键（每次重试=一次 token 消耗）
- **显式指派认领**（V2 M4）：认领从"抢任意 queued"变"抢 `assignee_id=自己` 的"，乐观锁机制不变
- **SQLite 多进程写**（V2 M4）：多 Agent 进程共享一个库文件，需要 WAL 模式 + busy_timeout 防锁死
- **轮询 vs 推送**（V2 M6）：前端 2 秒轮询 GET /api/team，零 token 消耗（token 只花在 claude 调用）；WebSocket 推送是进阶版（Multica 用）
- **记忆作用域隔离**（V2 M7）：`scope=team` 共享读 / `scope=agent_<id>` 私有读，显式共享替代 Claude Code auto-memory 的隐式共享泄漏
- **幂等建表**：`CREATE TABLE IF NOT EXISTS`（第 4 课真实 bug 教训）
- **资源释放**：连接用完 Close()；获取后中途出错也要关（Open 建表失败关 db）
- **测试用 t.TempDir()**：比手动删文件可靠（Windows 句柄占用坑）
- **中文乱码是发送端编码问题**：服务器按 UTF-8 解析正常；终端 curl 发中文要用 UTF-8 文件（`--data-binary @file`）
- **Go 接口多态**（V2 M4.5）：`Engine` 接口 = 契约，四种实现互不关心；调用方只认接口。`NewEngine(名字)` 工厂按名字换实现——加新引擎不改调用方，这正是接口抽象的意义
- **管道 EOF 语义**（V2 M4.5）：关闭 stdin = 告诉子进程"输入结束"。pi 收到 EOF 会把会话当结束并退出、不调 LLM——必须等 `agent_settled` 事件再关
- **流式攒正文**（V2 M4.5）：pi RPC 事件流里 `text_delta` 是回答增量，逐块拼进 strings.Builder；`thinking_delta` 是思考过程不存；Scanner 默认 64KB 上限要调大
- **相关子查询 EXISTS**（V2 M4.5-2）：认领 SQL 内层 `WHERE d.issue_id = i.depends_on` 引用外层——"我依赖的那条任务完成了吗"；`SELECT 1` 只看存不存在；无依赖 `IS NULL` 短路省钱
- **三世界"无"不通**（V2 M4.5-2 真 bug）：Go 空串 `""` 写库是 `''`（有值），认领 SQL 的 `IS NULL` 判断失灵，无依赖任务被误过滤——空串必须显式转 SQL NULL（空接口 `any` 装 nil）；此 bug 还**静默**（认领不到和没任务都返回 nil）
- **进程零通信**（V2 M4.5-2）：A、B 是两个独立进程，成果走共享 SQLite（agent_runs），B 认领后 `GetLatestRun` 读上游输出拼进 prompt——三表 JOIN（issue→queue→runs）
- **组合主键**（V2 M4.5-3）：`PRIMARY KEY (conversation_id, agent_id)`——PRIMARY KEY 可以是多列，天然保证"一个会话一个 agent 最多一行"
- **事务原子性**（V2 M4.5-3）：`BeginTx/Commit/Rollback` + `defer tx.Rollback()` 惯用法——会话+成员两个写操作要么都成要么都回滚，防脏状态
- **消息驱动协作**（V2 M4.5-4）：发消息时 `strings.Contains(content, "@"+名字)` 解析 @（中文名不用正则）→ 创建 issues（assignee=被 @ 者）→ 消息带 task_id；agent 完成是主流程，回消息是**副作用**（`GetMessageByTask` 反查来源会话 → 输出写回），不改任务状态
- **交流 = 过程，知识库 = 沉淀**（V2 M7 定稿）：消息（交流）不沉淀进知识库，靠会话历史临时查；知识库只装**文档 + 代码**，不装闲聊——两者分工，避免冗余和上下文爆炸
- **双层知识库**（V2 M7 定稿）：`scope=team` 老板写、所有 agent 读（项目介绍/注意事项/规范）；`scope=agent_<id>` 该 agent + 老板写、只有本人读（文档/代码）。现仓库 CLAUDE.md 就是"team 知识库"的文件原型
- **目录隔离 vs scope**（V2 M7 讨论结论）：claude 按目录隔离记忆 = **物理隔绝**（解决"私有不串味"）；数据库 scope = **选择性共享**（解决"B review A / team 知识"）——团队场景两半都需要，claude auto-memory 在多 agent 共享仓库时是泄漏源，禁用/绕过

## 文件清单（当前）

```
mini-agents/
  go.mod                      ✅ go 1.26.5
  internal/store/schema.sql   ✅ 7 张表（issues/task_queue/agent_runs/agents/conversations/conversation_members/messages）
  internal/store/store.go     ✅ 数据层（含会话/消息 CRUD + GetMessageByTask + GetLatestRun）
  internal/store/store_test.go ✅ CreateIssue + 会话消息闭环测试通过
  cmd/server/main.go          ✅ HTTP 服务（issues/agents/conversations/messages 路由）
  cmd/agent/main.go           ✅ Agent 入口（-name 认领自己的任务；按 agents.engine 选引擎）
  cmd/dump/main.go            ✅ 查看任务+队列+执行日志的小工具（调试用）
  internal/agent/runner.go    ✅ Engine 接口 + ClaudeEngine（直连 claude.exe 避编码坑；注入角色人设）
  internal/agent/pi.go        ✅ PiEngine（pi --mode rpc，JSONL 流式攒 text_delta）
  internal/agent/deepseek.go  ✅ DeepSeekEngine 预留 stub（dsh 待接入）
  internal/agent/fake.go      ✅ FakeEngine（干跑用，不调 LLM 省 token）
  internal/agent/engine.go    ✅ NewEngine 工厂（按名字选实现，多态入口）
  web/index.html              ⬜ M6 飞书式前端（会话列表 + 消息流 + 输入框 + 2 秒轮询）
  internal/memory/            ⬜ M7 双层知识库（memory 表 + 文档/代码归档 + 注入）
```
