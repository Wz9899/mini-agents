package agent

import (
	"context"
	"testing"
)

// TestFakeEngineExecute 验证 FakeEngine 不调 LLM，直接返回固定回复。
func TestFakeEngineExecute(t *testing.T) {
	e := &FakeEngine{Reply: "(fake) 任务已完成"}
	res, err := e.Execute(context.Background(), "随便一段 prompt")
	if err != nil {
		t.Fatalf("FakeEngine.Execute 失败: %v", err)
	}
	if res.Output != "(fake) 任务已完成" {
		t.Fatalf("输出应为 (fake) 任务已完成，实际 %q", res.Output)
	}
	if res.ExitCode != 0 {
		t.Fatalf("退出码应为 0，实际 %d", res.ExitCode)
	}
}

// TestNewEngineFactory 验证 NewEngine 工厂能按名字返回正确实现。
func TestNewEngineFactory(t *testing.T) {
	if _, ok := NewEngine("fake").(*FakeEngine); !ok {
		t.Fatal("fake 应返回 *FakeEngine")
	}
	if _, ok := NewEngine("claude").(*ClaudeEngine); !ok {
		t.Fatal("claude 应返回 *ClaudeEngine")
	}
	if _, ok := NewEngine("deepseek").(*DeepSeekEngine); !ok {
		t.Fatal("deepseek 应返回 *DeepSeekEngine")
	}
	if _, ok := NewEngine("pi").(*PiEngine); !ok {
		t.Fatal("pi 应返回 *PiEngine")
	}
	// 未知引擎应保守回退到 claude，不能返回 nil
	if _, ok := NewEngine("unknown").(*ClaudeEngine); !ok {
		t.Fatal("未知引擎应回退到 *ClaudeEngine")
	}
}
