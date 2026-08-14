package agent

import (
	"context"
	"time"
)

// FakeEngine 假引擎：不调任何 LLM，直接返回写死的回复。
// 用途：测试 / 干跑——先跑通全流程、省 token，验证逻辑无误再换真引擎。
// 这就是"打桩（stub）"：接口立好了，用一个假实现先把系统跑起来。
type FakeEngine struct {
	Reply string // 写死的回复
}

// Execute 实现 Engine 接口：假装干了一点活，返回预设的回复。
func (e *FakeEngine) Execute(ctx context.Context, prompt string) (Result, error) {
	time.Sleep(100 * time.Millisecond) // 假装花了点时间，模拟真执行
	return Result{
		ExitCode:   0,
		Output:     e.Reply,
		DurationMs: 100,
	}, nil
}
