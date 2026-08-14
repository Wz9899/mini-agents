package store

import (
	"context"
	"path/filepath"
	"testing"
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
