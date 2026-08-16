package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"sort"
	"strings"

	"mini-agents/internal/store"
)

func main() {
	// 0. 启动参数：默认只监听本机，避免局域网内其他人访问
	addr := flag.String("addr", "127.0.0.1:8080", "监听地址")
	flag.Parse()

	// 1. 打开数据库（自动建表）
	s, err := store.Open("mini-agents.db")
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer s.Close()

	// 2. 创建路由表，登记"哪个 URL 由哪个函数处理"
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/issues", handleCreateIssue(s))
	mux.HandleFunc("GET /api/issues", handleListIssues(s))
	mux.HandleFunc("POST /api/agents", handleCreateAgent(s))
	mux.HandleFunc("GET /api/agents", handleListAgents(s))
	mux.HandleFunc("POST /api/conversations", handleCreateConversation(s))
	mux.HandleFunc("GET /api/conversations", handleListConversations(s))
	mux.HandleFunc("POST /api/conversations/{id}/messages", handleCreateMessage(s))
	mux.HandleFunc("GET /api/conversations/{id}/messages", handleListMessages(s))
	mux.HandleFunc("GET /api/team", handleTeam(s)) // 团队状态：前端状态灯轮询

	// 2.5 前端静态文件（M6 飞书式界面）：web/ 目录按"文件系统"暴露。
	// 为什么用 "GET /"？它是兜底路由——ServeMux 永远选"最长匹配"，
	// 所以 /api/... 走上面注册的精确路由，剩下的（如 / 和 /index.html）才落进这里。
	mux.Handle("GET /", http.FileServer(http.Dir("web")))

	// 3. 启动 HTTP 服务
	log.Printf("服务已启动: http://%s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("服务启动失败: %v", err)
	}
}

// handleCreateIssue 返回一个"创建任务"的处理函数
func handleCreateIssue(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1. 从请求体里读出 JSON
		var req struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Assignee    string `json:"assignee"`   // 派给谁——填名字（agents.name），不是 id
			DependsOn   string `json:"depends_on"` // 上游任务 id（issues.id）；空 = 无依赖
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "请求体不是合法 JSON", http.StatusBadRequest)
			return
		}
		if req.Title == "" {
			http.Error(w, "title 不能为空", http.StatusBadRequest)
			return
		}
		if req.Assignee == "" {
			http.Error(w, "assignee 不能为空（任务必须派给具体员工）", http.StatusBadRequest)
			return
		}

		// 2. 名字 → id：API 对用户友好用名字，数据层用 id 关联
		who, err := s.GetAgent(r.Context(), req.Assignee)
		if err != nil {
			http.Error(w, "查询员工失败: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if who == nil {
			http.Error(w, "员工不存在: "+req.Assignee, http.StatusBadRequest)
			return
		}

		// 2.5 depends_on：校验上游任务存在（防写错 id），通过再往下走
		dependsOn := ""
		if req.DependsOn != "" {
			up, err := s.GetIssue(r.Context(), req.DependsOn)
			if err != nil {
				http.Error(w, "查询上游任务失败: "+err.Error(), http.StatusInternalServerError)
				return
			}
			if up == nil {
				http.Error(w, "上游任务不存在: "+req.DependsOn, http.StatusBadRequest)
				return
			}
			dependsOn = up.ID
		}

		// 3. 调数据层真正写入
		issue, err := s.CreateIssue(r.Context(), req.Title, req.Description, who.ID, dependsOn)
		if err != nil {
			http.Error(w, "创建失败: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 4. 把结果转成 JSON 返回给调用方
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated) // 201 = 创建成功
		json.NewEncoder(w).Encode(issue)
	}
}

