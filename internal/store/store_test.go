package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// TestCreateIssue 验证：创建一条任务，能写进数据库并返回完整数据
func TestCreateIssue(t *testing.T) {
	// t.TempDir()：自动创建独立临时目录，测试结束自动清理（比手动删文件可靠）
	path := filepath.Join(t.TempDir(), "test.db")

	// 1. 打开数据库（会自动建表）
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close() // 用完必须关，否则文件被占用，临时目录清理不掉

	// 1.5 先入职一名员工（任务必须派给具体的人）
	agent, err := s.CreateAgent(context.Background(), "小王", "前端工程师", "负责页面", "claude")
	if err != nil {
		t.Fatalf("CreateAgent 失败: %v", err)
	}

	// 2. 创建一条任务，派给小 王（无依赖，最后一个参数传空串）
	issue, err := s.CreateIssue(context.Background(), "学会 Go", "第一条测试任务", agent.ID, "")
	if err != nil {
		t.Fatalf("CreateIssue 失败: %v", err)
	}

	// 3. 逐项检查返回的数据是否符合预期
	if issue.ID == "" {
		t.Fatal("id 是空的，应该自动生成")
	}
	if issue.Status != "todo" {
		t.Fatalf("状态应该是 todo，实际是 %s", issue.Status)
	}
	if issue.Title != "学会 Go" {
		t.Fatalf("标题应该是「学会 Go」，实际是 %s", issue.Title)
	}
	if issue.AssigneeID != agent.ID {
		t.Fatalf("指派人不匹配: 应=%s 实=%s", agent.ID, issue.AssigneeID)
	}

	t.Logf("✅ 创建成功！id=%s, 状态=%s", issue.ID, issue.Status)
}

// TestConversationAndMessages 验证：建会话 → 拉 agent → 发消息 → 取消息流
func TestConversationAndMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	// 1. 先有员工，才能拉进会话
	agent, err := s.CreateAgent(context.Background(), "小王", "前端工程师", "负责页面", "fake")
	if err != nil {
		t.Fatalf("CreateAgent 失败: %v", err)
	}

	// 2. 建单聊会话，把小王拉进来（人类是隐式所有者，不建成员行）
	conv, err := s.CreateConversation(context.Background(), "和小王的私聊", "direct", []string{agent.ID})
	if err != nil {
		t.Fatalf("CreateConversation 失败: %v", err)
	}
	if conv.ID == "" {
		t.Fatal("会话 id 是空的，应该自动生成")
	}
	if conv.Type != "direct" {
		t.Fatalf("类型应该是 direct，实际是 %s", conv.Type)
	}

	// 3. 人类发一条消息（暂不关联任务，taskID 传空串）
	m, err := s.SendMessage(context.Background(), conv.ID, "user", "me", "@小王 帮我写登录页", "")
	if err != nil {
		t.Fatalf("SendMessage 失败: %v", err)
	}
	if m.Content != "@小王 帮我写登录页" {
		t.Fatalf("消息内容不符: %s", m.Content)
	}

	// 4. 取消息流：应该正好 1 条，内容一致
	msgs, err := s.ListMessages(context.Background(), conv.ID)
	if err != nil {
		t.Fatalf("ListMessages 失败: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("消息数应为 1，实际 %d", len(msgs))
	}
	if msgs[0].SenderType != "user" || msgs[0].Content != "@小王 帮我写登录页" {
		t.Fatalf("消息内容不符: %+v", msgs[0])
	}

	t.Logf("✅ 会话+消息闭环通过！会话=%s, 消息=%s", conv.Name, m.Content)
}

