package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"mini-agents/internal/store"
)

// newTestStore 打开一个临时数据库，测试结束自动清理。
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestCreateIssueAPI 验证 HTTP 层创建任务的完整链路：
// 先入职员工，再 POST /api/issues，返回 201 且指派人是刚入职的员工。
func TestCreateIssueAPI(t *testing.T) {
	s := newTestStore(t)

	agent, err := s.CreateAgent(context.Background(), "小王", "前端工程师", "负责页面", "fake")
	if err != nil {
		t.Fatalf("CreateAgent 失败: %v", err)
	}

	body := `{"title":"测试任务","description":"描述","assignee":"小王"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	newMux(s, "").ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("状态码应为 201，实际 %d，body=%s", w.Code, w.Body.String())
	}

	var issue store.Issue
	if err := json.NewDecoder(w.Body).Decode(&issue); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if issue.Title != "测试任务" {
		t.Fatalf("标题应为 测试任务，实际 %s", issue.Title)
	}
	if issue.AssigneeID != agent.ID {
		t.Fatalf("指派人不匹配: 应=%s 实=%s", agent.ID, issue.AssigneeID)
	}
}

// TestCreateAgentAPI 验证 HTTP 层员工入职接口。
func TestCreateAgentAPI(t *testing.T) {
	s := newTestStore(t)

	body := `{"name":"小李","role":"后端工程师","engine":"fake"}`
	req := httptest.NewRequest(http.MethodPost, "/api/agents", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	newMux(s, "").ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("状态码应为 201，实际 %d，body=%s", w.Code, w.Body.String())
	}

	var agent store.Agent
	if err := json.NewDecoder(w.Body).Decode(&agent); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if agent.Name != "小李" {
		t.Fatalf("名字应为 小李，实际 %s", agent.Name)
	}
	if agent.Engine != "fake" {
		t.Fatalf("引擎应为 fake，实际 %s", agent.Engine)
	}
}

// TestCreateMessageAPI 验证消息触发任务的 HTTP 闭环：
// 建单聊会话后，发一条消息，返回 201 且消息已入库。
func TestCreateMessageAPI(t *testing.T) {
	s := newTestStore(t)

	agent, err := s.CreateAgent(context.Background(), "小王", "前端工程师", "负责页面", "fake")
	if err != nil {
		t.Fatalf("CreateAgent 失败: %v", err)
	}
	conv, err := s.CreateConversation(context.Background(), "和小王的私聊", "direct", []string{agent.ID})
	if err != nil {
		t.Fatalf("CreateConversation 失败: %v", err)
	}

	body := `{"content":"帮我写登录页"}`
	req := httptest.NewRequest(http.MethodPost, "/api/conversations/"+conv.ID+"/messages", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	newMux(s, "").ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("状态码应为 201，实际 %d，body=%s", w.Code, w.Body.String())
	}

	var msg store.Message
	if err := json.NewDecoder(w.Body).Decode(&msg); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if msg.ConversationID != conv.ID {
		t.Fatalf("会话不匹配: 应=%s 实=%s", conv.ID, msg.ConversationID)
	}
	if msg.Content != "帮我写登录页" {
		t.Fatalf("消息内容应为 帮我写登录页，实际 %s", msg.Content)
	}
}

// TestUpdateAgentAPI 验证员工档案修改接口：改名成功 + 改不存在的员工返回 404。
func TestUpdateAgentAPI(t *testing.T) {
	s := newTestStore(t)

	agent, err := s.CreateAgent(context.Background(), "小王", "前端工程师", "负责页面", "fake")
	if err != nil {
		t.Fatalf("CreateAgent 失败: %v", err)
	}

	body := `{"name":"王师傅","role":"架构师","description":"负责架构"}`
	patch := func(id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "/api/agents/"+id, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		newMux(s, "").ServeHTTP(w, req)
		return w
	}

	// ① 改名成功：200 + 返回新档案
	w := patch(agent.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("状态码应为 200，实际 %d，body=%s", w.Code, w.Body.String())
	}
	var updated store.Agent
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if updated.Name != "王师傅" || updated.Role != "架构师" {
		t.Fatalf("修改结果不符: %+v", updated)
	}

	// ② 改不存在的员工：404（老 bug：store 不报"员工不存在"，这里会返回 200）
	if w := patch("no-such-id"); w.Code != http.StatusNotFound {
		t.Fatalf("状态码应为 404，实际 %d，body=%s", w.Code, w.Body.String())
	}
}

// TestDeleteAgentAPI 验证删除员工接口：删除成功（库里真删了）+ 删除不存在的员工返回 404。
func TestDeleteAgentAPI(t *testing.T) {
	s := newTestStore(t)

	agent, err := s.CreateAgent(context.Background(), "小王", "前端工程师", "负责页面", "fake")
	if err != nil {
		t.Fatalf("CreateAgent 失败: %v", err)
	}

	del := func(id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodDelete, "/api/agents/"+id, nil)
		w := httptest.NewRecorder()
		newMux(s, "").ServeHTTP(w, req)
		return w
	}

	// ① 删除成功：204，且库里真的没了
	if w := del(agent.ID); w.Code != http.StatusNoContent {
		t.Fatalf("状态码应为 204，实际 %d，body=%s", w.Code, w.Body.String())
	}
	gone, err := s.GetAgentByID(context.Background(), agent.ID)
	if err != nil {
		t.Fatalf("GetAgentByID 失败: %v", err)
	}
	if gone != nil {
		t.Fatalf("员工应已被删除，实际仍存在: %+v", gone)
	}

	// ② 再删一次（已不存在）：404
	if w := del(agent.ID); w.Code != http.StatusNotFound {
		t.Fatalf("状态码应为 404，实际 %d，body=%s", w.Code, w.Body.String())
	}
}

// TestRenameConversationAPI 验证会话改名接口：
// 改名成功 + 改成同名不误报 404（老 bug：RowsAffected()==0 判不存在）+ 改不存在的会话返回 404。
func TestRenameConversationAPI(t *testing.T) {
	s := newTestStore(t)

	agent, err := s.CreateAgent(context.Background(), "小王", "前端工程师", "负责页面", "fake")
	if err != nil {
		t.Fatalf("CreateAgent 失败: %v", err)
	}
	conv, err := s.CreateConversation(context.Background(), "老群名", "group", []string{agent.ID})
	if err != nil {
		t.Fatalf("CreateConversation 失败: %v", err)
	}

	rename := func(id, name string) *httptest.ResponseRecorder {
		body := `{"name":"` + name + `"}`
		req := httptest.NewRequest(http.MethodPatch, "/api/conversations/"+id, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		newMux(s, "").ServeHTTP(w, req)
		return w
	}

	// ① 正常改名：204，且库里生效
	if w := rename(conv.ID, "新群名"); w.Code != http.StatusNoContent {
		t.Fatalf("改名状态码应为 204，实际 %d，body=%s", w.Code, w.Body.String())
	}
	got, err := s.GetConversation(context.Background(), conv.ID)
	if err != nil {
		t.Fatalf("GetConversation 失败: %v", err)
	}
	if got.Name != "新群名" {
		t.Fatalf("会话名应为 新群名，实际 %s", got.Name)
	}

	// ② 改成同名：不误报 404（值没变，SQLite 的 RowsAffected()==0）
	if w := rename(conv.ID, "新群名"); w.Code != http.StatusNoContent {
		t.Fatalf("同名改名状态码应为 204，实际 %d，body=%s", w.Code, w.Body.String())
	}

	// ③ 改不存在的会话：404
	if w := rename("no-such-conv", "随便"); w.Code != http.StatusNotFound {
		t.Fatalf("状态码应为 404，实际 %d，body=%s", w.Code, w.Body.String())
	}
}
