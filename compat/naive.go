package compat

import (
	"fmt"
	"reflect"
	"sort"
)

// NaiveJudge 是朴素参照模型：不依赖变更日志，直接对 from/to 两个版本的
// 全量 Schema 做差集，并逐条独立应用兼容规则。其实现刻意保持简单直接，
// 用于在随机演化序列上与 Judge/JudgePath 的结果做对照。
//
// 与优化实现共用 Verdict 语义，但规则判定代码独立书写，
// 且开销与属性总数成正比（Stats.PropertiesInspected 为对比过的全部属性数）。
func NaiveJudge(profile Profile, from, to Format, snap Snapshot) Verdict {
	if !profile.Accepts.Contains(snap.Version) {
		return Verdict{
			Level:    LevelIncompatible,
			Category: CatVersionOutOfRange,
			Issues: []Issue{{
				Category: CatVersionOutOfRange,
				Detail: fmt.Sprintf("快照版本 %s 超出消费方 %s 可识别范围 [%s, %s]",
					snap.Version, profile.Name, profile.Accepts.Min, profile.Accepts.Max),
			}},
		}
	}

	dataOlder := Compare(snap.Version, profile.At) < 0

	// 全量差集：逐一对比两个版本 Schema 中的全部属性。
	var reqMissing, typeIncompat, deletedPresent []Issue
	var notes []string
	degraded := false
	inspected := 0

	objTypes := map[string]bool{}
	for name := range from.Schema {
		objTypes[name] = true
	}
	for name := range to.Schema {
		objTypes[name] = true
	}
	objNames := make([]string, 0, len(objTypes))
	for name := range objTypes {
		objNames = append(objNames, name)
	}
	sort.Strings(objNames)

	for _, objectType := range objNames {
		propNames := map[string]bool{}
		for p := range from.Schema[objectType] {
			propNames[p] = true
		}
		for p := range to.Schema[objectType] {
			propNames[p] = true
		}
		props := make([]string, 0, len(propNames))
		for p := range propNames {
			props = append(props, p)
		}
		sort.Strings(props)

		for _, property := range props {
			inspected++
			oldP, oldOK := lookupProp(from.Schema, objectType, property)
			newP, newOK := lookupProp(to.Schema, objectType, property)
			switch {
			case !oldOK && newOK:
				if newP.Required {
					if profile.Mode == ModeWrite {
						reqMissing = append(reqMissing, Issue{CatRequiredMissing, objectType, property,
							"新增必填属性：写入场景下禁止产出缺失该属性的对象"})
					} else {
						degraded = true
						notes = append(notes, fmt.Sprintf("%s.%s: 新增必填属性，读取时降级忽略", objectType, property))
					}
				} else {
					notes = append(notes, fmt.Sprintf("%s.%s: 新增可选属性，读取时忽略", objectType, property))
				}
			case oldOK && !newOK:
				if profile.dependsOn(objectType, property) {
					reqMissing = append(reqMissing, Issue{CatRequiredMissing, objectType, property,
						"属性已在数据格式中删除，消费方仍依赖该属性"})
				} else {
					notes = append(notes, fmt.Sprintf("%s.%s: 属性已删除，消费方声明不依赖，忽略", objectType, property))
				}
				for _, obj := range snap.Data[objectType] {
					if _, ok := obj[property]; ok {
						deletedPresent = append(deletedPresent, Issue{CatDeletedPropertyPresent, objectType, property,
							"属性已被声明删除，但快照数据中仍出现取值"})
						break
					}
				}
			case oldOK && newOK && !reflect.DeepEqual(oldP, newP):
				if !SubsetOf(newP.Type, oldP.Type) {
					proof := true
					for _, obj := range snap.Data[objectType] {
						if v, ok := obj[property]; ok && !oldP.Type.Contains(v) {
							proof = false
							break
						}
					}
					if proof {
						notes = append(notes, fmt.Sprintf("%s.%s: 取值类型变化，已由实际数据证明兼容", objectType, property))
					} else {
						typeIncompat = append(typeIncompat, Issue{CatTypeIncompatible, objectType, property,
							"取值类型收紧且实际数据中存在超出新范围的取值"})
					}
				}
				switch {
				case oldP.Required && !newP.Required:
					if dataOlder {
						for _, obj := range snap.Data[objectType] {
							if _, ok := obj[property]; !ok {
								reqMissing = append(reqMissing, Issue{CatRequiredMissing, objectType, property,
									"消费方要求该属性必填，但历史数据中存在缺失该属性的对象"})
								break
							}
						}
					}
				case !oldP.Required && newP.Required:
					if profile.Mode == ModeWrite {
						reqMissing = append(reqMissing, Issue{CatRequiredMissing, objectType, property,
							"属性已改为必填：写入场景下禁止产出缺失该属性的对象"})
					}
				}
			}
		}
	}

	v := Verdict{Stats: Stats{PropertiesInspected: inspected}}
	switch {
	case len(reqMissing) > 0:
		v.Level, v.Category, v.Issues = LevelIncompatible, CatRequiredMissing, reqMissing
	case len(typeIncompat) > 0:
		v.Level, v.Category, v.Issues = LevelIncompatible, CatTypeIncompatible, typeIncompat
	case len(deletedPresent) > 0:
		v.Level, v.Category, v.Issues = LevelIncompatible, CatDeletedPropertyPresent, deletedPresent
	case degraded:
		v.Level, v.Notes = LevelDegraded, notes
	default:
		v.Level, v.Notes = LevelCompatible, notes
	}
	return v
}

// NaiveJudgePath 是 JudgePath 的朴素参照：逐级调用 NaiveJudge，
// 命中失败即停止，不跳过中间版本。
func NaiveJudgePath(profile Profile, chain []Format, snap Snapshot) ChainVerdict {
	if !profile.Accepts.Contains(snap.Version) {
		return ChainVerdict{Final: NaiveJudge(profile, Format{Version: profile.At}, Format{}, snap)}
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
	out := ChainVerdict{}
	step := 1
	if toIdx < atIdx {
		step = -1
	}
	for i := atIdx + step; atIdx != toIdx; i += step {
		v := NaiveJudge(profile, chain[atIdx], chain[i], snap)
		out.Steps = append(out.Steps, StepVerdict{
			From:    chain[i-step].Version,
			To:      chain[i].Version,
			Verdict: v,
		})
		if v.Level == LevelIncompatible || i == toIdx {
			break
		}
	}
	if len(out.Steps) == 0 {
		out.Final = Verdict{Level: LevelCompatible}
		return out
	}
	out.Final = out.Steps[len(out.Steps)-1].Verdict
	return out
}
