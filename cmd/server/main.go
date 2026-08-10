package main

import (
	"encoding/json"
	"log"
	"net/http"

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
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "请求体不是合法 JSON", http.StatusBadRequest)
			return
		}
		if req.Title == "" {
			http.Error(w, "title 不能为空", http.StatusBadRequest)
			return
		}

		// 2. 调数据层真正写入
		issue, err := s.CreateIssue(r.Context(), req.Title, req.Description)
		if err != nil {
			http.Error(w, "创建失败: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 3. 把结果转成 JSON 返回给调用方
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