// TestFailRetryThenBlock 验证 M5 重试/上报状态机：
// 失败 3 次（MaxAttempts）内自动回退 queued 重试，第 3 次耗尽 → blocked 等人类。
func TestFailRetryThenBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	// 1. 入职员工 + 建一条无依赖任务
	agent, err := s.CreateAgent(context.Background(), "小王", "前端工程师", "负责页面", "fake")
	if err != nil {
		t.Fatalf("CreateAgent 失败: %v", err)
	}
	if _, err := s.CreateIssue(context.Background(), "会失败的活", "试试重试", agent.ID, ""); err != nil {
		t.Fatalf("CreateIssue 失败: %v", err)
	}

	ctx := context.Background()

	// 2. 模拟一个"每次认领都失败"的 worker：
	//    认领 → 开工 → 失败，走 3 轮（前 2 次回退重试 + 第 3 次耗尽上报）
	for i := 1; i <= MaxAttempts; i++ {
		task, err := s.ClaimTask(ctx, agent.ID, agent.ID)
		if err != nil {
			t.Fatalf("第 %d 次认领失败: %v", i, err)
		}
		if task == nil {
			t.Fatalf("第 %d 次认领：没有任务可领（前面失败没回退？）", i)
		}
		if err := s.StartTask(ctx, task); err != nil {
			t.Fatalf("第 %d 次开工失败: %v", i, err)
		}

		// 每次失败带不同原因，方便最后核对 error 存的是哪次
		finalStatus, err := s.FailTask(ctx, task, fmt.Sprintf("第 %d 次失败", i))
		if err != nil {
			t.Fatalf("第 %d 次 FailTask 失败: %v", i, err)
		}

		// 前 2 次应回退 queued 重试，第 3 次耗尽应上报 blocked
		want := "queued"
		if i == MaxAttempts {
			want = "blocked"
		}
		if finalStatus != want {
			t.Fatalf("第 %d 次失败：最终状态应=%s 实=%s", i, want, finalStatus)
		}
	}

	// 3. 查队列项：attempts 应累计到 MaxAttempts，状态 blocked，error 是最后一次原因
	items, err := s.ListQueue(ctx)
	if err != nil {
		t.Fatalf("ListQueue 失败: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("队列应有 1 条，实际 %d", len(items))
	}
	it := items[0]
	if it.Attempts != MaxAttempts {
		t.Fatalf("attempts 应为 %d，实际 %d", MaxAttempts, it.Attempts)
	}
	if it.Status != "blocked" {
		t.Fatalf("状态应为 blocked，实际 %s", it.Status)
	}
	if !it.Error.Valid || it.Error.String != fmt.Sprintf("第 %d 次失败", MaxAttempts) {
		t.Fatalf("error 应记录最后一次原因，实际 %+v", it.Error)
	}

	// 4. blocked 之后不会再被认领（等价于"等人类介入"，不会自己又跑起来）
	again, err := s.ClaimTask(ctx, agent.ID, agent.ID)
	if err != nil {
		t.Fatalf("再次认领出错: %v", err)
	}
	if again != nil {
		t.Fatalf("blocked 任务不应被再认领，实际领到了 %+v", again)
	}

	t.Logf("✅ 重试/上报状态机通过：%d 次失败 → 耗尽 → blocked，且不再被认领", MaxAttempts)
}

// TestBlockTaskDirect 验证 BlockTask：引擎说"我无法完成"时直接上报，不走重试
func TestBlockTaskDirect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	agent, err := s.CreateAgent(context.Background(), "小赵", "测试工程师", "负责测试", "fake")
	if err != nil {
		t.Fatalf("CreateAgent 失败: %v", err)
	}
	if _, err := s.CreateIssue(context.Background(), "我干不了", "需要人类", agent.ID, ""); err != nil {
		t.Fatalf("CreateIssue 失败: %v", err)
	}

	ctx := context.Background()
	task, err := s.ClaimTask(ctx, agent.ID, agent.ID)
	if err != nil {
		t.Fatalf("认领失败: %v", err)
	}
	if task == nil {
		t.Fatal("没有任务可领")
	}
	if err := s.StartTask(ctx, task); err != nil {
		t.Fatalf("开工失败: %v", err)
	}

	// 引擎说"我无法完成：缺数据库权限"，直接上报（attempts 不涨，不重试）
	if err := s.BlockTask(ctx, task, "我无法完成：缺数据库权限"); err != nil {
		t.Fatalf("BlockTask 失败: %v", err)
	}

	items, err := s.ListQueue(ctx)
	if err != nil {
		t.Fatalf("ListQueue 失败: %v", err)
	}
	if items[0].Status != "blocked" {
		t.Fatalf("状态应为 blocked，实际 %s", items[0].Status)
	}
	if items[0].Attempts != 0 {
		t.Fatalf("BlockTask 不应涨 attempts，实际 %d", items[0].Attempts)
	}
	if !items[0].Error.Valid || items[0].Error.String != "我无法完成：缺数据库权限" {
		t.Fatalf("error 应存 reason，实际 %+v", items[0].Error)
	}

	t.Logf("✅ BlockTask 通过：直接上报、attempts 不涨、reason 落库")
}

