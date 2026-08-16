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
| M5 | 可靠性 | ✅ 完成 | 2026-08-15 | **M5 达成**：重试（failed→queued，attempts 上限 3）+ 耗尽→blocked 上报 + claude"我无法完成"检测直接 BlockTask（不重试省 token）+ 终端红色告警 + 协作异常互锁（上游卡死→依赖级联 blocked，防静默卡死）。deepseek/fake 双场景真实验证 |
| M6 | 飞书式前端 | ✅ 完成 | 2026-08-15 | **M6 达成**：web/index.html「夜间调度台」——左会话列表（带成员+状态灯）/右消息记录流/底部输入框 + 成员快捷 @ chip + 2 秒轮询；后端加 /api/team（TeamStatus）+ 会话带成员（GROUP_CONCAT）+ 静态文件服务；修 nil slice 序列化 bug |
| M8 | 触发语义重构 | ⬜ 计划中 | 2026-08-15 | **社交软件式触发**：单聊默认派活（无需 @）/ 单聊不解析 @ / 群聊 @ 才干活（校验群成员 + 多 @）/ 群聊不 @ 的消息 agent 干活时临时注入（最近 20 条，不沉淀） |
| M8-1 | 员工管理 UI（无职责入职） | ✅ 完成 | 2026-08-16 | **M8-1 达成**：前端"＋人"入职表单（role 可选，**空 = 无预设职责**通用员工）+ "＋"新建会话面板（成员多选）+ agent 身份空壳降级（identityLabel）；无职责员工小周端到端验证（入职→拉群→派活→回复） |

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
| 第 10 课 | **M5 可靠性** | `attempts` 自动重试（failed→queued）+ blocked 上报（重试耗尽 / claude"我无法完成"）+ 终端告警 | ✅ 已达成 |
| 第 11 课 | **M6 飞书式前端** | 会话列表 + 消息流 + 输入框 + 2 秒轮询（原"团队看板"改造） | ✅ 已达成 |
| 第 12 课 | **M7 双层知识库** | `memory` 表 scope（team/agent_id）+ 文档上传 + 代码归档 + 注入；**交流不沉淀**（会话历史临时查） | ✅ 已达成 |
| 第 13 课 | **M8 触发语义重构** | 单聊默认派活（不 @）/ 群聊 @ 才干活 + 成员校验 + 多 @ / 会话历史注入（来源会话最近 20 条） | ✅ 已达成 |
| 后 | **群聊 + 多 agent @** | 群成员各自领活（@提及 + 成员校验）→ **已被 M8 吸收** | ⬜ 并入 M8 |
| 后 | **M7 记忆注入优化** | M7 演进：注入预算控制（条数上限 + `maxTotalRecallChars` + 关键词 FTS/向量**相关性召回**）+ 注入分块（**动态上下文**前插 / **稳定上下文**追加 system 末尾 + Prompt Caching）——参考 TencentDB-Agent-Memory 的 Recall Budget 与 RecallResult 分块 | ⬜ 未排期 |

> 建议推进顺序（未确认）：M5 可靠性 → M6 飞书式前端 → M7 双层知识库

## 关键教学点（边学边记）

