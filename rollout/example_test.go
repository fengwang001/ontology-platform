package rollout_test

import (
	"fmt"

	"ontology/rollout"
)

func Example() {
	cfg := rollout.Config{
		Ratios:      []int{1000, 5000, 10000}, // 10% -> 50% -> 100%
		DwellMs:     60_000,                   // 每阶段至少驻留 60s
		MinCanary:   100,                      // 灰度至少 100 个请求才评估
		ToleranceBP: 500,                      // 灰度错误率不得高于稳定 + 5%
		MaxFailures: 3,                        // 连续 3 次失败自动回滚
		StickyMs:    30_000,                   // 会话粘性 30s
		MaxSticky:   1_000_000,
	}
	s, err := rollout.New(cfg)
	if err != nil {
		panic(err)
	}

	// 位置可独立查询：恒为 [0,10000) 的确定值。
	pos := s.Position("user-123")

	if err := s.Start(1_000); err != nil { // 第 0 阶段，起点 t=1000
		panic(err)
	}
	decision, err := s.Route("user-123", 1_001)
	if err != nil {
		panic(err)
	}
	fmt.Println(0 <= pos && pos < 10000)
	if pos < 1000 {
		fmt.Println(decision.Version == rollout.VersionCanary)
	} else {
		fmt.Println(decision.Version == rollout.VersionStable)
	}

	// 记录观测：只有进行中状态才计入当前评估窗口。
	_ = s.Observe(rollout.VersionCanary, true, 2_000)

	// 驻留未满 -> DwellNotMet；满 D 且样本足、指标达标 -> Passed。
	r, err := s.Evaluate(61_001)
	if err != nil {
		panic(err)
	}
	fmt.Println(r) // 样本不足时为 insufficient_sample

	// 任意时刻可人工重置（回到未开始），或在阶段 > 0 时降一级。
	s.Reset()
	fmt.Println(s.Snapshot().Phase)

	// Output:
	// true
	// true
	// insufficient_sample
	// not_started
}
