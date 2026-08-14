package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"mini-agents/internal/agent"
	"mini-agents/internal/store"
)

func main() {
	// -name 指定"我是谁"（必须对应 agents 表里的 name）
	name := flag.String("name", "", "员工名字（对应 agents 表）")
	flag.Parse()
	if *name == "" {
		log.Fatal("用法: agent -name 小王")
	}

	s, err := store.Open("mini-agents.db")
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer s.Close()

	// 查自己的档案：id/role/description 都从 agents 表来
	me, err := s.GetAgent(context.Background(), *name)
	if err != nil {
		log.Fatalf("查档案失败: %v", err)
	}
	if me == nil {
		log.Fatalf("❌ agents 表里没有叫 %s 的员工，先让人类帮你入职", *name)
	}

	// 创建执行器：按档案里的 engine 字段挑引擎（claude/pi/deepseek/fake）
	runner := agent.NewEngine(me.Engine)
	log.Printf("🧑💻 %s（%s）[引擎:%s] 开工: %s", me.Name, me.Role, me.Engine, me.ID)

	for {
		runOnce(s, runner, me)
		time.Sleep(2 * time.Second) // 干完一轮歇 2 秒
	}
}

// runOnce 干一轮活：认领（只认领派给我的）→ 开工 → 读任务 → 调引擎 → 记账 → 汇报
func runOnce(s *store.Store, runner agent.Engine, me *store.Agent) {
	ctx := context.Background()

	// ① 认领：workerID 和 assigneeID 都填我的员工 id（身份 = 账本）
	task, err := s.ClaimTask(ctx, me.ID, me.ID)
	if err != nil {
		log.Printf("⚠️  认领出错: %v", err)
		return
	}
	if task == nil {
		log.Println("🕊️  没有派给我的活，歇会儿")
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

	// ④ 真·执行：带上人设，让 claude 进入角色（前端 vs 后端回答风格不同）
	prompt := fmt.Sprintf("你是%s（%s）。人设：%s\n请完成以下任务，用中文给出简洁的答案：\n标题：%s\n描述：%s",
		me.Name, me.Role, me.Description, issue.Title, issue.Description)

	// 协作任务：我有 depends_on → 上游已完成，把上游的成果拼进 prompt 供我参考
	if issue.DependsOn != "" {
		up, err := s.GetIssue(ctx, issue.DependsOn) // 上游任务是什么
		if err != nil {
			log.Printf("⚠️  读上游任务失败: %v", err)
		} else if up != nil {
			upOut, err := s.GetLatestRun(ctx, up.ID) // 上游最近一次执行的输出
			if err != nil {
				log.Printf("⚠️  读上游输出失败: %v", err)
			}
			log.Printf("🔗 依赖上游任务: %s（成果已注入）", up.Title)
			prompt += fmt.Sprintf("\n\n上游任务已完成，其成果供你参考：\n上游标题：%s\n上游输出：\n%s",
				up.Title, upOut)
		}
	}
	log.Printf("🔨 调引擎执行: %s", issue.Title)

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
		log.Printf("⚠️  引擎执行失败: %v", execErr)
		s.FailTask(ctx, task, execErr.Error())
		return
	}
	if err := s.CompleteTask(ctx, task); err != nil {
		log.Printf("⚠️  完成失败: %v", err)
		return
	}
	log.Printf("🏁 任务完成: %s (执行记录 %s)", issue.Title, runID)
	log.Printf("   引擎回答: %s", firstLine(result.Output))

	// 协作回信：任务是消息 @ 触发的话，把结果作为回复消息发回会话（副作用，不影响任务状态）
	src, err := s.GetMessageByTask(ctx, task.IssueID)
	if err != nil {
		log.Printf("⚠️  查来源消息失败: %v", err)
	} else if src != nil {
		if _, err := s.SendMessage(ctx, src.ConversationID, "agent", me.ID, result.Output, task.IssueID); err != nil {
			log.Printf("⚠️  回复消息发送失败: %v", err)
		} else {
			log.Printf("💬 已回复会话: %s", src.ConversationID)
		}
	}
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
