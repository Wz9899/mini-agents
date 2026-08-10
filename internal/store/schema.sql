-- 第 1 张表：任务（给人类看的业务视图）
CREATE TABLE IF NOT EXISTS issues (
  id            TEXT PRIMARY KEY,              -- 主键：唯一标识一条任务
  title         TEXT NOT NULL,                 -- 任务标题，不能为空
  description   TEXT NOT NULL DEFAULT '',      -- 任务描述，默认空字符串
  status        TEXT NOT NULL DEFAULT 'todo',  -- 状态：todo | in_progress | done | blocked
  assignee_type TEXT NOT NULL DEFAULT 'agent', -- 分配给谁的类型：member | agent
  assignee_id   TEXT,                          -- 分配给谁的具体 id
  created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,  -- 创建时间
  updated_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP   -- 更新时间
);

-- 第 2 张表：执行队列（给机器看的执行视图）
CREATE TABLE IF NOT EXISTS task_queue (
  id           TEXT PRIMARY KEY,               -- 队列项的唯一 id
  issue_id     TEXT NOT NULL REFERENCES issues(id),  -- 关联哪个任务（外键）
  status       TEXT NOT NULL DEFAULT 'queued', -- queued|dispatched|running|completed|failed|cancelled
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