// handleListIssues 返回一个"列出所有任务"的处理函数
func handleListIssues(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issues, err := s.ListIssues(r.Context())
		if err != nil {
			http.Error(w, "查询失败: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(issues)
	}
}

// handleCreateAgent 返回一个"员工入职"的处理函数
func handleCreateAgent(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name        string `json:"name"`
			Role        string `json:"role"`
			Description string `json:"description"`
			Engine      string `json:"engine"` // claude | pi | deepseek | fake；不填默认 claude
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "请求体不是合法 JSON", http.StatusBadRequest)
			return
		}
		// 只要求名字：role（职责）允许留空——留空 = 无预设职责的通用员工
		if req.Name == "" {
			http.Error(w, "name 不能为空", http.StatusBadRequest)
			return
		}

		// engine 校验：拼错直接 400，别等运行时静默回退成 claude（那时反而难查）
		if req.Engine == "" {
			req.Engine = "claude" // 默认值：老客户端的请求不带 engine 也能用
		}
		switch req.Engine {
		case "claude", "pi", "deepseek", "fake":
		default:
			http.Error(w, "engine 必须是 claude|pi|deepseek|fake 之一", http.StatusBadRequest)
			return
		}

		agent, err := s.CreateAgent(r.Context(), req.Name, req.Role, req.Description, req.Engine)
		if err != nil {
			// 重名：UNIQUE 约束冲突。这是"你的输入有问题"，不是"服务器坏了"
			if strings.Contains(err.Error(), "UNIQUE") {
				http.Error(w, "重名: agents 表里已有 "+req.Name, http.StatusConflict)
				return
			}
			http.Error(w, "创建失败: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated) // 201 = 创建成功
		json.NewEncoder(w).Encode(agent)
	}
}