// TestCascadeBlock 验证协作异常互锁：
// C 卡死 → 依赖它的 B 被级联 blocked → 依赖 B 的 A 也被级联（层层传导）；
// 无关任务 X 不受影响；重复调用幂等（第二次没有新的 queued 下游可标）。
func TestCascadeBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	ctx := context.Background()

	// 1. 员工 + 一条依赖链 C ← B ← A（A 依赖 B，B 依赖 C），再加一条无依赖的 X
	agent, err := s.CreateAgent(ctx, "小王", "前端工程师", "负责页面", "fake")
	if err != nil {
		t.Fatalf("CreateAgent 失败: %v", err)
	}
	c, err := s.CreateIssue(ctx, "C 任务", "最上游", agent.ID, "")
	if err != nil {
		t.Fatalf("创建 C 失败: %v", err)
	}
	b, err := s.CreateIssue(ctx, "B 任务", "依赖 C", agent.ID, c.ID)
	if err != nil {
		t.Fatalf("创建 B 失败: %v", err)
	}
	a, err := s.CreateIssue(ctx, "A 任务", "依赖 B", agent.ID, b.ID)
	if err != nil {
		t.Fatalf("创建 A 失败: %v", err)
	}
	x, err := s.CreateIssue(ctx, "X 任务", "无依赖", agent.ID, "")
	if err != nil {
		t.Fatalf("创建 X 失败: %v", err)
	}

	// 2. C 卡死了（比如重试耗尽），级联它
	n, err := s.CascadeBlock(ctx, c.ID, "上游 C 已 blocked，无法完成依赖")
	if err != nil {
		t.Fatalf("CascadeBlock 失败: %v", err)
	}
	if n != 2 { // 应该级联 B 和 A 两个下游
		t.Fatalf("级联数量应为 2，实际 %d", n)
	}

	// 3. 检查：B、A 变 blocked 并带 reason，X 仍是 queued
	queueStatus := map[string]string{}
	items, err := s.ListQueue(ctx)
	if err != nil {
		t.Fatalf("ListQueue 失败: %v", err)
	}
	for _, it := range items {
		queueStatus[it.IssueID] = it.Status
	}
	if queueStatus[b.ID] != "blocked" {
		t.Fatalf("B 应为 blocked，实际 %s", queueStatus[b.ID])
	}
	if queueStatus[a.ID] != "blocked" {
		t.Fatalf("A 应为 blocked（层层传导），实际 %s", queueStatus[a.ID])
	}
	if queueStatus[x.ID] != "queued" {
		t.Fatalf("X 无依赖不应被级联，实际 %s", queueStatus[x.ID])
	}

	// 4. 幂等：再级联一次，没有新的 queued 下游，返回 0
	n2, err := s.CascadeBlock(ctx, c.ID, "上游 C 已 blocked")
	if err != nil {
		t.Fatalf("再次 CascadeBlock 失败: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("幂等性：第二次级联应为 0，实际 %d", n2)
	}

	t.Logf("✅ 级联互锁通过：C→B→A 层层传导 2 个，X 不受影响，重复调用幂等")
}

