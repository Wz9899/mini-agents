package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// PiEngine 用 Pi coding agent 执行：spawn `pi --mode rpc`，走 JSONL 流式协议。
// 这是第二种引擎。Pi 是统一多厂商 LLM 客户端（本机配置的 provider 决定背后是谁）。
type PiEngine struct {
	Command string // pi 可执行文件（默认 "pi"，PATH 里要有）
}

// NewPiEngine 创建 pi 引擎。
func NewPiEngine() *PiEngine {
	return &PiEngine{Command: "pi"}
}

// Execute 实现 Engine 接口：起一个 rpc 进程，发一条 prompt，攒出最终回复。
// pi 的 RPC 是"事件流"：我们只关心两种事件——
//   text_delta：回答正文的增量（逐字）；agent_settled：一轮 agent 会话结束。
func (e *PiEngine) Execute(ctx context.Context, prompt string) (Result, error) {
	start := time.Now()

	// ① 起进程，挂上 stdin/stdout 管道
	cmd := exec.CommandContext(ctx, e.Command, "--mode", "rpc", "--no-session")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Result{}, fmt.Errorf("pi 拿 stdin 失败: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("pi 拿 stdout 失败: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("pi 启动失败: %w", err)
	}

	// ② 发一条 prompt 命令：用 json.Marshal 转义，中文和引号都不会坏
	msg, _ := json.Marshal(map[string]string{"type": "prompt", "message": prompt})
	fmt.Fprintln(stdin, string(msg))
	// ⚠️ 不能立即 Close！管道语义：关闭 stdin = 告诉 pi"没有更多输入"。
	//    pi 收到 EOF 会把整场会话当结束，处理完当前消息就退出，根本不会调 LLM。
	//    必须保持 stdin 打开，等收到 agent_settled 再关。

	// ③ 逐行读 stdout 事件流
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 事件行可能超长，默认 64KB 上限会报 token too long

	var reply strings.Builder
	for scanner.Scan() {
		var ev struct {
			Type                  string `json:"type"`
			AssistantMessageEvent *struct {
				Type  string `json:"type"`
				Delta string `json:"delta"`
			} `json:"assistantMessageEvent"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue // 非 JSON 的行（可能的启动 banner）跳过
		}

		// 只攒回答正文（text_delta）；thinking_delta 是思考过程，不存
		if ev.Type == "message_update" && ev.AssistantMessageEvent != nil &&
			ev.AssistantMessageEvent.Type == "text_delta" {
			reply.WriteString(ev.AssistantMessageEvent.Delta)
		}
		if ev.Type == "agent_settled" {
			break // 一轮结束，收工
		}
	}
	stdin.Close() // 收工：通知 pi"输入结束"，它才退出，Wait 才返回
	if err := scanner.Err(); err != nil {
		return Result{}, fmt.Errorf("pi 读输出失败: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return Result{}, fmt.Errorf("pi 退出异常: %w", err)
	}

	return Result{
		ExitCode:   0,
		Output:     reply.String(),
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}
