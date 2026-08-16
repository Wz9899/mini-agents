// Package memory 提供 M7 双层知识库的封装。
// 底层数据访问全部委托给 internal/store，这里只做语义化入口：
// Capture 写知识，Recall 读知识，RecallForAgent 拼装注入文本。
package memory

import (
	"context"

	"mini-agents/internal/store"
)

// Manager 是知识库门面。
type Manager struct {
	store *store.Store
}

// New 创建一个基于 Store 的知识库管理器。
func New(s *store.Store) *Manager {
	return &Manager{store: s}
}

// Capture 写入一条团队或个人知识。
func (m *Manager) Capture(ctx context.Context, scope, agentID, kind, content string) (*store.Memory, error) {
	return m.store.CaptureMemory(ctx, scope, agentID, kind, content)
}

// Recall 按作用域读取知识。
func (m *Manager) Recall(ctx context.Context, scope, agentID string) ([]store.Memory, error) {
	return m.store.RecallMemory(ctx, scope, agentID)
}

// RecallForAgent 返回某个 agent 干活前需要注入的团队 + 个人知识文本。
func (m *Manager) RecallForAgent(ctx context.Context, agentID string) (string, error) {
	return m.store.RecallMemoryForAgent(ctx, agentID)
}
