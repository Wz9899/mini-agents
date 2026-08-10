package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"mini-agents/internal/agent"
	"mini-agents/internal/store"
)

func main() {
	s, err := store.Open("mini-agents.db")
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer s.Close()

	// 创建执行器：负责真正调用 claude CLI
	runner := agent.NewRunner()

	workerID := "worker-" + store.NewID()
	log.Printf("🧑🔧 worker 启动: %s", workerID)

	for {
		runOnce(s, runner, workerID)
		time.Sleep(2 * time.Second) // 干完一轮歇 2 秒
	}
}

// runOnce 干一轮活：认领 → 开工 → 读任务 → 调 claude → 记账 → 汇报
func runOnce(s *store.Store, runner *agent.Runner, workerID string) {
	ctx := context.Background()

	// ① 认领
	task, err := s.ClaimTask(ctx, workerID)
	if err != nil {
		log.Printf("⚠️  认领出错: %v", err)
		return
	}
	if task == nil {
		log.Println("🕊️  队列空，歇会儿")
		return
	}
	log.Printf("✅ 认领到任务: 队列=%s 任务=%s", task.ID, task.IssueID)

	// ② 开工
	if err := s.StartTask(ctx, task); err != nil {
		log.Printf("⚠️  开工失败: %v", err)
		return
	}

	// ③ 读任务内容，作为问 claude 的问题
	issue, err := s.GetIssue(ctx, task.IssueID)
	if err != nil {
		log.Printf("⚠️  读任务失败: %v", err)
		s.FailTask(ctx, task, err.Error())
		return
	}
	if issue == nil {
		log.Printf("⚠️  任务不存在: %s", task.IssueID)
		s.FailTask(ctx, task, "任务不存在")
		return
	}

	// ④ 真·执行：调 claude，让 Agent 完成这条任务
	prompt := fmt.Sprintf("你是执行任务的 agent。请完成以下任务，用中文给出简洁的答案：\n标题：%s\n描述：%s",
		issue.Title, issue.Description)
	log.Printf("🔨 调 claude 执行: %s", issue.Title)

	// 给 claude 设 2 分钟超时，防止它卡死（CommandContext 配合 WithTimeout）
	execCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	result, execErr := runner.Execute(execCtx, prompt)

	// ⑤ 记账：无论成败都写 agent_runs（执行过程要留痕）
	runID, err := s.RecordRun(ctx, task, result.ExitCode, result.Output, result.DurationMs)
	if err != nil {
		log.Printf("⚠️  记账失败: %v", err)
	}

	// ⑥ 汇报：失败标 failed，成功标 completed
	if execErr != nil {
		log.Printf("⚠️  claude 执行失败: %v", execErr)
		s.FailTask(ctx, task, execErr.Error())
		return
	}
	if err := s.CompleteTask(ctx, task); err != nil {
		log.Printf("⚠️  完成失败: %v", err)
		return
	}
	log.Printf("🏁 任务完成: %s (执行记录 %s)", issue.Title, runID)
	log.Printf("   claude 回答: %s", firstLine(result.Output))
}

// firstLine 取输出的第一行，日志里省地方
func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
