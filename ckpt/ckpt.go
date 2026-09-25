// Package ckpt 提供单 offset 的幂等判定与连续前缀推进规则。
// 它不依赖任何其他包，也不持有状态。
package ckpt

// Decision 是对一个 incoming offset 的幂等判定结果。
type Decision int

const (
	// Accept 表示新事件，应放入 pending。
	Accept Decision = iota
	// SkipPersisted 表示 offset <= cp，已持久化，幂等跳过。
	SkipPersisted
	// SkipInFlight 表示 offset 已在 pending 中，在途重复，幂等跳过。
	SkipInFlight
)

// Decide 判定 offset 是新事件还是重复。inFlight 为「offset 是否已在 pending 中」。
// 重复防护由「offset <= cp」与「offset 在 pending 中」两个判据共同保证。
func Decide(offset, cp int64, inFlight bool) Decision {
	if offset <= cp {
		return SkipPersisted
	}
	if inFlight {
		return SkipInFlight
	}
	return Accept
}

// Fold 从 cp+1 起，凡 consume 报告存在的 offset 逐个推进 cp，遇到第一个缺口即停。
// consume 返回 true 表示该 offset 存在（已被消费），false 表示缺口。
// cp 只沿连续前缀推进，绝不跳过缺口。
func Fold(cp int64, consume func(offset int64) bool) int64 {
	for consume(cp + 1) {
		cp++
	}
	return cp
}
