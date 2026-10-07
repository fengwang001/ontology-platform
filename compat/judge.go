package compat

import (
	"fmt"
	"sort"
)

// Judge 执行一次属性级兼容性判定：编写于 profile.At 版本格式的消费方，
// 尝试消费 to 版本格式的快照。
//
// 判定遵循固定优先级：
// 版本范围 > 必填缺失 > 类型不兼容 > 删除属性仍出现取值。
// 等价于 JudgePath 的最终结论；逐级依据见 JudgePath。
//
// Judge 不修改任何输入，可安全并发调用。
func Judge(profile Profile, chain []Format, snap Snapshot) Verdict {
	return JudgePath(profile, chain, snap).Final
}

// evaluate 对一组净变化应用属性级兼容规则，是规则引擎的核心。
// dataOlder 表示快照数据是否早于消费方编写版本（读取历史数据）。
//
// 输出确定性：净变化按对象类型名、属性名排序后逐条判定，
// 保证相同输入产生逐字节相同的判定依据。
func evaluate(profile Profile, net map[string]map[string]netProp, snap Snapshot, dataOlder bool) Verdict {
	var reqMissing, typeIncompat, deletedPresent []Issue
	var notes []string
	degraded := false
	inspected := 0

	for _, objectType := range sortedKeys(net) {
		props := net[objectType]
		for _, property := range sortedKeys(props) {
			np := props[property]
			inspected++
			switch {
			case !np.oldPresent && np.newPresent:
				// 新增属性：旧格式消费方视为可忽略信息。
				if np.newProp.Required {
					if profile.Mode == ModeWrite {
						reqMissing = append(reqMissing, Issue{
							Category:   CatRequiredMissing,
							ObjectType: objectType,
							Property:   property,
							Detail:     "新增必填属性：写入场景下禁止产出缺失该属性的对象",
						})
					} else {
						// 读取场景允许忽略，但属于降级处理。
						degraded = true
						notes = append(notes, fmt.Sprintf("%s.%s: 新增必填属性，读取时降级忽略", objectType, property))
					}
				} else {
					notes = append(notes, fmt.Sprintf("%s.%s: 新增可选属性，读取时忽略", objectType, property))
				}
			case np.oldPresent && !np.newPresent:
				// 删除属性：消费方仍依赖则不兼容；声明不依赖则可忽略。
				if profile.dependsOn(objectType, property) {
					reqMissing = append(reqMissing, Issue{
						Category:   CatRequiredMissing,
						ObjectType: objectType,
						Property:   property,
						Detail:     "属性已在数据格式中删除，消费方仍依赖该属性",
					})
				} else {
					notes = append(notes, fmt.Sprintf("%s.%s: 属性已删除，消费方声明不依赖，忽略", objectType, property))
				}
				// 已声明删除的属性若仍在数据中出现取值，必须报告格式不一致。
				if dataHasProp(snap, objectType, property) {
					deletedPresent = append(deletedPresent, Issue{
						Category:   CatDeletedPropertyPresent,
						ObjectType: objectType,
						Property:   property,
						Detail:     "属性已被声明删除，但快照数据中仍出现取值",
					})
				}
			default:
				// 两侧都存在的属性：检查取值类型与必填性变化。
				if !SubsetOf(np.newProp.Type, np.oldProp.Type) {
					// 数据侧类型不完全落在消费方理解的范围内：
					// 用实际数据证明全部既有取值仍可被消费方理解。
					if allValuesWithin(snap, objectType, property, np.oldProp.Type) {
						notes = append(notes, fmt.Sprintf("%s.%s: 取值类型变化，已由实际数据证明兼容", objectType, property))
					} else {
						typeIncompat = append(typeIncompat, Issue{
							Category:   CatTypeIncompatible,
							ObjectType: objectType,
							Property:   property,
							Detail:     "取值类型收紧且实际数据中存在超出新范围的取值",
						})
					}
				}
				switch {
				case np.oldProp.Required && !np.newProp.Required:
					// 必填改非必填：对任何消费方兼容；
					// 但读取历史数据且消费方仍要求该属性时，须按历史数据是否始终存在判定。
					if dataOlder && !allObjectsHaveProp(snap, objectType, property) {
						reqMissing = append(reqMissing, Issue{
							Category:   CatRequiredMissing,
							ObjectType: objectType,
							Property:   property,
							Detail:     "消费方要求该属性必填，但历史数据中存在缺失该属性的对象",
						})
					}
				case !np.oldProp.Required && np.newProp.Required:
					// 非必填改必填：写入场景下消费方无法保证产出该属性。
					if profile.Mode == ModeWrite {
						reqMissing = append(reqMissing, Issue{
							Category:   CatRequiredMissing,
							ObjectType: objectType,
							Property:   property,
							Detail:     "属性已改为必填：写入场景下禁止产出缺失该属性的对象",
						})
					}
				}
			}
		}
	}

	v := Verdict{Stats: Stats{PropertiesInspected: inspected}}
	// 固定优先级：必填缺失 > 类型不兼容 > 删除属性仍出现取值。
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

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func dataHasProp(snap Snapshot, objectType, property string) bool {
	for _, obj := range snap.Data[objectType] {
		if _, ok := obj[property]; ok {
			return true
		}
	}
	return false
}

func allObjectsHaveProp(snap Snapshot, objectType, property string) bool {
	for _, obj := range snap.Data[objectType] {
		if _, ok := obj[property]; !ok {
			return false
		}
	}
	return true
}

func allValuesWithin(snap Snapshot, objectType, property string, t TypeSpec) bool {
	for _, obj := range snap.Data[objectType] {
		if v, ok := obj[property]; ok && !t.Contains(v) {
			return false
		}
	}
	return true
}