// handleListAgents 返回一个"列出所有员工"的处理函数
func handleListAgents(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agents, err := s.ListAgents(r.Context())
		if err != nil {
			http.Error(w, "查询失败: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(agents)
	}
}

// handleCreateConversation 返回一个"建会话（拉群/开单聊）"的处理函数
func handleCreateConversation(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name    string `json:"name"`
			Type    string `json:"type"` // direct 单聊 | group 群聊；空则默认 direct
			Members []struct {
				Name string `json:"name"` // agent 名字（API 友好），服务端转 id
			} `json:"members"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "请求体不是合法 JSON", http.StatusBadRequest)
			return
		}
		if req.Name == "" {
			http.Error(w, "name 不能为空", http.StatusBadRequest)
			return
		}
		if req.Type == "" {
			req.Type = "direct"
		}
		if len(req.Members) == 0 {
			http.Error(w, "至少要拉一位 agent 进会话", http.StatusBadRequest)
			return
		}

		// 成员名字 → id：跟 assignee 同样的"名字转 id"套路
		var ids []string
		for _, m := range req.Members {
			a, err := s.GetAgent(r.Context(), m.Name)
			if err != nil {
				http.Error(w, "查询员工失败: "+err.Error(), http.StatusInternalServerError)
				return
			}
			if a == nil {
				http.Error(w, "员工不存在: "+m.Name, http.StatusBadRequest)
				return
			}
			ids = append(ids, a.ID)
		}

		conv, err := s.CreateConversation(r.Context(), req.Name, req.Type, ids)
		if err != nil {
			http.Error(w, "创建会话失败: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated) // 201 = 创建成功
		json.NewEncoder(w).Encode(conv)
	}
}

// handleListConversations 返回一个"列出所有会话（带成员）"的处理函数。
// 用带成员的版本：前端左栏要显示每个会话里有谁、以及聚合状态灯。
func handleListConversations(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		convs, err := s.ListConversationsWithMembers(r.Context())
		if err != nil {
			http.Error(w, "查询失败: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(convs)
	}
}

// handleTeam 返回一个"团队状态"的处理函数：每个 agent + 当前状态（前端状态灯轮询用）
func handleTeam(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, err := s.TeamStatus(r.Context())
		if err != nil {
			http.Error(w, "查询失败: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(status)
	}
}

// handleCreateMessage 返回一个"往会话发消息"的处理函数。
// M8 触发语义（社交软件式）：
//   单聊：不解析 @，直接派活给聊天对象（找员工就是为了让他干活）；
//   群聊：必须 @ 才触发干活，且只能 @ 群成员（支持一条消息 @ 多人）。
func handleCreateMessage(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		convID := r.PathValue("id") // 路由里的 {id} 参数

		// ① 先校验会话存在，别把消息写进不存在的会话
		conv, err := s.GetConversation(r.Context(), convID)
		if err != nil {
			http.Error(w, "查询会话失败: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if conv == nil {
			http.Error(w, "会话不存在: "+convID, http.StatusNotFound)
			return
		}

		var req struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "请求体不是合法 JSON", http.StatusBadRequest)
			return
		}
		if req.Content == "" {
			http.Error(w, "content 不能为空", http.StatusBadRequest)
			return
		}

		// ② 决定"派给谁干活"——单聊和群聊是两套语义
		var targets []store.Agent // 要触发干活的人（0 个 = 只聊天不派活）
		if conv.Type == "group" {
			// 群聊：必须 @ 才触发。先解析内容里 @ 到了哪些员工，再校验"都在群里"
			all, err := s.ListAgents(r.Context())
			if err != nil {
				http.Error(w, "查询员工失败: "+err.Error(), http.StatusInternalServerError)
				return
			}
			// 按名字长度从长到短排序，先匹配长名字，避免 @小王总 误命中 小王
			sort.Slice(all, func(i, j int) bool { return len(all[i].Name) > len(all[j].Name) })
			var mentioned []store.Agent
			for i := range all {
				name := all[i].Name
				// 一条消息可以 @ 多人，全部收集；同名员工只触发一次
				for _, chunk := range strings.Split(req.Content, "@")[1:] {
					if chunk == name {
						mentioned = append(mentioned, all[i])
						break
					}
					if strings.HasPrefix(chunk, name) && len(chunk) > len(name) {
						// 取名字后第一个完整字符：中文标点占多个字节，必须转 []rune 再取，
						// 直接 chunk[len(name)] 只拿到第一个 byte，跟 rune 标点比较会编译报错
						next := []rune(chunk[len(name):])[0]
						if next == ' ' || next == '\t' || next == '\n' ||
							next == ',' || next == '，' || next == '。' ||
							next == '、' || next == ';' || next == '；' ||
							next == ':' || next == '：' || next == '!' || next == '！' ||
							next == '?' || next == '？' {
							mentioned = append(mentioned, all[i])
							break
						}
					}
				}
			}
			// 群成员校验：@ 的人必须在群里（社交软件语义：群里只能点到群里的人）
			members, err := s.ListConversationMembers(r.Context(), convID)
			if err != nil {
				http.Error(w, "查询会话成员失败: "+err.Error(), http.StatusInternalServerError)
				return
			}
			inGroup := map[string]bool{}
			for _, m := range members {
				inGroup[m.ID] = true
			}
			for _, ag := range mentioned {
				if !inGroup[ag.ID] {
					http.Error(w, ag.Name+" 不在这个会话里，无法 @（请先把他拉进群）", http.StatusBadRequest)
					return
				}
			}
			targets = mentioned // 校验都过了，@ 到谁谁干活（多 @ 全触发）
		} else {
			// 单聊：不解析 @，直接派给唯一成员。整条消息就是任务内容。
			members, err := s.ListConversationMembers(r.Context(), convID)
			if err != nil {
				http.Error(w, "查询会话成员失败: "+err.Error(), http.StatusInternalServerError)
				return
			}
			if len(members) > 0 {
				targets = append(targets, members[0])
			}
		}

		// ③ 创建任务：每个 target 一个（多 @ = 多个任务），收集任务 id
		taskIDs := []string{}
		for i := range targets {
			issue, err := s.CreateIssue(r.Context(), req.Content, req.Content, targets[i].ID, "")
			if err != nil {
				http.Error(w, "触发任务失败: "+err.Error(), http.StatusInternalServerError)
				return
			}
			taskIDs = append(taskIDs, issue.ID)
		}

		// ④ 人类发消息：sender_type=user，sender_id='me'（人类是隐式所有者）
		// 用 SendMessageWithTasks 把"消息 + 任务关联"放进一个事务，避免半截状态。
		msg, err := s.SendMessageWithTasks(r.Context(), convID, "user", "me", req.Content, taskIDs)
		if err != nil {
			http.Error(w, "发送消息失败: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(msg)
	}
}

// handleListMessages 返回一个"取消息流"的处理函数（前端 2 秒轮询用）。
// 支持 ?after=<消息ID> 做增量拉取：前端传上次最后一条消息 id，后端只返回新消息。
func handleListMessages(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		convID := r.PathValue("id")
		after := r.URL.Query().Get("after")
		msgs, err := s.ListMessagesAfter(r.Context(), convID, after)
		if err != nil {
			http.Error(w, "查询消息失败: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(msgs)
	}
}
