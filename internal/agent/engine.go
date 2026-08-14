package agent

// NewEngine 按名字挑引擎实现，返回 Engine 接口。
// cmd/agent 启动时用它：从 agents.engine 读出名字，交给工厂，拿回一个能干活的东西。
// 调用方只认接口，不关心背后是 claude 还是 pi——这就是"多态"的用法。
func NewEngine(engine string) Engine {
	switch engine {
	case "pi":
		return NewPiEngine()
	case "deepseek":
		return NewDeepSeekEngine()
	case "fake":
		return &FakeEngine{Reply: "(fake) 任务已完成"}
	default:
		return NewClaudeEngine() // 未知/空 → 保守回退 claude，不会炸
	}
}