// TestTeamStatusAndConversations 验证 M6 的两个新查询：
// ① ListConversationsWithMembers 返回带成员的会话；② TeamStatus 聚合每个 agent 状态。
func TestTeamStatusAndConversations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	w, err := s.CreateAgent(ctx, "小王", "前端工程师", "负责页面", "fake")
	if err != nil {
		t.Fatalf("创建小王失败: %v", err)
	}
	l, err := s.CreateAgent(ctx, "小李", "后端工程师", "负责接口", "fake")
	if err != nil {
		t.Fatalf("创建小李失败: %v", err)
	}

	// 1. 建群聊，拉两位进来 → 会话应带成员名
	if _, err := s.CreateConversation(ctx, "项目组", "group", []string{w.ID, l.ID}); err != nil {
		t.Fatalf("建会话失败: %v", err)
	}
	convs, err := s.ListConversationsWithMembers(ctx)
	if err != nil {
		t.Fatalf("ListConversationsWithMembers 失败: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("会话数应为 1，实际 %d", len(convs))
	}
	if len(convs[0].Members) != 2 || convs[0].Members[0] != "小王" || convs[0].Members[1] != "小李" {
		t.Fatalf("成员应为 [小王 小李]，实际 %v", convs[0].Members)
	}

	// 2. 小王开工（认领+开始→running）；小李没任务 → idle
	if _, err := s.CreateIssue(ctx, "小王的任务", "", w.ID, ""); err != nil {
		t.Fatalf("建任务失败: %v", err)
	}
	task, err := s.ClaimTask(ctx, w.ID, w.ID)
	if err != nil {
		t.Fatalf("认领失败: %v", err)
	}
	if task == nil {
		t.Fatal("没有任务可领")
	}
	if err := s.StartTask(ctx, task); err != nil {
		t.Fatalf("开工失败: %v", err)
	}

	// 3. TeamStatus：小王 running（dispatch 后 running）、小李 idle
	statuses, err := s.TeamStatus(ctx)
	if err != nil {
		t.Fatalf("TeamStatus 失败: %v", err)
	}
	byName := map[string]string{}
	for _, st := range statuses {
		byName[st.Name] = st.Status
	}
	if byName["小王"] != "running" {
		t.Fatalf("小王状态应为 running，实际 %s", byName["小王"])
	}
	if byName["小李"] != "idle" {
		t.Fatalf("小李状态应为 idle，实际 %s", byName["小李"])
	}

	t.Logf("✅ 会话带成员 + 团队状态聚合通过")
}

// TestSendMessageWithTasks 验证发消息 + 任务关联在同一个事务里完成。
func TestSendMessageWithTasks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	agent, err := s.CreateAgent(ctx, "小王", "前端工程师", "负责页面", "fake")
	if err != nil {
		t.Fatalf("CreateAgent 失败: %v", err)
	}
	conv, err := s.CreateConversation(ctx, "和小王的私聊", "direct", []string{agent.ID})
	if err != nil {
		t.Fatalf("CreateConversation 失败: %v", err)
	}
	issue, err := s.CreateIssue(ctx, "消息触发的任务", "", agent.ID, "")
	if err != nil {
		t.Fatalf("CreateIssue 失败: %v", err)
	}

	msg, err := s.SendMessageWithTasks(ctx, conv.ID, "user", "me", "@小王 帮我干活", []string{issue.ID})
	if err != nil {
		t.Fatalf("SendMessageWithTasks 失败: %v", err)
	}
	if msg.ID == "" {
		t.Fatal("消息 id 不应为空")
	}

	src, err := s.GetMessageByTask(ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetMessageByTask 失败: %v", err)
	}
	if src == nil || src.ID != msg.ID {
		t.Fatalf("任务应能反查来源消息，实际 src=%+v", src)
	}

	t.Logf("✅ SendMessageWithTasks 通过：消息与任务关联可反查")
}


// TestIssueStatusSync 验证 issues.status 会随流水线同步：
// 创建 todo → 开工 in_progress → 完成 done。
func TestIssueStatusSync(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	agent, err := s.CreateAgent(ctx, "小王", "前端工程师", "负责页面", "fake")
	if err != nil {
		t.Fatalf("CreateAgent 失败: %v", err)
	}
	issue, err := s.CreateIssue(ctx, "状态同步", "", agent.ID, "")
	if err != nil {
		t.Fatalf("CreateIssue 失败: %v", err)
	}

	task, err := s.ClaimTask(ctx, agent.ID, agent.ID)
	if err != nil {
		t.Fatalf("ClaimTask 失败: %v", err)
	}
	if task == nil {
		t.Fatal("没有任务可领")
	}
	if err := s.StartTask(ctx, task); err != nil {
		t.Fatalf("StartTask 失败: %v", err)
	}

	issues, err := s.ListIssues(ctx)
	if err != nil {
		t.Fatalf("ListIssues 失败: %v", err)
	}
	if len(issues) != 1 || issues[0].Status != "in_progress" {
		t.Fatalf("开工后 issues.status 应为 in_progress，实际 %+v", issues)
	}

	if err := s.CompleteTask(ctx, task); err != nil {
		t.Fatalf("CompleteTask 失败: %v", err)
	}
	issues, err = s.ListIssues(ctx)
	if err != nil {
		t.Fatalf("ListIssues 失败: %v", err)
	}
	if len(issues) != 1 || issues[0].Status != "done" {
		t.Fatalf("完成后 issues.status 应为 done，实际 %+v", issues)
	}

	_ = issue
	t.Logf("✅ issues.status 同步通过：todo → in_progress → done")
}

