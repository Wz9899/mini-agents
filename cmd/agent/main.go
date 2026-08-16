package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
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
	log.Printf("🧑💻 %s [引擎:%s] 开工: %s", identityLabel(me), me.Engine, me.ID)

	// 启动时先回收一次孤儿任务（进程被杀残留的 dispatched/running）
	ctx := context.Background()
	if n, err := s.RequeueStaleTasks(ctx, 10*time.Minute); err != nil {
		log.Printf("⚠️  孤儿任务回收失败: %v", err)
	} else if n > 0 {
		log.Printf("🧹 回收孤儿任务 %d 条", n)
	}

	idle := false // 空闲日志降噪：只在"从有活到没活"时打印一次
	for {
		if n, err := s.RequeueStaleTasks(ctx, 10*time.Minute); err != nil {
			log.Printf("⚠️  孤儿任务回收失败: %v", err)
		} else if n > 0 {
			log.Printf("🧹 回收孤儿任务 %d 条", n)
		}

		worked := runOnce(s, runner, me)
		if !worked {
			if !idle {
				log.Println("🕊️  没有派给我的活，歇会儿")
				idle = true
			}
		} else {
			idle = false
		}
		time.Sleep(2 * time.Second) // 干完一轮歇 2 秒
	}
}

// runOnce 干一轮活：认领（只认领派给我的）→ 开工 → 读任务 → 调引擎 → 记账 → 汇报。
// 返回是否真的认领到了任务（供外层做空闲日志降噪）。
func runOnce(s *store.Store, runner agent.Engine, me *store.Agent) (worked bool) {
	ctx := context.Background()

	// ① 认领：workerID 和 assigneeID 都填我的员工 id（身份 = 账本）
	task, err := s.ClaimTask(ctx, me.ID, me.ID)
	if err != nil {
		log.Printf("⚠️  认领出错: %v", err)
		return false
	}
	if task == nil {
		return false
	}
	worked = true
	log.Printf("✅ 认领到任务: 队列=%s 任务=%s", task.ID, task.IssueID)

	// ② 开工：失败不能直接 return，否则任务会卡死在 dispatched（ClaimTask 只认领 queued）。
	// 走统一失败处理：让它回退 queued 或上报 blocked。
	if err := s.StartTask(ctx, task); err != nil {
		log.Printf("⚠️  开工失败: %v", err)
		reportFailure(s, task, err.Error())
		return
	}

	// ③ 读任务内容，作为问 claude 的问题
	issue, err := s.GetIssue(ctx, task.IssueID)
	if err != nil {
		log.Printf("⚠️  读任务失败: %v", err)
		reportFailure(s, task, err.Error()) // M5：失败统一走重试/上报决策
		return
	}
	if issue == nil {
		log.Printf("⚠️  任务不存在: %s", task.IssueID)
		reportFailure(s, task, "任务不存在")
		return
	}

	// ④ 真·执行：带上人设，让 claude 进入角色（前端 vs 后端回答风格不同）
	// M5：prompt 里约定"做不到就明说"。为什么？claude 面对做不了的事有时会硬着头皮编答案，
	// 那比"承认做不到"更糟（下游会拿着假成果继续干活）。约定以"我无法完成：<原因>"开头，
	// 让程序能精确识别——见下面 ⑥ 的 HasPrefix 检测。
	// 身份条件拼接：role 空（无预设职责）→ 只"你是小王"；description 空 → 不带"人设："
	identity := identityLabel(me)
	persona := ""
	if me.Description != "" {
		persona = "人设：" + me.Description + "。"
	}
	prompt := fmt.Sprintf("你是%s。%s请完成以下任务，用中文给出简洁的答案：\n标题：%s\n描述：%s\n\n注意：如果这个任务你无法完成（例如缺少必要信息、没有对应权限），请以「我无法完成：原因」开头直接回答，不要假装完成。",
		identity, persona, issue.Title, issue.Description)

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
	// M8 会话背景：给 agent 装上"眼睛"。把来源会话最近 20 条拼进 prompt，
	// 这样群里不 @ 的铺垫、单聊里交代的背景，agent 干活时都能看到。
	// 注意：这是"临时上下文"（一次性注入），不是持久记忆——符合 M7 的"交流不沉淀"。
	ctxMsgs, err := s.GetConversationContext(ctx, task.IssueID, 20)
	if err != nil {
		log.Printf("⚠️  读会话背景失败: %v", err)
	} else if len(ctxMsgs) > 0 {
		// sender_id → 名字：agent 消息显示人名（小王/小李），user 消息显示"你"
		allAgents, aerr := s.ListAgents(ctx)
		if aerr != nil {
			log.Printf("⚠️  读员工表失败: %v", aerr)
		}
		nameByID := map[string]string{}
		for _, a := range allAgents {
			nameByID[a.ID] = a.Name
		}
		var sb strings.Builder
		sb.WriteString("\n\n── 会话上下文（最近对话，供你参考背景）──\n")
		for _, m := range ctxMsgs {
			who := "你"
			if m.SenderType == "agent" {
				if n, ok := nameByID[m.SenderID]; ok && n != "" {
					who = n
				}
			}
			sb.WriteString(who + ": " + m.Content + "\n")
		}
		prompt += sb.String()
		log.Printf("💬 已注入会话背景 %d 条", len(ctxMsgs))
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

	// ⑥ 汇报：失败走重试/上报决策，成功标 completed
	if execErr != nil {
		log.Printf("⚠️  引擎执行失败: %v", execErr)
		reportFailure(s, task, execErr.Error())
		return
	}

	// 引擎主动说"我无法完成：<原因>"——直接上报人，不重试。
	// 为什么不重试？claude 这种回答不是"技术故障"（技术故障重试几次可能就好），
	// 而是"这活本身做不了"，重试只会得到同样答案、再烧一次 token。
	// 所以用 BlockTask（不涨 attempts），让人类来看怎么办。
	if strings.HasPrefix(strings.TrimSpace(result.Output), "我无法完成") {
		reason := firstLine(result.Output) // 只要第一行，把原因摘出来给人类看
		if err := s.BlockTask(ctx, task, reason); err != nil {
			log.Printf("⚠️  上报 blocked 失败: %v", err)
		} else {
			alertBlocked(reason)
			cascadeBlocked(s, task) // 我卡死了，依赖我的下游一起上报（防静默卡死）
		}
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
	return // 正常完成路径也必须有 return（worked 已为 true，裸 return 返回它）
}

// reportFailure 统一处理"失败"：交给 store 层做重试/上报决策，再按结果打不同日志。
// 三处失败点（读任务失败 / 任务不存在 / 引擎失败）都走这里，决策逻辑不重复。
//
// 为什么失败还要分"重试"和"上报"两路日志？
// 回退重试 = 系统自己能消化，打个普通日志就行；blocked = 需要人介入，
// 必须醒目。终端 2 秒轮询一轮，红色横幅扫一眼就能发现。
func reportFailure(s *store.Store, task *store.QueuedTask, errMsg string) {
	finalStatus, err := s.FailTask(context.Background(), task, errMsg)
	if err != nil {
		log.Printf("⚠️  标记失败失败: %v", err)
		return
	}

	// task.Attempts 是认领时读到的旧值，FailTask 已在库里 +1，所以这里 +1 才是"第几次失败"
	if finalStatus == "blocked" {
		alertBlocked(fmt.Sprintf("重试 %d 次后仍失败: %s", task.Attempts+1, errMsg))
		cascadeBlocked(s, task) // 重试耗尽 = 我卡死了，依赖我的下游一起上报
	} else {
		log.Printf("🔄 任务失败，自动重试（第 %d 次尝试失败）: %s", task.Attempts+1, errMsg)
	}
}

// cascadeBlocked 上游 failed/blocked 后，把依赖它的下游（还在 queued 排队的）级联标成 blocked。
// 为什么要级联：下游认领有"上游必须 completed"的检查（EXISTS 子查询）。上游卡死了，
// 下游永远等不到 completed，会静默卡在 queued——人根本不知道还有任务在等。
// 级联把下游也标成 blocked（reason 注明"上游卡住"），让人一眼看到"B 卡死拖累了 A"。
func cascadeBlocked(s *store.Store, task *store.QueuedTask) {
	n, err := s.CascadeBlock(context.Background(), task.IssueID, "上游任务 "+task.IssueID+" 已 blocked，无法完成依赖")
	if err != nil {
		log.Printf("⚠️  级联 blocked 失败: %v", err)
	} else if n > 0 {
		log.Printf("🔗 级联上报：%d 个依赖本任务的下游也标成了 blocked", n)
	}
}

// alertBlocked 打印红色横幅：需要人类介入的任务，用 ANSI 红字醒目提示。
// 两条 blocked 路径共用：① claude 主动"我无法完成"（BlockTask）② 重试耗尽（FailTask 返回 blocked）
func alertBlocked(reason string) {
	// \x1b[31m = ANSI 转义：从这里起终端文字变红；\x1b[0m = 复位回默认色
	log.Printf("\x1b[31m🚨 任务上报人类（blocked）: %s\x1b[0m", reason)
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

// identityLabel 组员工身份标签："小王（前端工程师）"。
// role 为空 = 无预设职责的通用员工，就不带括号（否则会显示"小王（）"的空壳）。
func identityLabel(a *store.Agent) string {
	s := a.Name
	if a.Role != "" {
		s += "（" + a.Role + "）"
	}
	return s
}