- **外键**：mini 版用了外键方便理解关系；**Multica 刻意不用外键**（多态 + Agent 异步写入难维护），这个反差是理解点
- **三张表分工**：issues=待办清单（人类看）、task_queue=流水线（机器看）、agent_runs=记账本（事后查）
- **状态机**：`queued → dispatched → running → completed/failed/blocked` ✅ 已实现（M5 补 blocked）
- **认领乐观锁**：`UPDATE ... WHERE status='queued'` + 检查 RowsAffected ✅ 已实现（两条 SQL 合一，防并发重复认领）
- **幂等完成**：`WHERE claim_token=? AND status IN ('dispatched','running')`——重复调用不会搞坏状态 ✅
- **NULL ≠ 空字符串**：数据库可空列 Scan 必须用 `sql.NullString`/`sql.NullTime`（`converting NULL to string` 真实 bug 教训）
- **Windows 编码坑**：npm 的 claude shim 走 cmd 会把中文参数按 GBK 破坏；解法是直连 `claude.exe`（Go 用 UTF-16 传参给 CreateProcess，中文无损）
- **失败也记账**：claude 执行失败也要写 agent_runs（留痕），再 FailTask
- **超时保护**：`context.WithTimeout` + `exec.CommandContext` 防止 claude 卡死 worker
- **可重试失败 vs 不可恢复问题**（V2 M5）：技术失败回退重试；claude"我无法完成"直接 blocked 上报人——区分二者才是省钱关键（每次重试=一次 token 消耗）✅ 已落地（第 1-3 步）
- **prompt 约定"我无法完成"格式**（V2 M5）：prompt 里要求 claude"做不到就以『我无法完成：原因』开头"，程序用 `strings.HasPrefix` 精确识别——比让模型自由发挥好识别得多；这是"机器和模型之间定暗号"的实践
- **CASE WHEN 原子决策**（V2 M5）：失败计数 + "回退 or 上报"分支合进一条 `UPDATE ... SET status = CASE WHEN attempts+1 < ? THEN 'queued' ELSE 'blocked' END`——判断下沉到 SQL 防两段式竞态；Go 侧返回状态与 SQL 同句逻辑，改要两边同步（测试兜底）
- **回退要作废旧凭证**（V2 M5）：`failed→queued` 回退时清 `claim_token=NULL, worker_id=NULL`，否则旧 token 找不回来，任务永远卡在 queued
- **终端红色告警**（V2 M5）：`\x1b[31m...\x1b[0m` ANSI 转义——blocked 时打红字横幅，2 秒轮询扫一眼就能发现需人介入的任务
- **协作异常互锁 / 级联 blocked**（V2 M5）：上游 failed/blocked → 依赖它的下游（queued）自动级联标 blocked + reason——否则下游认领的 EXISTS 检查（上游必须 completed）永远等不到，会**静默卡死**在 queued 没人知道
- **逐层传导防环**（V2 M5）：级联用"逐层循环"（C→B→A 层层传导）；`status='queued'` 条件天然防循环依赖死循环（已标过的不再命中）
- **UPDATE ... RETURNING**（V2 M5）：SQLite 3.35+ 语法，`UPDATE` 也能返回被更新行的列——"级联标 blocked + 拿回被标的下游 id 用于下一层"合进一条 SQL，省掉先 SELECT 再 UPDATE 两段式
- **GROUP_CONCAT 防 N+1**（V2 M6）：会话列表带成员用 `GROUP_CONCAT(a.name,'、')` 把多行拼成一行字符串，避免"查完会话再逐个查成员"的 N+1 次查询；LEFT JOIN 保住没成员的会话；Go 侧 strings.Split 拆回数组
- **相关子查询 + COALESCE 兜底**（V2 M6）：TeamStatus 用子查询取"该 agent 最新一条未结束任务的状态"；查不到行返回 NULL，COALESCE 兜底成 'idle'——NULL 不泄漏到前端
- **Go struct 嵌入**（V2 M6）：`type X struct { Conversation; Members []string }`——嵌入让外层自动拥有内层的全部字段，JSON 序列化时平铺，给会话"附加"成员数组零拷贝
- **nil slice vs 空 slice**（V2 M6 真 bug）：`var xs []T` 序列化成 JSON `null`（前端 for..of 会炸）；`xs := []T{}` 序列化成 `[]`（安全）——"三世界的不互通"第三次：Go nil ≠ 空数组 ≠ JSON null
- **FileServer 兜底路由**（V2 M6）：`mux.Handle("GET /", http.FileServer(http.Dir("web")))`——ServeMux 永远选"最长匹配"，/api/... 走精确路由，其余（/ 和 /index.html）落进静态文件
- **前端 2 秒轮询**（V2 M6）：`setInterval(tick, 2000)` + fetch 三个接口（conversations/team/messages）并行刷新，纯本地读零 token；发送后立即刷新不等轮询；滚动保持（在底部才贴底，翻历史不打扰）；fetch 失败只 console.warn 不打断轮询
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
- **Go 忽略下划线开头的 .go 文件**（M8 真坑）：文件名以 `_` 或 `.` 开头的 .go 文件会被 Go 构建工具**静默忽略**（`go run _v_schema.go` 报 `no Go files`）——临时验证脚本别用 `_` 前缀，用 `v_` 之类正常前缀
- **多对多拆关联表**（V2 M8）：一条消息 @ 多人 → 触发多个任务，`messages.task_id` 单值字段装不下。正解是拆 `message_tasks` 关联表（两个外键 + 组合主键）——把"多对多"拆成两张"1 对多"中间用连接表记配对，这是数据库关系建模的核心手法
- **role 空 = 无预设职责**（V2 M8-1）：创建员工时 role 可选，留空 = 纯通用员工（用户拍板"agent 无身份"）；后端去掉 `role 必填` 校验，前端显示"通用"。职责（身份层）人定，动作（执行层）引擎自主——两者分离是理解 agent 行为的钥匙
- **agent 启动时读一次身份**（V2 M8-1）：`me := GetAgent()` 在 main 读一次存内存、每轮复用——改 agents 表身份**必须重启进程**才生效，否则以为改了没生效（进程内缓存）
- **身份空壳降级**（V2 M8-1）：`identityLabel` 条件拼接——role 空时输出"小王"而非"小王（）"，prompt 同理（role/desc 都有时输出与之前一致，回归安全）
- **curl 命令行中文编码坑**（V2 M8-1 真坑）：Windows Git Bash 里 `curl -d '{"name":"小周"...}'` 中文被按 GBK 破坏成乱码（`С��`）；解法是 JSON 写 UTF-8 文件 + `curl --data-binary @file`。**前端 fetch 的 JSON.stringify 走 UTF-8 无此问题**——只坑命令行，产品功能不受影响
- **业务状态同步**（RD P0-1）：`issues.status` 是业务视图，`task_queue.status` 是流水线视图。两者必须在同一事务里同步（`StartTask→in_progress`、`CompleteTask→done`、`FailTask→in_progress/blocked`、`BlockTask→blocked`），否则 API 看到的是过期状态
- **写操作事务化**（RD P0-2/P0-3）：`CreateIssue` 要包住"issues 插入 + task_queue 入队"；`SendMessageWithTasks` 要包住"消息插入 + message_tasks 关联"——半截写是隐形 bug，事务是正解
- **孤儿任务回收**（RD P0-5）：进程被杀会留下 `dispatched/running` 任务。`running` 用 `started_at` 判超时；`dispatched` 且未开工必须单独记 `dispatched_at` 判超时——只认 `started_at` 会漏掉一类孤儿
- **旧库自动迁移**（RD P0-5）：`CREATE TABLE IF NOT EXISTS` 不会给旧表补新列。新增列时用 `PRAGMA table_info` 检查 + `ALTER TABLE` 补列，让旧 DB 打开时自动升级
- **@ 解析边界匹配**（RD P2-4）：`strings.Contains` 会把"@小王总"误判给"小王"。先按名字长度降序匹配长名，再检查 `@name` 后一个字符必须是空白/标点/结尾
- **空闲日志降噪**（RD P2-1）：每 2 秒一条"没有派给我的活"会刷爆日志。用状态变化触发打印（只在从有活到没活时打一次），既安静又能发现问题
- **前端发送错误处理**（前端审核 P0）：发消息前先检查 `res.ok`，失败时 `alert` 并保留输入框内容；发送期间禁用按钮，防止连按重复提交
- **前端链式轮询**（前端审核 P1）：`setInterval` 可能在上一次请求未结束时再次触发；改用 `setTimeout` 链式调用，等上一轮完成后再等 2 秒
- **前端统一渲染**（前端审核 P1）：`loadConvs` / `loadTeam` 只更新数据，由 `tick` 或提交成功后统一调用一次 `renderConvs()`，避免一次轮询里多次清空重建 DOM
- **前端无障碍**（前端审核 P2）：消息流加 `aria-live="polite"`，输入框加 `aria-label`，状态灯加 `role="img"` + `aria-label`，让屏幕阅读器能感知新消息和员工状态
- **M7 双层知识库落地**：`memory` 表按 `scope` 区分团队共享和个人私有；`internal/memory` 封装 Capture/Recall；`cmd/agent` 执行前用 `RecallMemoryForAgent` 把团队 + 个人知识注入 prompt；HTTP 提供 `POST/GET /api/memory`
- **time.Time.String() 存储格式坑**（V2 RD 验证真 bug）：SQLite 存 Go time.Time 时驱动按 `time.Time.String()` 落盘，带 `m=+XX.XXXX` 单调时钟后缀。单调时钟是进程私有读数，**扫描读回时必然丢失**（从字符串恢复不了）；再把读回的时间当查询参数绑回去，格式就与库值不一致（库值多了 ` m=...` 后缀）→ 字符串比较 `created_at > ?` 把"库值比前缀相同的参数长"当成 `>`，**游标消息自己也被返回** → 增量拉取重复返回。为什么孤儿回收没踩坑？它用 `time.Now().Add()` 直接生成 cutoff（进程内带 m=，与库值格式一致）；坑只出现在"扫描恢复"的时间戳上。**当前修复**：增量拉取不再用 `created_at` 做游标，改用消息 ULID `id > ?` 判断，ULID 本身按时间有序，从根上绕开单调时钟问题；前端恢复 `?after=` 增量拉取。后续仍建议统一时间格式常量（如 `2006-01-02 15:04:05.999999999 -07:00`），避免其他地方再次踩坑

