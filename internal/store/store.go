package store

import (
	"context"
	"crypto/rand"
	_ "embed"
	"database/sql"
	"fmt"
	"strings"
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

	// 旧库迁移：task_queue 新增的 dispatched_at 列不会由 CREATE TABLE IF NOT EXISTS 补上。
	// 这里检查列是否存在，不存在则 ALTER TABLE 补列。
	if err := ensureColumn(db, "task_queue", "dispatched_at", "TIMESTAMP"); err != nil {
		db.Close()
		return nil, fmt.Errorf("迁移 task_queue.dispatched_at 失败: %w", err)
	}

	return &Store{db: db}, nil
}

// ensureColumn 检查表里是否已有某列，没有就 ALTER TABLE 补一列。
// table/column/ddl 都来自代码硬编码，不拼用户输入，无 SQL 注入风险。
func ensureColumn(db *sql.DB, table, column, ddl string) error {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil // 列已存在
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	_, err = db.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + ddl)
	return err
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

	agents := []Agent{} // 空 slice（不是 nil！）：JSON 序列化出 []，前端遍历不炸
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

// GetAgentByID 按 id 查员工；找不到返回 (nil, nil)。
// agent 进程用它做身份热更新：员工改名后不需要重启进程，下一轮按 id 重新读取即可。
func (s *Store) GetAgentByID(ctx context.Context, id string) (*Agent, error) {
	var a Agent
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, role, description, engine, created_at FROM agents WHERE id = ?`, id,
	).Scan(&a.ID, &a.Name, &a.Role, &a.Description, &a.Engine, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询员工失败: %w", err)
	}
	return &a, nil
}

// UpdateAgent 更新员工档案：支持改名、改角色、改描述。
// 重名时返回 UNIQUE 冲突错误，由上层转成 409。
func (s *Store) UpdateAgent(ctx context.Context, id, name, role, description string) (*Agent, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE agents SET name = ?, role = ?, description = ? WHERE id = ?`,
		name, role, description, id,
	)
	if err != nil {
		return nil, fmt.Errorf("更新员工失败: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("读取更新结果失败: %w", err)
	}
	if affected == 0 {
		return nil, fmt.Errorf("员工不存在: %s", id)
	}
	return s.GetAgentByID(ctx, id)
}

