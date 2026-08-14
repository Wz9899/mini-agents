package agent

import (
	"context"
	"fmt"
)

// DeepSeekEngine 用 DeepSeek Harness（dsh）执行——预留占位。
// dsh 提供 API Gateway（HTTP RPC），但需要先 build deepseek-harness 并启动服务。
// 这是"接口是合同"的体现：约定已立好，将来接入只是填一个 Execute。
type DeepSeekEngine struct{}

// NewDeepSeekEngine 创建 dsh 引擎。
func NewDeepSeekEngine() *DeepSeekEngine {
	return &DeepSeekEngine{}
}

// Execute 实现 Engine 接口：目前只返回清晰的"待接入"错误，不会静默失败。
func (e *DeepSeekEngine) Execute(ctx context.Context, prompt string) (Result, error) {
	return Result{}, fmt.Errorf("dsh 引擎待接入：需要先 build deepseek-harness 并启动其服务")
}
