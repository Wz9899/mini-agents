package store

import (
	"context"
	"crypto/rand"
	_ "embed"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
	"github.com/oklog/ulid/v2"
)

//go:embed schema.sql
var schemaSQL string

// Store 封装了所有数据库操作
type Store struct {
	db *sql.DB
}

// Open 打开（或创建）SQLite 数据库文件，并自动建表
func Open(path string) (*Store, error) {
	// DSN 里带两条 pragma：多进程并发写 SQLite 的地基
	//   busy_timeout(5000)：撞锁时先等 5 秒，别立刻报 database is locked
	//   journal_mode(WAL)： 预写日志，读写不互斥，写冲突窗口更小
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close() // 建表失败：刚开的连接不能泄漏，关掉再返回错误
		return nil, fmt.Errorf("建表失败: %w", err)
	}
	return &Store{db: db}, nil
}

// Close 关闭数据库连接，释放文件占用
func (s *Store) Close() error {
	return s.db.Close()
}

// NewID 生成一个 ULID 主键
func NewID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// Agent 代表一个团队员工（对应数据库 agents 表的一行）
type Agent struct {
	ID          string
	Name        string
	Role        string
	Description string
	Engine      string // claude | pi | deepseek（M4.5 加的列，谁干活）
	CreatedAt   time.Time
}

// CreateAgent 让一名员工"入职"：写入 agents 表，返回带 id 和时间戳的完整档案
func (s *Store) CreateAgent(ctx context.Context, name, role, description, engine string) (*Agent, error) {
	now := time.Now()
	agent := &Agent{
		ID:          NewID(),
		Name:        name,
		Role:        role,
		Description: description,
		Engine:      engine,
		CreatedAt:   now,
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agents (id, name, role, description, engine, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		agent.ID, agent.Name, agent.Role, agent.Description, agent.Engine, agent.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("创建员工失败: %w", err)
	}
	return agent, nil
}

// ListAgents 返回全部员工档案（旧的在前）
func (s *Store) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, role, description, engine, created_at FROM agents ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("查询员工失败: %w", err)
	}
	defer rows.Close()

	var agents []Agent
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.ID, &a.Name, &a.Role, &a.Description, &a.Engine, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("读取员工行失败: %w", err)
		}
		agents = append(agents, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历员工失败: %w", err)
	}
	return agents, nil
}

// GetAgent 按名字查员工；找不到返回 (nil, nil)。
// cmd/agent -name 小王 启动时用它拿 role/description 和 engine
func (s *Store) GetAgent(ctx context.Context, name string) (*Agent, error) {
	var a Agent
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, role, description, engine, created_at FROM agents WHERE name = ?`, name,
	).Scan(&a.ID, &a.Name, &a.Role, &a.Description, &a.Engine, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询员工失败: %w", err)
	}
	return &a, nil
}

// Conversation 代表一个会话（对应 conversations 表：direct 单聊 / group 群聊）
type Conversation struct {
	ID        string
	Name      string
	Type      string // direct | group
	CreatedAt time.Time
}

// Message 代表会话里的一条消息（messages 表）
type Message struct {
	ID             string
	ConversationID string
	SenderType     string // user 人类 | agent
	SenderID       string // agent 填 agents.id；user 填 'me'
	Content        string
	TaskID         string // 由这条消息触发的工作（issues.id）；空 = 普通消息
	CreatedAt      time.Time
}

// CreateConversation 建一个会话并把指定的 agent 拉进来。
// 事务：会话 + 成员是两个写操作，必须原子（要么都成，要么都回滚），
// 否则会出现"会话建了但成员没拉进来"的脏状态。
func (s *Store) CreateConversation(ctx context.Context, name, typ string, memberAgentIDs []string) (*Conversation, error) {
	tx, err := s.db.BeginTx(ctx, nil) // 开启事务
	if err != nil {
		return nil, fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback() // 惯用法：中途出错回滚；Commit 成功后 Rollback 是空操作

	c := &Conversation{ID: NewID(), Name: name, Type: typ, CreatedAt: time.Now()}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO conversations (id, name, type, created_at) VALUES (?, ?, ?, ?)`,
		c.ID, c.Name, c.Type, c.CreatedAt); err != nil {
		return nil, fmt.Errorf("创建会话失败: %w", err)
	}

	// 把每位 agent 拉进会话（组合主键保证不会重复拉）
	for _, aid := range memberAgentIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO conversation_members (conversation_id, agent_id) VALUES (?, ?)`,
			c.ID, aid); err != nil {
			return nil, fmt.Errorf("添加成员失败: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交事务失败: %w", err)
	}
	return c, nil
}

// ListConversations 列出所有会话（旧的在前）
func (s *Store) ListConversations(ctx context.Context) ([]Conversation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, type, created_at FROM conversations ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("查询会话失败: %w", err)
	}
	defer rows.Close()

	var convs []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.Name, &c.Type, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("读取会话行失败: %w", err)
		}
		convs = append(convs, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历会话失败: %w", err)
	}
	return convs, nil
}

// GetConversation 按 id 查会话；找不到返回 (nil, nil)。
// 发消息前用：校验会话存在，避免把消息写进不存在的会话
func (s *Store) GetConversation(ctx context.Context, id string) (*Conversation, error) {
	var c Conversation
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, type, created_at FROM conversations WHERE id = ?`, id,
	).Scan(&c.ID, &c.Name, &c.Type, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询会话失败: %w", err)
	}
	return &c, nil
}

