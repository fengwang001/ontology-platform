// Package order 生成两阶段禁选的完整行动步序。
package order

// Kind 表示一个步的类型。
type Kind int

const (
	KindBan  Kind = iota // 禁用步
	KindPick             // 选人步
)

// Step 描述完整步序中的一步：行动队（1 或 2）与步类型。
type Step struct {
	Team int
	Kind Kind
}

// Config 为步序生成参数：每队人数、一阶段每队禁用数、一阶段选手数、二阶段每队禁用数。
type Config struct {
	N  int
	B1 int
	C  int
	B2 int
}

// PickTeam 返回选人第 i 手（i 从 0 起）所属队伍：i%4 为 0 或 3 时归队 1，否则队 2。
func PickTeam(i int) int {
	r := i % 4
	if r < 0 {
		r += 4
	}
	if r == 0 || r == 3 {
		return 1
	}
	return 2
}

// Build 按 Config 生成完整步序。参数合法性由上层 draft 保证。
func Build(cfg Config) []Step {
	steps := make([]Step, 0, 2*cfg.N+2*(cfg.B1+cfg.B2))

	// 一阶段禁用：队 1 先，两队交替，每队 B1 步。
	for i := 0; i < 2*cfg.B1; i++ {
		steps = append(steps, Step{Team: 1 + i%2, Kind: KindBan})
	}

	// 选人第 0 到 C-1 手。
	for i := 0; i < cfg.C; i++ {
		steps = append(steps, Step{Team: PickTeam(i), Kind: KindPick})
	}

	// 二阶段禁用：由第 C 手所属队先禁，两队交替，每队 B2 步。
	if cfg.B2 > 0 {
		first := PickTeam(cfg.C)
		for i := 0; i < 2*cfg.B2; i++ {
			team := first
			if i%2 != 0 {
				team = 3 - first
			}
			steps = append(steps, Step{Team: team, Kind: KindBan})
		}
	}

	// 选人第 C 到 2N-1 手。
	for i := cfg.C; i < 2*cfg.N; i++ {
		steps = append(steps, Step{Team: PickTeam(i), Kind: KindPick})
	}

	return steps
}
