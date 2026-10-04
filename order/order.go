// Package order 生成两阶段禁选的完整步序。
package order

// Kind 表示一步的类型：禁用或选人。
type Kind int

const (
	Ban Kind = iota
	Pick
)

// Step 表示一步行动，恰由一队执行。
type Step struct {
	Kind Kind
	Team int // 行动队：1 或 2
	Hand int // 仅 Pick 有效：第几手选人（0 起）
}

// PickTeam 返回第 i 手（0 起）选人所属的队：
// i 除以 4 余 0 或 3 时归队 1，否则归队 2。
func PickTeam(i int) int {
	if i%4 == 0 || i%4 == 3 {
		return 1
	}
	return 2
}

// Steps 生成完整步序：一阶段禁用 2*b1 步（队 1 先，交替），选人第 0..c-1 手，
// 二阶段禁用 2*b2 步（第 c 手所属队先禁，交替），选人第 c..2n-1 手。
func Steps(n, b1, c, b2 int) []Step {
	steps := make([]Step, 0, 2*(b1+b2+n))
	for i := 0; i < 2*b1; i++ {
		steps = append(steps, Step{Kind: Ban, Team: 1 + i%2})
	}
	for i := 0; i < c; i++ {
		steps = append(steps, Step{Kind: Pick, Team: PickTeam(i), Hand: i})
	}
	for i := 0; i < 2*b2; i++ {
		team := PickTeam(c)
		if i%2 == 1 {
			team = 3 - team
		}
		steps = append(steps, Step{Kind: Ban, Team: team})
	}
	for i := c; i < 2*n; i++ {
		steps = append(steps, Step{Kind: Pick, Team: PickTeam(i), Hand: i})
	}
	return steps
}