// TestBlockTaskSync 验证 BlockTask 会把 issues.status 同步成 blocked。
func TestBlockTaskSync(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	agent, err := s.CreateAgent(ctx, "小赵", "测试工程师", "负责测试", "fake")
	if err != nil {
		t.Fatalf("CreateAgent 失败: %v", err)
	}
	if _, err := s.CreateIssue(ctx, "我干不了", "需要人类", agent.ID, ""); err != nil {
		t.Fatalf("CreateIssue 失败: %v", err)
	}

	task, err := s.ClaimTask(ctx, agent.ID, agent.ID)
	if err != nil {
		t.Fatalf("ClaimTask 失败: %v", err)
	}
	if task == nil {
		t.Fatal("没有任务可领")
	}
	if err := s.StartTask(ctx, task); err != nil {
		t.Fatalf("StartTask 失败: %v", err)
	}
	if err := s.BlockTask(ctx, task, "我无法完成：缺数据库权限"); err != nil {
		t.Fatalf("BlockTask 失败: %v", err)
	}

	issues, err := s.ListIssues(ctx)
	if err != nil {
		t.Fatalf("ListIssues 失败: %v", err)
	}
	if len(issues) != 1 || issues[0].Status != "blocked" {
		t.Fatalf("blocked 后 issues.status 应为 blocked，实际 %+v", issues)
	}

	t.Logf("✅ BlockTask 同步通过：issues.status = blocked")
}

// TestRequeueStaleTasks 验证孤儿任务回收：
// dispatched 且 dispatched_at 超时、started_at 为空的任务应回退 queued。
func TestRequeueStaleTasks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	agent, err := s.CreateAgent(ctx, "小王", "前端工程师", "负责页面", "fake")
	if err != nil {
		t.Fatalf("CreateAgent 失败: %v", err)
	}
	if _, err := s.CreateIssue(ctx, "孤儿任务", "", agent.ID, ""); err != nil {
		t.Fatalf("CreateIssue 失败: %v", err)
	}

	task, err := s.ClaimTask(ctx, agent.ID, agent.ID)
	if err != nil {
		t.Fatalf("ClaimTask 失败: %v", err)
	}
	if task == nil {
		t.Fatal("没有任务可领")
	}

	// 模拟：认领后进程被杀，dispatched_at 已超时，但 started_at 一直为空
	if _, err := s.db.ExecContext(ctx,
		`UPDATE task_queue SET dispatched_at = ? WHERE id = ?`,
		time.Now().Add(-20*time.Minute), task.ID,
	); err != nil {
		t.Fatalf("更新 dispatched_at 失败: %v", err)
	}

	n, err := s.RequeueStaleTasks(ctx, 10*time.Minute)
	if err != nil {
		t.Fatalf("RequeueStaleTasks 失败: %v", err)
	}
	if n != 1 {
		t.Fatalf("应回收 1 条孤儿任务，实际 %d", n)
	}

	items, err := s.ListQueue(ctx)
	if err != nil {
		t.Fatalf("ListQueue 失败: %v", err)
	}
	if len(items) != 1 || items[0].Status != "queued" {
		t.Fatalf("孤儿任务应回退 queued，实际 %+v", items)
	}

	t.Logf("✅ 孤儿任务回收通过：dispatched 超时未开工 → queued")
}