// GetMessageByTask 按任务 id 找触发它的消息。
// agent 完成任务后用它反查"往哪个会话回消息"：消息.task_id 关联了任务。
func (s *Store) GetMessageByTask(ctx context.Context, issueID string) (*Message, error) {
	var m Message
	var taskID sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, conversation_id, sender_type, sender_id, content, task_id, created_at
		 FROM messages WHERE task_id = ? ORDER BY created_at LIMIT 1`, issueID,
	).Scan(&m.ID, &m.ConversationID, &m.SenderType, &m.SenderID, &m.Content, &taskID, &m.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil // 没找到触发消息（任务可能是 API 直接建的，不是消息触发的）
	}
	if err != nil {
		return nil, fmt.Errorf("查询消息失败: %w", err)
	}
	if taskID.Valid {
		m.TaskID = taskID.String
	}
	return &m, nil
}

// SendMessage 往会话里发一条消息（人类和 agent 都能发，sender_type 区分）。
// taskID 是这条消息对应的工作（issues.id）；空串 = 普通消息
func (s *Store) SendMessage(ctx context.Context, conversationID, senderType, senderID, content, taskID string) (*Message, error) {
	m := &Message{
		ID:             NewID(),
		ConversationID: conversationID,
		SenderType:     senderType,
		SenderID:       senderID,
		Content:        content,
		TaskID:         taskID, // 触发/对应的工作；空 = 普通消息
		CreatedAt:      time.Now(),
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (id, conversation_id, sender_type, sender_id, content, task_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.ConversationID, m.SenderType, m.SenderID, m.Content, m.TaskID, m.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("发送消息失败: %w", err)
	}
	return m, nil
}

// ListMessages 取一个会话的消息流（旧的在前，按时间排）
func (s *Store) ListMessages(ctx context.Context, conversationID string) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, conversation_id, sender_type, sender_id, content, task_id, created_at
		 FROM messages WHERE conversation_id = ? ORDER BY created_at`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("查询消息失败: %w", err)
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		var taskID sql.NullString // task_id 可空：普通消息没有关联任务
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.SenderType, &m.SenderID, &m.Content, &taskID, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("读取消息行失败: %w", err)
		}
		if taskID.Valid {
			m.TaskID = taskID.String
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历消息失败: %w", err)
	}
	return msgs, nil
}

