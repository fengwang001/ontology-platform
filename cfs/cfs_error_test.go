package cfs

import (
	"errors"
	"testing"
)

func mustReject(t *testing.T, err, want error, what string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s 应报 %v, 实际 %v", what, want, err)
	}
}

// TestInvalidConfig 构造参数越界整体拒绝。
func TestInvalidConfig(t *testing.T) {
	cases := []struct {
		q, p, s, b int64
		cpus       int
	}{
		{0, 100, 5, 0, 1},              // Q 下界
		{1_000_000_001, 100, 5, 0, 1},  // Q 上界
		{20, 0, 5, 0, 1},               // P 下界
		{20, 1_000_000_001, 5, 0, 1},   // P 上界
		{20, 100, 0, 0, 1},             // S 下界
		{20, 100, 1_000_000_001, 0, 1}, // S 上界
		{20, 100, 5, -1, 1},            // B 下界
		{20, 100, 5, 1_000_000_001, 1}, // B 上界
		{20, 100, 5, 0, 0},             // C 下界
		{20, 100, 5, 0, 65},            // C 上界
	}
	for i, tc := range cases {
		if _, err := New(tc.q, tc.p, tc.s, tc.b, tc.cpus); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("用例 %d: 期望 ErrInvalidConfig, 实际 %v", i, err)
		}
	}
	if _, err := New(1, 1, 1, 0, 1); err != nil {
		t.Fatalf("最小合法配置被拒: %v", err)
	}
	if _, err := New(1_000_000_000, 1_000_000_000, 1_000_000_000, 1_000_000_000, 64); err != nil {
		t.Fatalf("最大合法配置被拒: %v", err)
	}
}

// TestErrorPrecedence 错误按序只报第一个：cpu 越界 > 参数非法 > 时间回退 > 状态类。
func TestErrorPrecedence(t *testing.T) {
	c := mustNew(t, 20, 100, 5, 0, 2)
	mustOp(t, c.Wake(10, 0), "Wake(10,0)") // lastNow=10，cpu0 运行中
	// cpu 越界 优先于 参数非法
	mustReject(t, c.Wake(-1, 9), ErrCPUOutOfRange, "Wake(now=-1,cpu=9)")
	mustReject(t, c.Run(2_000_000_000_000_000, 9, 0), ErrCPUOutOfRange, "Run(now 越界,cpu=9,d=0)")
	// 参数非法 优先于 时间回退（now 越界 与 d 越界 同属参数非法）
	mustReject(t, c.Wake(1_000_000_000_000_001, 0), ErrInvalidParam, "Wake(now=1e15+1)")
	mustReject(t, c.Wake(-1, 0), ErrInvalidParam, "Wake(now=-1)")
	mustReject(t, c.Run(5, 0, 0), ErrInvalidParam, "Run(d=0)")
	mustReject(t, c.Run(5, 0, 1_000_001), ErrInvalidParam, "Run(d=1e6+1)")
	// 时间回退 优先于 状态类（cpu1 空闲，Wake 本应报状态错误，但先报回退）
	mustReject(t, c.Wake(9, 1), ErrTimeRegression, "Wake(now=9 回退, cpu1 空闲)")
	mustReject(t, c.Run(9, 1, 1), ErrTimeRegression, "Run(now=9 回退, cpu1 空闲)")
	// 状态类：Wake 时不是空闲
	mustReject(t, c.Wake(10, 0), ErrNotIdle, "Wake(运行中)")
	// Run/Idle 时空闲报未运行
	mustReject(t, c.Run(10, 1, 1), ErrNotRunning, "Run(空闲)")
	mustReject(t, c.Idle(10, 1), ErrNotRunning, "Idle(空闲)")
	// now 恰等于 lastNow 允许
	mustOp(t, c.Run(10, 0, 1), "Run(10,0,1)")
}

// TestThrottledStateErrors 被节流 CPU 的 Run/Idle 报被节流，Wake 报状态错误。
func TestThrottledStateErrors(t *testing.T) {
	c := mustNew(t, 5, 100, 2, 0, 1)
	mustOp(t, c.Wake(0, 0), "Wake")
	mustOp(t, c.Run(1, 0, 7), "Run(1,0,7)") // l=-7, want=9, take=5, l=-2 节流
	mustState(t, c, 0, Throttled, -2, 1)
	mustReject(t, c.Run(2, 0, 1), ErrThrottled, "Run(被节流)")
	mustReject(t, c.Idle(2, 0), ErrThrottled, "Idle(被节流)")
	mustReject(t, c.Wake(2, 0), ErrNotIdle, "Wake(被节流)")
	// 边界 100：G=5，need=3，take=3，l=1 解除，G=2；
	// 同一时刻可运行：l=0，want=2，take=2，l=2
	mustOp(t, c.Run(100, 0, 1), "Run(100,0,1)")
	mustState(t, c, 0, Running, 2, 1)
}