## 文件清单（当前）

```
mini-agents/
  go.mod                      ✅ go 1.26.5
  .gitignore                  ✅ 运行产物/日志/数据库文件已忽略
  docs/rd.md                  ✅ 优化 RD 文档（P0-P2 清单与方案）
  internal/store/schema.sql   ✅ 8 张表 + 业务索引 + task_queue.dispatched_at
  internal/store/store.go     ✅ 数据层（事务化 CreateIssue/SendMessageWithTasks；issues.status 同步；RequeueStaleTasks 孤儿回收）
  internal/store/store_test.go ✅ 任务/会话消息/重试/blocked/级联/团队状态/状态同步/孤儿回收/消息事务测试
    cmd/server/main_test.go     ✅ HTTP handler 测试（创建任务/员工/消息触发任务）
  cmd/server/main.go          ✅ HTTP 服务（默认 127.0.0.1:8080；issues/agents/conversations/messages/team 路由 + 静态文件）
  cmd/agent/main.go           ✅ Agent 入口（-name 认领自己的任务；孤儿回收；失败重试/上报/级联；空闲日志降噪）
  cmd/dump/main.go            ✅ 查看任务+队列+执行日志的小工具（调试用）
  internal/agent/runner.go    ✅ Engine 接口 + ClaudeEngine（直连 claude.exe 避编码坑；注入角色人设）
  internal/agent/pi.go        ✅ PiEngine（pi --mode rpc，JSONL 流式攒 text_delta）
  internal/agent/deepseek.go  ✅ DeepSeekEngine 预留 stub（dsh 待接入）
  internal/agent/fake.go      ✅ FakeEngine（干跑用，不调 LLM 省 token）
  internal/agent/engine.go    ✅ NewEngine 工厂（按名字选实现，多态入口）
    internal/agent/engine_test.go ✅ 引擎测试（FakeEngine + NewEngine 工厂）
  web/index.html              ✅ M6 飞书式前端「夜间调度台」（会话列表+消息流+@chip+链式轮询+增量拉取+状态灯+无障碍；M8-1 加入职/新建会话面板）
  internal/memory/memory.go   ✅ M7 双层知识库（Capture/Recall/RecallForAgent 门面）
```