// DeleteAgent 硬删除员工。
// 硬删除会清理：
//   - 会话成员关系（conversation_members）
//   - 个人私有知识（memory where scope='agent'）
// 历史消息/任务/执行日志保留，但 sender/assignee 关联会变成悬空 id。
func (s *Store) DeleteAgent(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM conversation_members WHERE agent_id = ?`, id,
	); err != nil {
		return fmt.Errorf("清理会话成员失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM memory WHERE scope = 'agent' AND agent_id = ?`, id,
	); err != nil {
		return fmt.Errorf("清理个人知识失败: %w", err)
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM agents WHERE id = ?`, id,
	)
	if err != nil {
		return fmt.Errorf("删除员工失败: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取删除结果失败: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("员工不存在: %s", id)
	}

	return tx.Commit()
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

	convs := []Conversation{}
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

// ConversationWithMembers 会话 + 成员名字列表（前端左栏显示用）。
// 嵌入 Conversation：它自动"拥有" Conversation 的全部字段（ID/Name/Type/CreatedAt），
// 序列化成 JSON 时这些字段会平铺在外层，外面再加一个 Members 数组。
type ConversationWithMembers struct {
	Conversation
	Members []string // 成员名字（'小王'、'小李'…）；没有成员的会话为空
}

// ListConversationsWithMembers 列出所有会话，每个会话带成员名字列表。
// 为什么用 GROUP_CONCAT 而不用"先查会话再逐个查成员"？
// 后者是 N+1 查询（1 次会话 + N 次成员查询）；GROUP_CONCAT 把"一个会话的所有成员名"
// 拼成一个字符串 '小王、小李'，一条 JOIN 全拿到。
// LEFT JOIN 保证：没成员的会话也有一行（成员是 NULL），不会漏会话。
func (s *Store) ListConversationsWithMembers(ctx context.Context) ([]ConversationWithMembers, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT c.id, c.name, c.type, c.created_at,
		        GROUP_CONCAT(a.name, '、' ORDER BY a.rowid)
		 FROM conversations c
		 LEFT JOIN conversation_members cm ON cm.conversation_id = c.id
		 LEFT JOIN agents a ON a.id = cm.agent_id
		 GROUP BY c.id ORDER BY c.created_at`)
	if err != nil {
		return nil, fmt.Errorf("查询会话失败: %w", err)
	}
	defer rows.Close()

	convs := []ConversationWithMembers{}
	for rows.Next() {
		var c ConversationWithMembers
		var members sql.NullString // 没成员时 GROUP_CONCAT 结果是 NULL
		if err := rows.Scan(&c.ID, &c.Name, &c.Type, &c.CreatedAt, &members); err != nil {
			return nil, fmt.Errorf("读取会话行失败: %w", err)
		}
		if members.Valid && members.String != "" {
			c.Members = strings.Split(members.String, "、") // '小王、小李' → [小王 小李]
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

// RenameConversation 修改会话名称（群聊改名/单聊显示名由前端控制）。
// 为什么不沿用 RowsAffected()==0 判断"会话不存在"？
// RowsAffected 返回"实际变更的行数"，值没变的 UPDATE 也算 0——比如给会话改一个
// 相同的名字，SQLite 不会真的写，返回 0。此时"会话存在但没变化"和"会话不存在"
// 都表现为 0，用 RowsAffected 判断会把前者误报成后者（404）。
// 所以先 GetConversation 确认存在，再 UPDATE，两者分开判断。
func (s *Store) RenameConversation(ctx context.Context, id, name string) error {
	existing, err := s.GetConversation(ctx, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("会话不存在: %s", id)
	}

	if _, err := s.db.ExecContext(ctx,
		`UPDATE conversations SET name = ? WHERE id = ?`, name, id,
	); err != nil {
		return fmt.Errorf("修改会话名失败: %w", err)
	}
	return nil
}


// ListConversationMembers 返回一个会话里的所有 agent 成员（按名字排序）。
// 单聊 = 1 人、群聊 = N 人；人类是隐式所有者，不在 members 表里。
// M8 触发逻辑要用它：单聊直接派给唯一成员；群聊校验 @ 的人是否在群里。
func (s *Store) ListConversationMembers(ctx context.Context, conversationID string) ([]Agent, error) {
	// conversation_members 只有两个 id（conversation_id + agent_id），要拿员工完整信息必须 JOIN agents
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.id, a.name, a.role, a.description, a.engine, a.created_at
		 FROM conversation_members cm
		 JOIN agents a ON a.id = cm.agent_id
		 WHERE cm.conversation_id = ?
		 ORDER BY a.rowid`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("查询会话成员失败: %w", err)
	}
	defer rows.Close()

	members := []Agent{} // 空 slice（不是 nil）：JSON 出 []，前端 for..of 不炸（M6 教学点复习）
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.ID, &a.Name, &a.Role, &a.Description, &a.Engine, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("读取成员行失败: %w", err)
		}
		members = append(members, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历成员失败: %w", err)
	}
	return members, nil
}

// GetMessageByTask 按任务 id 找触发它的消息。
// agent 完成任务后用它反查"往哪个会话回消息"。M8 起从 message_tasks 关联表反查
// （任务可能被多条消息/多个 agent 关联，但"触发它"的那条 user 消息是唯一可反查的目标）。
func (s *Store) GetMessageByTask(ctx context.Context, issueID string) (*Message, error) {
	var m Message
	var taskID sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT m.id, m.conversation_id, m.sender_type, m.sender_id, m.content, m.task_id, m.created_at
		 FROM message_tasks mt
		 JOIN messages m ON m.id = mt.message_id
		 WHERE mt.task_id = ?
		 ORDER BY m.created_at LIMIT 1`, issueID,
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

// SendMessageWithTasks 往会话里发一条消息，并把一组任务关联到这条消息。
// 与 SendMessage + AttachTasks 两步分开写不同，这里用事务保证"消息 + 任务关联"原子：
// 要么都成，要么都回滚——避免 agent 完成后反查不到来源消息。
func (s *Store) SendMessageWithTasks(ctx context.Context, conversationID, senderType, senderID, content string, taskIDs []string) (*Message, error) {
	m := &Message{
		ID:             NewID(),
		ConversationID: conversationID,
		SenderType:     senderType,
		SenderID:       senderID,
		Content:        content,
		CreatedAt:      time.Now(),
	}
	if len(taskIDs) > 0 {
		m.TaskID = taskIDs[0] // 兼容旧逻辑：messages.task_id 存第一个主任务
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


// AttachTasks 把一组任务关联到一条消息（写 message_tasks 关联表）。
// 场景：一条消息 @ 多人 → 触发多个任务，每个任务都要能反查到这条消息。
// OR IGNORE：组合主键已存在就跳过（重复挂同一个任务不报错），幂等。
func (s *Store) AttachTasks(ctx context.Context, messageID string, taskIDs []string) error {
	for _, taskID := range taskIDs {
		if _, err := s.db.ExecContext(ctx,
			`INSERT OR IGNORE INTO message_tasks (message_id, task_id) VALUES (?, ?)`,
			messageID, taskID); err != nil {
			return fmt.Errorf("关联任务失败: %w", err)
		}
	}
	return nil
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

	msgs := []Message{}
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

// ListMessagesAfter 取一个会话中在 afterID 之后的新消息（增量拉取用）。
// 没有 afterID 时退化为全量 ListMessages。
func (s *Store) ListMessagesAfter(ctx context.Context, conversationID, afterID string) ([]Message, error) {
	if afterID == "" {
		return s.ListMessages(ctx, conversationID)
	}

	var exists int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM messages WHERE id = ?`, afterID,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		// afterID 不存在（比如会话被重置），退回全量拉取，前端会重建消息流。
		return s.ListMessages(ctx, conversationID)
	}
	if err != nil {
		return nil, fmt.Errorf("查询增量游标失败: %w", err)
	}

	// 用 ULID 字符串做游标，避免 created_at 的单调时钟（m=+...）在 SQLite 里
	// 往返后比较不一致的问题。ULID 本身按时间有序，id > ? 即可拿到游标之后的新消息。
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, conversation_id, sender_type, sender_id, content, task_id, created_at
		 FROM messages
		 WHERE conversation_id = ? AND id > ?
		 ORDER BY created_at, id`,
		conversationID, afterID,
	)
	if err != nil {
		return nil, fmt.Errorf("查询增量消息失败: %w", err)
	}
	defer rows.Close()

	msgs := []Message{}
	for rows.Next() {
		var m Message
		var taskID sql.NullString
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.SenderType, &m.SenderID, &m.Content, &taskID, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("读取增量消息失败: %w", err)
		}
		if taskID.Valid {
			m.TaskID = taskID.String
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历增量消息失败: %w", err)
	}
	return msgs, nil
}


// GetConversationContext 取"某任务来源会话"的最近 limit 条消息，给 agent 当干活背景。
// 链路：任务 id → message_tasks 反查触发消息 → 拿到会话 id → 取该会话最近 limit 条。
// 任务不是消息触发的（API 直接建）→ 返回空 slice，没有会话背景可注入。
func (s *Store) GetConversationContext(ctx context.Context, issueID string, limit int) ([]Message, error) {
	// ① 反查来源会话（拿到 conversation_id）
	src, err := s.GetMessageByTask(ctx, issueID)
	if err != nil {
		return nil, err
	}
	if src == nil {
		return []Message{}, nil // 任务不是消息来的，没有背景
	}

	// ② 取最近 limit 条。"最近 N 条"的经典套路：
	//    DESC（时间倒序，新的在前）→ LIMIT N 截取 → 再反转回"旧的在前"。
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, conversation_id, sender_type, sender_id, content, task_id, created_at
		 FROM messages WHERE conversation_id = ?
		 ORDER BY created_at DESC LIMIT ?`, src.ConversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("查询会话背景失败: %w", err)
	}
	defer rows.Close()

	msgs := []Message{}
	for rows.Next() {
		var m Message
		var taskID sql.NullString
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.SenderType, &m.SenderID, &m.Content, &taskID, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("读取背景消息失败: %w", err)
		}
		if taskID.Valid {
			m.TaskID = taskID.String
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历背景消息失败: %w", err)
	}

	// ③ 反转顺序（双指针从两端往中间走，逐个交换）：DESC 取出来新的在前，
	//    倒回"旧的在前"，prompt 里对话才顺（就像人从前往后读聊天记录）
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
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

	// 事务：任务 + 入队必须原子完成。
	// 否则会出现"任务写进 issues，但 task_queue 没写进去"的半截状态——worker 永远领不到。
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx,
		`INSERT INTO issues (id, title, description, status, assignee_type, assignee_id, depends_on, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		issue.ID, issue.Title, issue.Description, issue.Status, issue.AssigneeType, issue.AssigneeID, dependsOnArg, issue.CreatedAt, issue.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("创建任务失败: %w", err)
	}

	// 任务建好，自动入队：让 worker 能领到这份活
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

// MaxAttempts 一条任务最多尝试执行几次；超过就上报 blocked 等人类介入。
// 为什么设上限？每次重试 = 再调一次 claude = 再花一次 token。技术性失败重试几次合理，
// 但无限重试只会无限烧钱——所以到点上报，让人看看到底卡在哪。
const MaxAttempts = 3

// QueuedTask 是一次认领到手的"活"：worker 凭它干活，并凭 ClaimToken 汇报结果
type QueuedTask struct {
	ID         string // 队列项 id（task_queue.id）
	IssueID    string // 对应哪条任务（issues.id）
	ClaimToken string // 认领凭证：只有持有它才能标记完成/失败
	Attempts   int    // 已经尝试过几次（M5：重试/上报 的分界依据）
}

// ClaimTask 尝试认领一条 queued 任务。
// assigneeID 是"我是谁"：只认领派给我的任务。
// 认领成功返回任务；队列空或没抢到返回 (nil, nil)；出错返回错误。
func (s *Store) ClaimTask(ctx context.Context, workerID, assigneeID string) (*QueuedTask, error) {
	// 第一步：挑——从"派给我"的任务里找排队最久的一条
	// 注意：assignee_id 在 issues 表，不在 task_queue，所以要 JOIN 连表查
	// 依赖过滤（协作任务）：没依赖直接放行；有依赖必须等上游 completed
	var id, issueID string
	var attempts int // M5：顺带读出"已尝试几次"，FailTask 判断重试/上报要用
	err := s.db.QueryRowContext(ctx,
		`SELECT t.id, t.issue_id, t.attempts FROM task_queue t
		 JOIN issues i ON i.id = t.issue_id
		 WHERE t.status = 'queued' AND i.assignee_id = ?
		   AND (i.depends_on IS NULL
		        OR EXISTS (SELECT 1 FROM task_queue d
		                   WHERE d.issue_id = i.depends_on AND d.status = 'completed'))
		 ORDER BY t.created_at LIMIT 1`,
		assigneeID,
	).Scan(&id, &issueID, &attempts)
	if err == sql.ErrNoRows {
		return nil, nil // 没有派给我的活
	}
	if err != nil {
		return nil, fmt.Errorf("查找待认领任务失败: %w", err)
	}

	// 第二步：抢——乐观锁，把"抢 + 贴标签"合并成一条 SQL。
	// 同时写 dispatched_at：认领时间，孤儿回收用（进程在认领后、开工前被杀也能被发现）。
	claimToken := NewID() // 本次认领的凭证，完成/失败时要用它对账
	res, err := s.db.ExecContext(ctx,
		`UPDATE task_queue
		 SET status = 'dispatched', claim_token = ?, worker_id = ?, dispatched_at = ?
		 WHERE id = ? AND status = 'queued'`,
		claimToken, workerID, time.Now(), id,
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

	return &QueuedTask{ID: id, IssueID: issueID, ClaimToken: claimToken, Attempts: attempts}, nil
}

// CompleteTask 凭认领凭证，把任务标记为已完成。
// 凭证不匹配（或任务已被处理）时返回错误——这就是"幂等"：重复调用不会重复搞坏状态。
// 同时把 issues.status 同步成 done（业务视图与流水线状态一致）。
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

// FailTask 凭认领凭证标记任务失败，并自动处理"重试 or 上报"。
// 返回字符串是任务的最终状态：
//   "queued"  → 这次失败还能重试（attempts+1 还没到 MaxAttempts），已回退排队
//   "blocked" → 重试耗尽，需要人类介入
//
// 一条 UPDATE 原子完成三件事：
//   1) attempts = attempts + 1                          计数自增
//   2) CASE WHEN 决定这次失败去 queued（回退重试）还是 blocked（上报人）
//   3) 回退时把 claim_token / worker_id 清成 NULL       旧凭证作废，重新排队等再认领
//
// 为什么不用"Go 先查 attempts，再决定回退 or 上报"两段式？
// 两段式中间可能有别的进程插入写，计数就失真了。CASE WHEN 把"还有没有重试额度"
// 的判断下沉到 SQL，计数 + 分支在一条 UPDATE 里原子完成——让数据库帮你做决策。
// 注意：Go 里计算返回状态用的判断和 SQL 里的 CASE 是同一句，改动必须两边同步。
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
		     dispatched_at = NULL,
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

	// 算返回状态：和 SQL 的 CASE 同一句判断（attempts+1 达到上限 → blocked）
	finalStatus := "queued"
	issueStatus := "in_progress" // 回退重试：任务还在处理流程中
	if task.Attempts+1 >= MaxAttempts {
		finalStatus = "blocked"
		issueStatus = "blocked" // 重试耗尽：需要人介入
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

// BlockTask 把任务标记为 blocked：需要人类介入，不再重试。
// 两个来源（M5）：① claude 主动回答"我无法完成：<原因>"，② 重试耗尽。
// 注意和 FailTask 的区别——FailTask 会先试着重试；BlockTask 是"这个重试也没意义，
// 直接上报"，比如 claude 明说自己做不到（再重试 = 再烧一次 token 得到同样答案）。
// reason 是给人类看的说明，存在 error 列里。
func (s *Store) BlockTask(ctx context.Context, task *QueuedTask, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`UPDATE task_queue
		 SET status = 'blocked', error = ?, finished_at = ?
		 WHERE id = ? AND claim_token = ? AND status IN ('dispatched', 'running')`,
		reason, time.Now(), task.ID, task.ClaimToken,
	)
	if err != nil {
		return fmt.Errorf("标记 blocked 失败: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取 blocked 结果失败: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("标记 blocked 失败: 凭证不匹配或任务已被处理 (id=%s)", task.ID)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE issues SET status = 'blocked', updated_at = ? WHERE id = ?`,
		time.Now(), task.IssueID,
	); err != nil {
		return fmt.Errorf("同步任务状态失败: %w", err)
	}

	return tx.Commit()
}

// RequeueStaleTasks 回收孤儿任务：agent 进程在认领后或开工后被杀，任务会卡在
// dispatched/running。这里把它们回退到 queued，让其他（或重启后的）agent 可以重新认领。
// 两类孤儿分别处理：
//   - running：已开工，用 started_at 判断是否超时。
//   - dispatched 且 started_at IS NULL：认领后从未开工，用 dispatched_at 判断是否超时。
//
// 返回值是本次回退的任务总数。
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

	// 类型 2：已认领但一直没开工（旧方案只认 started_at，这类任务永远卡死）
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


// CascadeBlock 上游 failed/blocked 时，把依赖它的下游（还在 queued 排队的）级联标成 blocked。
// 为什么需要：认领有 EXISTS 检查（上游必须 completed 才能领）。上游卡死了，
// 下游永远等不到，会静默卡在 queued 没人知道。级联把它变成显式的 blocked + reason 上报人。
//
// 级联会层层传导（A 依赖 B、B 依赖 C；C 卡死 → B 被级联 blocked → A 也要被级联），
// 所以是"逐层循环"：处理完一层，拿到的下游当下一层的"上游"，直到某层没有新下游为止。
// 环（A↔B 互相依赖）不会死循环：被标过 blocked 的下游 status 不再是 'queued'，下一轮 WHERE 不命中。
//
// 返回被级联标成 blocked 的总数。
func (s *Store) CascadeBlock(ctx context.Context, upstreamIssueID string, reason string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	var total int64
	layer := []string{upstreamIssueID} // 这一层要检查"谁依赖它们"
	for len(layer) > 0 {
		var next []string
		for _, up := range layer {
			// UPDATE + RETURNING：把"标 blocked"和"拿到被标的任务 id"合进一条 SQL。
			// RETURNING 是 SQLite 3.35+ 的语法：UPDATE 也能返回被更新行的列。
			// 不 RETURNING 的话，得先 SELECT 再 UPDATE 两段式，中间可能被别的进程插一刀。
			rows, err := tx.QueryContext(ctx,
				`UPDATE task_queue
				 SET status = 'blocked', error = ?, finished_at = ?
				 WHERE status = 'queued'
				   AND issue_id IN (SELECT id FROM issues WHERE depends_on = ?)
				 RETURNING issue_id`,
				reason, time.Now(), up,
			)
			if err != nil {
				return total, fmt.Errorf("级联 blocked 失败: %w", err)
			}
			for rows.Next() {
				var issueID string
				if err := rows.Scan(&issueID); err != nil {
					rows.Close()
					return total, fmt.Errorf("读取级联结果失败: %w", err)
				}
				next = append(next, issueID) // 这批新 blocked 的下游，作为下一层的"上游"
				total++
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return total, fmt.Errorf("遍历级联结果失败: %w", err)
			}
		}

		// 同步 issues.status：级联 blocked 也是业务状态变更，不能让 API 继续看到 todo。
		for _, issueID := range next {
			if _, err := tx.ExecContext(ctx,
				`UPDATE issues SET status = 'blocked', updated_at = ? WHERE id = ?`,
				time.Now(), issueID,
			); err != nil {
				return total, fmt.Errorf("同步级联任务状态失败: %w", err)
			}
		}

		layer = next // 继续往下一层找
	}

	if err := tx.Commit(); err != nil {
		return total, fmt.Errorf("提交事务失败: %w", err)
	}
	return total, nil
}

// StartTask 标记任务"正式开始执行"：status → running，并记录开始时间。
// 同时把 issues.status 同步成 in_progress（业务视图不再是 todo）。
func (s *Store) StartTask(ctx context.Context, task *QueuedTask) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
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

	if _, err := tx.ExecContext(ctx,
		`UPDATE issues SET status = 'in_progress', updated_at = ? WHERE id = ?`,
		time.Now(), task.IssueID,
	); err != nil {
		return fmt.Errorf("同步任务状态失败: %w", err)
	}

	return tx.Commit()
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

	items := []QueueItem{}
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

	runs := []RunRecord{}
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

	issues := []Issue{}
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

// AgentStatus 一个 agent 的当前状态（前端状态灯用）
type AgentStatus struct {
	ID     string
	Name   string
	Role   string
	Status string // running | blocked | idle
}

// TeamStatus 返回每个 agent 的当前状态（前端"团队状态灯"轮询用）。
// status 看该 agent 最新一条"还没结束"的任务：
//   running/dispatched → 在干活（绿灯）
//   blocked            → 卡住了（红灯）
//   都没有             → 空闲（灰灯）
// 教学点一（相关子查询）：子查询里引用外层 a.id——"这个 agent 的最新任务状态是什么"。
// 教学点二（COALESCE 兜底）：子查询查不到行时返回 NULL（agent 从没干过活），
//   COALESCE 把它替换成默认值 'idle'——NULL 不会"漏到"前端。
func (s *Store) TeamStatus(ctx context.Context) ([]AgentStatus, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.id, a.name, a.role,
		        COALESCE((SELECT t.status FROM task_queue t
		                  JOIN issues i ON i.id = t.issue_id
		                  WHERE i.assignee_id = a.id
		                    AND t.status IN ('running', 'dispatched', 'blocked')
		                  ORDER BY t.created_at DESC LIMIT 1), 'idle') AS status
		 FROM agents a ORDER BY a.created_at`)
	if err != nil {
		return nil, fmt.Errorf("查询团队状态失败: %w", err)
	}
	defer rows.Close()

	agents := []AgentStatus{}
	for rows.Next() {
		var a AgentStatus
		if err := rows.Scan(&a.ID, &a.Name, &a.Role, &a.Status); err != nil {
			return nil, fmt.Errorf("读取团队状态失败: %w", err)
		}
		agents = append(agents, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历团队状态失败: %w", err)
	}
	return agents, nil
}

// Memory 代表一条知识库记录（M7 双层知识库）。
// scope=team 时 AgentID 为空；scope=agent 时 AgentID 指向 agents.id。
type Memory struct {
	ID        string
	Scope     string // team | agent
	AgentID   string // 可空；team 知识为空
	Kind      string // doc | code
	Content   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CaptureMemory 写入一条知识：团队共享（scope=team）或个人私有（scope=agent）。
// 相同 (scope, agent_id, kind, content) 会新增一条，保留历史版本。
func (s *Store) CaptureMemory(ctx context.Context, scope, agentID, kind, content string) (*Memory, error) {
	now := time.Now()
	m := &Memory{
		ID:        NewID(),
		Scope:     scope,
		AgentID:   agentID,
		Kind:      kind,
		Content:   content,
		CreatedAt: now,
		UpdatedAt: now,
	}

	var agentArg any
	if agentID != "" {
		agentArg = agentID
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memory (id, scope, agent_id, kind, content, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.Scope, agentArg, m.Kind, m.Content, m.CreatedAt, m.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("写入知识失败: %w", err)
	}
	return m, nil
}

// RecallMemory 按作用域读取知识：scope=team 时 agentID 传空；
// scope=agent 时 agentID 传员工 id。返回按时间从旧到新排序。
func (s *Store) RecallMemory(ctx context.Context, scope, agentID string) ([]Memory, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, scope, agent_id, kind, content, created_at, updated_at
		 FROM memory
		 WHERE scope = ? AND (agent_id = ? OR (? = '' AND agent_id IS NULL))
		 ORDER BY created_at, id`,
		scope, agentID, agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("查询知识失败: %w", err)
	}
	defer rows.Close()

	mems := []Memory{}
	for rows.Next() {
		var m Memory
		var agentIDVal sql.NullString
		if err := rows.Scan(&m.ID, &m.Scope, &agentIDVal, &m.Kind, &m.Content, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("读取知识行失败: %w", err)
		}
		if agentIDVal.Valid {
			m.AgentID = agentIDVal.String
		}
		mems = append(mems, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历知识失败: %w", err)
	}
	return mems, nil
}

// RecallMemoryForAgent 取一个 agent 干活前要注入的全部知识：
// 先取团队共享，再取个人私有，合并成一段文本。
func (s *Store) RecallMemoryForAgent(ctx context.Context, agentID string) (string, error) {
	var sb strings.Builder

	teamMems, err := s.RecallMemory(ctx, "team", "")
	if err != nil {
		return "", err
	}
	for _, m := range teamMems {
		sb.WriteString("【团队知识·" + m.Kind + "】\n" + m.Content + "\n\n")
	}

	agentMems, err := s.RecallMemory(ctx, "agent", agentID)
	if err != nil {
		return "", err
	}
	for _, m := range agentMems {
		sb.WriteString("【个人知识·" + m.Kind + "】\n" + m.Content + "\n\n")
	}

	return sb.String(), nil
}

