package compat

import "fmt"

// JudgePath 执行跨版本的传递性逐级核对：
// 从消费方编写版本出发，沿 chain 逐级判定至快照版本，
// 每一级给出独立判定依据，命中失败即停止，不跳过中间版本。
//
// chain 必须包含消费方 At 版本与快照版本对应的 Format，
// 且相邻版本在演化序列中相邻（允许正向或反向）。
//
// 版本号落在消费方可识别范围之外时直接拒绝，不做任何属性级判定。
// 兼容性不具有传递性：本函数不对中间结果做任何传递推断，
// 每一级都独立地把“消费方编写版本”与“该级版本”的净变化完整判定一遍。
func JudgePath(profile Profile, chain []Format, snap Snapshot) ChainVerdict {
	// 第一优先级：版本号范围判定，命中即拒绝。
	if !profile.Accepts.Contains(snap.Version) {
		return ChainVerdict{Final: Verdict{
			Level:    LevelIncompatible,
			Category: CatVersionOutOfRange,
			Issues: []Issue{{
				Category: CatVersionOutOfRange,
				Detail: fmt.Sprintf("快照版本 %s 超出消费方 %s 可识别范围 [%s, %s]",
					snap.Version, profile.Name, profile.Accepts.Min, profile.Accepts.Max),
			}},
		}}
	}

	atIdx := indexOfVersion(chain, profile.At)
	toIdx := indexOfVersion(chain, snap.Version)
	if atIdx < 0 || toIdx < 0 {
		return ChainVerdict{Final: Verdict{
			Level:    LevelIncompatible,
			Category: CatVersionOutOfRange,
			Issues: []Issue{{
				Category: CatVersionOutOfRange,
				Detail:   "消费方编写版本或快照版本不在演化链中，无法逐级核对",
			}},
		}}
	}

	dataOlder := Compare(snap.Version, profile.At) < 0
	out := ChainVerdict{}
	net := map[string]map[string]netProp{}

	record := func(from, to Version, verdict Verdict) bool {
		out.Steps = append(out.Steps, StepVerdict{From: from, To: to, Verdict: verdict})
		return verdict.Level == LevelIncompatible
	}
	if atIdx < toIdx {
		// 正向：跨过版本 i 使用 chain[i].Changes。
		for i := atIdx + 1; i <= toIdx; i++ {
			applyChanges(net, chain[atIdx].Schema, chain[i].Changes)
			if record(chain[i-1].Version, chain[i].Version, evaluate(profile, net, snap, dataOlder)) {
				break
			}
		}
	} else {
		// 反向：跨过版本 i 使用 chain[i].Changes 的逆向形式。
		for i := atIdx; i > toIdx; i-- {
			applyChanges(net, chain[atIdx].Schema, invertAll(chain[i].Changes))
			if record(chain[i].Version, chain[i-1].Version, evaluate(profile, net, snap, dataOlder)) {
				break
			}
		}
	}

	if len(out.Steps) == 0 {
		// 消费方编写版本即快照版本：无变化，直接兼容。
		out.Final = Verdict{Level: LevelCompatible}
		return out
	}
	out.Final = out.Steps[len(out.Steps)-1].Verdict
	return out
}
