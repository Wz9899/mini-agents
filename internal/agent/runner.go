// Package agent 负责"真正干活"：调用 claude CLI 并拿回结果。
// 与数据层（store）解耦——runner 只管执行，不碰数据库。
package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// Result 是一次执行的完整记录（对应 agent_runs 表的一行）
type Result struct {
	ExitCode   int    // 退出码：0 成功，非 0 失败
	Output     string // claude 返回的文本
	DurationMs int64  // 执行耗时（毫秒）
}

// Runner 负责执行 claude -p，是 worker 的"双手"。
// Command 字段可替换成假程序路径，方便以后测试（不用真调 claude）。
type Runner struct {
	Command string
}

// NewRunner 创建一个默认的 runner。
// Windows 上直接指向 npm 装的 claude.exe——绕开 cmd shim 的 GBK 编码坑。
func NewRunner() *Runner {
	cmd := "claude" // 非 Windows 直接用 PATH 里的 claude
	if runtime.GOOS == "windows" {
		// npm 全局安装的原生可执行文件（真正的 .exe，不是 shim 脚本）
		home, err := os.UserHomeDir()
		if err == nil {
			exe := filepath.Join(home, "AppData", "Roaming", "npm", "node_modules",
				"@anthropic-ai", "claude-code", "bin", "claude.exe")
			if _, err := os.Stat(exe); err == nil {
				cmd = exe
			}
		}
	}
	return &Runner{Command: cmd}
}

// Execute 跑一次 `claude -p <prompt>`，返回结果。
// 直接 exec 原生可执行文件：Go 用 UTF-16 把参数传给 Windows 的 CreateProcess，
// 中文 prompt 不会被 cmd 的 GBK 代码页破坏（这是之前乱码 bug 的根因）。
func (r *Runner) Execute(ctx context.Context, prompt string) (Result, error) {
	start := time.Now() // 计时开始

	cmd := exec.CommandContext(ctx, r.Command, "-p", prompt)

	// CombinedOutput 同时拿到标准输出和错误输出（合并成一份）
	out, err := cmd.CombinedOutput()

	// 退出码：进程启动成功就有 ProcessState
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	result := Result{
		ExitCode:   exitCode,
		Output:     string(out),
		DurationMs: time.Since(start).Milliseconds(),
	}
	if err != nil {
		return result, fmt.Errorf("claude 执行失败(退出码 %d): %w", exitCode, err)
	}
	return result, nil
}