// Issue 代表一条任务（对应数据库 issues 表的一行）
type Issue struct {
	ID           string
	Title        string
	Description  string
	Status       string
	AssigneeType string
	AssigneeID   string
	DependsOn    string // 上游任务 id（issues.id）；空 = 无依赖（协作任务用）
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CreateIssue 把一条新任务写入数据库，返回带 id 和时间戳的完整任务。
// assigneeID 指明派给哪位员工（对应 agents.id）；dependsOn 是上游任务 id，空串 = 无依赖
func (s *Store) CreateIssue(ctx context.Context, title, description, assigneeID, dependsOn string) (*Issue, error) {
	now := time.Now()
	issue := &Issue{
		ID:           NewID(),
		Title:        title,
		Description:  description,
		Status:       "todo",
		AssigneeType: "agent",    // 派给的是 agent（V2 里目前只有 agent 一种）
		AssigneeID:   assigneeID, // 派给谁的员工 id
		DependsOn:    dependsOn,  // 上游任务 id；空 = 无依赖
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	// 依赖：空串 = 无依赖，写库时转成 SQL NULL！
	// 为什么？认领 SQL 用 IS NULL 判断"无依赖"。Go 空串 "" 写进库是 ''（有值，不是 NULL），
	// 会把无依赖任务误当成"依赖了个空串"而过滤掉。三个世界的"无"（Go ""、SQL NULL、JSON 空）不互通，边界必须显式转。
	var dependsOnArg any // 空接口：装 string 就写字符串，装 nil 就写 NULL
	if dependsOn != "" {
		dependsOnArg = dependsOn
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO issues (id, title, description, status, assignee_type, assignee_id, depends_on, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		issue.ID, issue.Title, issue.Description, issue.Status, issue.AssigneeType, issue.AssigneeID, dependsOnArg, issue.CreatedAt, issue.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("创建任务失败: %w", err)
	}

	// 任务建好，自动入队：让 worker 能领到这份活
	if err := s.EnqueueTask(ctx, issue.ID); err != nil {
		return nil, err
	}
	return issue, nil
}

// EnqueueTask 往执行队列里插入一条记录，表示"这份活排队等 worker 来干"
// issueID 是哪条任务的 id（一条任务最多对应一条队列项）
func (s *Store) EnqueueTask(ctx context.Context, issueID string) error {
	// dedup_sha 先存 issueID：用它标识"同一任务"，将来重试时查它防重复入队
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO task_queue (id, issue_id, status, dedup_sha)
		 VALUES (?, ?, 'queued', ?)`,
		NewID(), issueID, issueID,
	)
	if err != nil {
		return fmt.Errorf("任务入队失败: %w", err)
	}
	return nil
}

// QueuedTask 是一次认领到手的"活"：worker 凭它干活，并凭 ClaimToken 汇报结果
type QueuedTask struct {
	ID         string // 队列项 id（task_queue.id）
	IssueID    string // 对应哪条任务（issues.id）
	ClaimToken string // 认领凭证：只有持有它才能标记完成/失败
}

// ClaimTask 尝试认领一条 queued 任务。
// assigneeID 是"我是谁"：只认领派给我的任务。
// 认领成功返回任务；队列空或没抢到返回 (nil, nil)；出错返回错误。
func (s *Store) ClaimTask(ctx context.Context, workerID, assigneeID string) (*QueuedTask, error) {
	// 第一步：挑——从"派给我"的任务里找排队最久的一条
	// 注意：assignee_id 在 issues 表，不在 task_queue，所以要 JOIN 连表查
	// 依赖过滤（协作任务）：没依赖直接放行；有依赖必须等上游 completed
	var id, issueID string
	err := s.db.QueryRowContext(ctx,
		`SELECT t.id, t.issue_id FROM task_queue t
		 JOIN issues i ON i.id = t.issue_id
		 WHERE t.status = 'queued' AND i.assignee_id = ?
		   AND (i.depends_on IS NULL
		        OR EXISTS (SELECT 1 FROM task_queue d
		                   WHERE d.issue_id = i.depends_on AND d.status = 'completed'))
		 ORDER BY t.created_at LIMIT 1`,
		assigneeID,
	).Scan(&id, &issueID)
	if err == sql.ErrNoRows {
		return nil, nil // 没有派给我的活
	}
	if err != nil {
		return nil, fmt.Errorf("查找待认领任务失败: %w", err)
	}

	// 第二步：抢——乐观锁，把"抢 + 贴标签"合并成一条 SQL
	claimToken := NewID() // 本次认领的凭证，完成/失败时要用它对账
	res, err := s.db.ExecContext(ctx,
		`UPDATE task_queue
		 SET status = 'dispatched', claim_token = ?, worker_id = ?
		 WHERE id = ? AND status = 'queued'`,
		claimToken, workerID, id,
	)
	if err != nil {
		return nil, fmt.Errorf("认领任务失败: %w", err)
	}

	affected, err := res.RowsAffected() // 影响行数：1=抢到，0=没抢到
	if err != nil {
		return nil, fmt.Errorf("读取认领结果失败: %w", err)
	}
	if affected == 0 {
		return nil, nil // 没抢到：别人更快，这一轮空手而归
	}

	return &QueuedTask{ID: id, IssueID: issueID, ClaimToken: claimToken}, nil
}

// CompleteTask 凭认领凭证，把任务标记为已完成。
// 凭证不匹配（或任务已被处理）时返回错误——这就是"幂等"：重复调用不会重复搞坏状态。
func (s *Store) CompleteTask(ctx context.Context, task *QueuedTask) error {
	res, err := s.db.ExecContext(ctx,
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
	return nil
}

// FailTask 凭认领凭证，把任务标记为失败，并记下错误原因。
func (s *Store) FailTask(ctx context.Context, task *QueuedTask, errMsg string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE task_queue
		 SET status = 'failed', error = ?, finished_at = ?
		 WHERE id = ? AND claim_token = ? AND status IN ('dispatched', 'running')`,
		errMsg, time.Now(), task.ID, task.ClaimToken,
	)
	if err != nil {
		return fmt.Errorf("标记失败失败: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取失败结果失败: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("标记失败失败: 凭证不匹配或任务已被处理 (id=%s)", task.ID)
	}
	return nil
}

// StartTask 标记任务"正式开始执行"：status → running，并记录开始时间。
func (s *Store) StartTask(ctx context.Context, task *QueuedTask) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE task_queue
		 SET status = 'running', started_at = ?
		 WHERE id = ? AND claim_token = ? AND status = 'dispatched'`,
		time.Now(), task.ID, task.ClaimToken,
	)
	if err != nil {
		return fmt.Errorf("标记开始失败: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取开始结果失败: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("标记开始失败: 任务状态已变化 (id=%s)", task.ID)
	}
	return nil
}

// QueueItem 表示执行队列里的一行（用于查看流水线状态）
type QueueItem struct {
	ID         string
	IssueID    string
	Status     string
	Attempts   int
	WorkerID   sql.NullString // 可空列：还没人认领就是 NULL
	Error      sql.NullString // 可空列：没失败就是 NULL
	CreatedAt  time.Time
	StartedAt  sql.NullTime // 可空列：没开工就是 NULL
	FinishedAt sql.NullTime // 可空列：没完成就是 NULL
}

// ListQueue 返回队列里的全部记录（旧的在前），方便查看流水线状态
func (s *Store) ListQueue(ctx context.Context) ([]QueueItem, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, issue_id, status, attempts, worker_id, error, created_at, started_at, finished_at
		 FROM task_queue ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("查询队列失败: %w", err)
	}
	defer rows.Close()

	var items []QueueItem
	for rows.Next() {
		var it QueueItem
		if err := rows.Scan(&it.ID, &it.IssueID, &it.Status, &it.Attempts, &it.WorkerID, &it.Error, &it.CreatedAt, &it.StartedAt, &it.FinishedAt); err != nil {
			return nil, fmt.Errorf("读取队列行失败: %w", err)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历队列失败: %w", err)
	}
	return items, nil
}

// GetIssue 按 id 返回一条任务；找不到返回 (nil, nil)
func (s *Store) GetIssue(ctx context.Context, id string) (*Issue, error) {
	var i Issue
	// assignee_id / depends_on 都是可空列，先扫进 NullString 再取（NULL ≠ ""，直接扫 string 会报错）
	var assigneeID, dependsOn sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, title, description, status, assignee_type, assignee_id, depends_on, created_at, updated_at
		 FROM issues WHERE id = ?`, id,
	).Scan(&i.ID, &i.Title, &i.Description, &i.Status, &i.AssigneeType, &assigneeID, &dependsOn, &i.CreatedAt, &i.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil // 没这条任务
	}
	if err != nil {
		return nil, fmt.Errorf("查询任务失败: %w", err)
	}
	if assigneeID.Valid {
		i.AssigneeID = assigneeID.String
	}
	if dependsOn.Valid {
		i.DependsOn = dependsOn.String
	}
	return &i, nil
}

// RecordRun 把一次执行结果记入 agent_runs 表（记账本），返回日志的 id
func (s *Store) RecordRun(ctx context.Context, task *QueuedTask, exitCode int, output string, durationMs int64) (string, error) {
	id := NewID()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_runs (id, task_id, exit_code, output, duration_ms)
		 VALUES (?, ?, ?, ?, ?)`,
		id, task.ID, exitCode, output, durationMs,
	)
	if err != nil {
		return "", fmt.Errorf("记录执行结果失败: %w", err)
	}
	return id, nil
}

// GetLatestRun 按任务（issue）找最近一次执行的输出。
// 协作任务用它读上游成果：B 要 review A 的代码，就从这拿 A 的输出。
// 链路：issues → task_queue → agent_runs 三表 JOIN（runs 挂队列项 id，不挂 issue id）
func (s *Store) GetLatestRun(ctx context.Context, issueID string) (string, error) {
	var output string
	err := s.db.QueryRowContext(ctx,
		`SELECT r.output FROM agent_runs r
		 JOIN task_queue q ON q.id = r.task_id
		 WHERE q.issue_id = ? ORDER BY r.created_at DESC LIMIT 1`,
		issueID,
	).Scan(&output)
	if err == sql.ErrNoRows {
		return "", nil // 上游还没执行记录（依赖条件已保证它 completed，正常不该发生）
	}
	if err != nil {
		return "", fmt.Errorf("查询执行日志失败: %w", err)
	}
	return output, nil
}

// RunRecord 是执行日志的一行（agent_runs）
type RunRecord struct {
	ID         string
	TaskID     string
	ExitCode   int
	Output     string
	DurationMs int64
	CreatedAt  time.Time
}

// ListRuns 返回全部执行日志（旧的在前），方便查看 claude 干过什么
func (s *Store) ListRuns(ctx context.Context) ([]RunRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, task_id, exit_code, output, duration_ms, created_at
		 FROM agent_runs ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("查询执行日志失败: %w", err)
	}
	defer rows.Close()

	var runs []RunRecord
	for rows.Next() {
		var r RunRecord
		if err := rows.Scan(&r.ID, &r.TaskID, &r.ExitCode, &r.Output, &r.DurationMs, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("读取执行日志行失败: %w", err)
		}
		runs = append(runs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历执行日志失败: %w", err)
	}
	return runs, nil
}

// ListIssues 返回数据库里所有任务，最新的排在最前面
func (s *Store) ListIssues(ctx context.Context) ([]Issue, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, title, description, status, assignee_type, assignee_id, depends_on, created_at, updated_at
		 FROM issues ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("查询任务失败: %w", err)
	}
	defer rows.Close()

	var issues []Issue
	for rows.Next() {
		var i Issue
		// assignee_id / depends_on 可空，先扫 NullString 再取
		var assigneeID, dependsOn sql.NullString
		if err := rows.Scan(&i.ID, &i.Title, &i.Description, &i.Status, &i.AssigneeType, &assigneeID, &dependsOn, &i.CreatedAt, &i.UpdatedAt); err != nil {
			return nil, fmt.Errorf("读取行失败: %w", err)
		}
		if assigneeID.Valid {
			i.AssigneeID = assigneeID.String
		}
		if dependsOn.Valid {
			i.DependsOn = dependsOn.String
		}
		issues = append(issues, i)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历结果失败: %w", err)
	}
	return issues, nil
}
