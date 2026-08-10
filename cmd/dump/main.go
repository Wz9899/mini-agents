package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"mini-agents/internal/store"
)

// dump 是个只读小工具：打印当前的任务表和队列状态，方便观察状态流转
func main() {
	s, err := store.Open("mini-agents.db")
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	issues, err := s.ListIssues(ctx)
	if err != nil {
		log.Fatalf("查询任务失败: %v", err)
	}
	fmt.Printf("=== 任务 (issues) 共 %d 条 ===\n", len(issues))
	for _, i := range issues {
		fmt.Printf("  %s | %-11s | %s\n", shortID(i.ID), i.Status, i.Title)
	}

	queue, err := s.ListQueue(ctx)
	if err != nil {
		log.Fatalf("查询队列失败: %v", err)
	}
	fmt.Printf("=== 队列 (task_queue) 共 %d 条 ===\n", len(queue))
	for _, q := range queue {
		fmt.Printf("  %s | %-10s | worker=%s | started=%s finished=%s",
			shortID(q.ID), q.Status, workerName(q.WorkerID), ts(q.StartedAt), ts(q.FinishedAt))
		if q.Error.Valid {
			fmt.Printf(" | error=%s", q.Error.String)
		}
		fmt.Println()
	}

	runs, err := s.ListRuns(ctx)
	if err != nil {
		log.Fatalf("查询执行日志失败: %v", err)
	}
	fmt.Printf("=== 执行日志 (agent_runs) 共 %d 条 ===\n", len(runs))
	for _, r := range runs {
		fmt.Printf("  %s | task=%s | exit=%d | %dms | %s\n",
			shortID(r.ID), shortID(r.TaskID), r.ExitCode, r.DurationMs, firstLine(r.Output))
	}
}

// firstLine 取文本的第一行（输出太长时日志里省地方）
func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

// workerName 把可空的 worker_id 转成短 id，没值显示 "-"
func workerName(w sql.NullString) string {
	if !w.Valid {
		return "-"
	}
	return shortID(w.String)
}

// shortID 把 ULID 截短成前 8 位，输出更好看
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// ts 把可空时间转成 "15:04:05"，没值就显示 "-"
func ts(t sql.NullTime) string {
	if !t.Valid {
		return "-"
	}
	return t.Time.Format("15:04:05")
}
