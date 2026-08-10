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
	db, err := sql.Open("sqlite", path)
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

// Issue 代表一条任务（对应数据库 issues 表的一行）
type Issue struct {
	ID           string
	Title        string
	Description  string
	Status       string
	AssigneeType string
	AssigneeID   string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CreateIssue 把一条新任务写入数据库，返回带 id 和时间戳的完整任务
func (s *Store) CreateIssue(ctx context.Context, title, description string) (*Issue, error) {
	now := time.Now()
	issue := &Issue{
		ID:          NewID(),
		Title:       title,
		Description: description,
		Status:      "todo",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO issues (id, title, description, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		issue.ID, issue.Title, issue.Description, issue.Status, issue.CreatedAt, issue.UpdatedAt,
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
// 认领成功返回任务；队列空或没抢到返回 (nil, nil)；出错返回错误。
func (s *Store) ClaimTask(ctx context.Context, workerID string) (*QueuedTask, error) {
	// 第一步：挑——找排队最久的一条还没人认领的
	var id, issueID string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, issue_id FROM task_queue
		 WHERE status = 'queued' ORDER BY created_at LIMIT 1`,
	).Scan(&id, &issueID)
	if err == sql.ErrNoRows {
		return nil, nil // 队列空：没有活可干
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
	// assignee_id 是可空列，先扫进 NullString 再取（NULL ≠ ""，直接扫 string 会报错）
	var assigneeID sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, title, description, status, assignee_type, assignee_id, created_at, updated_at
		 FROM issues WHERE id = ?`, id,
	).Scan(&i.ID, &i.Title, &i.Description, &i.Status, &i.AssigneeType, &assigneeID, &i.CreatedAt, &i.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil // 没这条任务
	}
	if err != nil {
		return nil, fmt.Errorf("查询任务失败: %w", err)
	}
	if assigneeID.Valid {
		i.AssigneeID = assigneeID.String
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
		`SELECT id, title, description, status, created_at, updated_at
		 FROM issues ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("查询任务失败: %w", err)
	}
	defer rows.Close()

	var issues []Issue
	for rows.Next() {
		var i Issue
		if err := rows.Scan(&i.ID, &i.Title, &i.Description, &i.Status, &i.CreatedAt, &i.UpdatedAt); err != nil {
			return nil, fmt.Errorf("读取行失败: %w", err)
		}
		issues = append(issues, i)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历结果失败: %w", err)
	}
	return issues, nil
}
