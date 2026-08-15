-- 第 1 张表：任务（给人类看的业务视图）
CREATE TABLE IF NOT EXISTS issues (
  id            TEXT PRIMARY KEY,              -- 主键：唯一标识一条任务
  title         TEXT NOT NULL,                 -- 任务标题，不能为空
  description   TEXT NOT NULL DEFAULT '',      -- 任务描述，默认空字符串
  status        TEXT NOT NULL DEFAULT 'todo',  -- 状态：todo | in_progress | done | blocked
  assignee_type TEXT NOT NULL DEFAULT 'agent', -- 分配给谁的类型：member | agent
  assignee_id   TEXT,                          -- 分配给谁的具体 id
  depends_on    TEXT,                          -- 上游任务 id（issues.id）；NULL=无依赖（协作任务用）
  created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,  -- 创建时间
  updated_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP   -- 更新时间
);

-- 第 2 张表：执行队列（给机器看的执行视图）
CREATE TABLE IF NOT EXISTS task_queue (
  id           TEXT PRIMARY KEY,               -- 队列项的唯一 id
  issue_id     TEXT NOT NULL REFERENCES issues(id),  -- 关联哪个任务（外键）
  status       TEXT NOT NULL DEFAULT 'queued', -- queued|dispatched|running|completed|failed|blocked|cancelled
  attempts     INTEGER NOT NULL DEFAULT 0,     -- 尝试执行了几次
  dedup_sha    TEXT,                           -- 防重复：相同任务只入队一次
  claim_token  TEXT,                           -- 认领凭证：谁认领了，谁就能完成
  worker_id    TEXT,                           -- 是哪个 worker 在执行
  error        TEXT,                           -- 失败时记录错误信息
  created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  started_at   TIMESTAMP,                      -- 开始执行时间（可能为空）
  finished_at  TIMESTAMP                       -- 结束时间（可能为空）
);

-- 第 3 张表：执行日志（事后查询用的）
CREATE TABLE IF NOT EXISTS agent_runs (
  id           TEXT PRIMARY KEY,
  task_id      TEXT NOT NULL REFERENCES task_queue(id),  -- 外键指向队列项
  exit_code    INTEGER,                        -- 退出码：0 成功，非 0 失败
  output       TEXT,                           -- Agent 返回的文本
  duration_ms  INTEGER,                        -- 执行耗时（毫秒）
  created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 第 4 张表：员工档案（每个 Agent 一个进程，这里存身份）
CREATE TABLE IF NOT EXISTS agents (
  id           TEXT PRIMARY KEY,                -- 员工唯一 id（ULID，同 V1 的 NewID()）
  name         TEXT NOT NULL UNIQUE,            -- 名字：小王 / 小李（cmd/agent -name 要对上）
  role         TEXT NOT NULL,                   -- 角色：前端工程师 / 后端工程师
  description  TEXT NOT NULL DEFAULT '',        -- 人设：一句话介绍自己
  engine       TEXT NOT NULL DEFAULT 'claude',  -- 引擎：claude | pi | deepseek（谁干活）
  created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP  -- 入职时间
);

-- 第 5 张表：会话（飞书式：单聊 direct / 群聊 group）
CREATE TABLE IF NOT EXISTS conversations (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL,                        -- 会话名：单聊"和小王的私聊"、群聊"项目组"
  type         TEXT NOT NULL DEFAULT 'direct',       -- direct 单聊 | group 群聊
  created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 第 6 张表：会话成员（哪些 agent 在这个会话里；人类是隐式所有者，不建行）
CREATE TABLE IF NOT EXISTS conversation_members (
  conversation_id TEXT NOT NULL REFERENCES conversations(id),  -- 属于哪个会话
  agent_id        TEXT NOT NULL REFERENCES agents(id),         -- 哪位员工在群里
  PRIMARY KEY (conversation_id, agent_id)                      -- 组合主键：一个会话一个 agent 最多一行
);

-- 第 7 张表：消息（会话里的消息流）
CREATE TABLE IF NOT EXISTS messages (
  id              TEXT PRIMARY KEY,
  conversation_id TEXT NOT NULL REFERENCES conversations(id),  -- 属于哪个会话
  sender_type     TEXT NOT NULL DEFAULT 'user',                -- user 人类 | agent
  sender_id       TEXT,                                        -- agent 填 agents.id；user 填 'me'
  content         TEXT NOT NULL,                               -- 消息正文
  task_id         TEXT,                                        -- 由这条消息触发的工作（issues.id），普通消息为 NULL
  created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 第 8 张表：消息 ↔ 任务 关联表（M8 多 @：一条消息可触发多个任务）
-- 为什么需要它：messages.task_id 只能存一个任务 id。一条消息 @ 多人 → 触发多个任务，
-- 一个字段装不下多个值。关系建模的正解是拆一张"关联表"：
-- 把"消息 → 任务"的 1 对多，拆成两张 1 对多（消息→关联行、任务→关联行），中间用这张表连接。
CREATE TABLE IF NOT EXISTS message_tasks (
  message_id TEXT NOT NULL REFERENCES messages(id),  -- 消息 id（外键指向 messages）
  task_id    TEXT NOT NULL REFERENCES issues(id),    -- 任务 id（外键指向 issues）
  PRIMARY KEY (message_id, task_id)                  -- 组合主键：同一个(消息,任务)组合最多出现一次
);
