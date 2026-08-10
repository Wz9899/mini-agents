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

	// 2. 创建一条任务
	issue, err := s.CreateIssue(context.Background(), "学会 Go", "第一条测试任务")
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

	t.Logf("✅ 创建成功！id=%s, 状态=%s", issue.ID, issue.Status)
}
