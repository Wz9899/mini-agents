package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"mini-agents/internal/store"
)

func main() {
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

	// 3. 启动 HTTP 服务，监听 8080 端口
	log.Println("服务已启动: http://localhost:8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
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
		if req.Name == "" || req.Role == "" {
			http.Error(w, "name 和 role 不能为空", http.StatusBadRequest)
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

// handleListConversations 返回一个"列出所有会话"的处理函数
func handleListConversations(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		convs, err := s.ListConversations(r.Context())
		if err != nil {
			http.Error(w, "查询失败: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(convs)
	}
}

// handleCreateMessage 返回一个"往会话发消息"的处理函数。
// @解析和触发任务是下一课（M4.5-4）的事，这步先做"纯发消息"。
func handleCreateMessage(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		convID := r.PathValue("id") // 路由里的 {id} 参数

		// 先校验会话存在，别把消息写进不存在的会话
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

		// 2. @解析：消息里 @ 到哪位员工，就触发哪位员工干活
		// 遍历员工表，看消息里有没有 "@员工名"（中文名字用字符串匹配，不用正则）
		agents, err := s.ListAgents(r.Context())
		if err != nil {
			http.Error(w, "查询员工失败: "+err.Error(), http.StatusInternalServerError)
			return
		}
		var who *store.Agent
		for i := range agents {
			if strings.Contains(req.Content, "@"+agents[i].Name) {
				who = &agents[i]
				break
			}
		}

		// 3. 触发：@ 到员工 → 创建任务（assignee=他）+ 自动入队，消息带上 task_id 关联
		taskID := ""
		if who != nil {
			issue, err := s.CreateIssue(r.Context(), req.Content, req.Content, who.ID, "")
			if err != nil {
				http.Error(w, "触发任务失败: "+err.Error(), http.StatusInternalServerError)
				return
			}
			taskID = issue.ID
		}

		// 4. 人类发消息：sender_type=user，sender_id='me'（人类是隐式所有者）
		msg, err := s.SendMessage(r.Context(), convID, "user", "me", req.Content, taskID)
		if err != nil {
			http.Error(w, "发送消息失败: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(msg)
	}
}

// handleListMessages 返回一个"取消息流"的处理函数（前端 2 秒轮询用）
func handleListMessages(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		convID := r.PathValue("id")
		msgs, err := s.ListMessages(r.Context(), convID)
		if err != nil {
			http.Error(w, "查询消息失败: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(msgs)
	}
}
