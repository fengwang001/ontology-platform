// Package naive 是双时态链接历史的独立朴素对照实现。
//
// 它不共享 ontology 包的任何内部状态结构：不做快照、不做增量，
// 每次查询都从头线性扫描全部事实。正确性优先于效率，
// 用于在随机操作序列下与主实现逐条对照（差分测试）。
package naive

import (
	"sort"

	"ontology/ontology"
)

// Fact 朴素模型的事实记录（字段全部导出，与主实现内部表示无关）。
type Fact struct {
	RecordedAt   int64
	Kind         ontology.FactKind
	A, B         ontology.ObjectID
	ValidFrom    int64
	ValidTo      int64
	EndorsedBoth bool
}

// Model 单个链接类型的朴素双时态模型。
type Model struct {
	Def      ontology.LinkTypeDef
	Facts    []Fact
	Versions []ontology.ConstraintVersion
}

// NewModel 创建朴素模型，初始约束版本为 1（双侧不限，自时刻 0 生效）。
func NewModel(def ontology.LinkTypeDef) *Model {
	return &Model{
		Def: def,
		Versions: []ontology.ConstraintVersion{{
			Version:       1,
			EffectiveFrom: 0,
			Left:          ontology.Cardinality{Min: 0, Max: -1},
			Right:         ontology.Cardinality{Min: 0, Max: -1},
		}},
	}
}

// AdjustCardinality 追加一个自 at 起生效的约束版本。
func (m *Model) AdjustCardinality(at int64, left, right ontology.Cardinality) {
	m.Versions = append(m.Versions, ontology.ConstraintVersion{
		Version:       len(m.Versions) + 1,
		EffectiveFrom: at,
		Left:          left,
		Right:         right,
	})
}

// Apply 追加一条事实。
func (m *Model) Apply(f Fact) {
	m.Facts = append(m.Facts, f)
}

type iv struct{ from, to int64 }

const openEnd = int64(^uint64(0) >> 1)

// canon 对称链接按字典序规范化键。
func (m *Model) canon(a, b ontology.ObjectID) [2]ontology.ObjectID {
	if m.Def.Symmetric && b < a {
		return [2]ontology.ObjectID{b, a}
	}
	return [2]ontology.ObjectID{a, b}
}

// LinksAt 线性扫描全部事实，返回 (rt, vt) 时刻存在的链接及缺损标记。
func (m *Model) LinksAt(rt, vt int64) (links [][2]ontology.ObjectID, deficit map[[2]ontology.ObjectID]bool) {
	type state struct {
		ivs     []iv
		deficit bool
	}
	states := map[[2]ontology.ObjectID]*state{}
	for _, f := range m.Facts {
		if f.RecordedAt > rt {
			continue
		}
		k := m.canon(f.A, f.B)
		st, ok := states[k]
		if !ok {
			st = &state{}
			states[k] = st
		}
		open := -1
		for i := len(st.ivs) - 1; i >= 0; i-- {
			if st.ivs[i].to == openEnd {
				open = i
				break
			}
		}
		switch f.Kind {
		case ontology.FactCreate:
			if open < 0 {
				st.ivs = append(st.ivs, iv{from: f.ValidFrom, to: openEnd})
			}
			if m.Def.Symmetric {
				st.deficit = !f.EndorsedBoth
			}
		case ontology.FactCorroborate:
			st.deficit = false
		case ontology.FactRevoke:
			if open >= 0 {
				st.ivs[open].to = f.ValidTo
			}
		}
	}
	deficit = map[[2]ontology.ObjectID]bool{}
	for k, st := range states {
		for _, span := range st.ivs {
			if span.from <= vt && vt < span.to {
				links = append(links, k)
				if st.deficit {
					deficit[k] = true
				}
				break
			}
		}
	}
	sort.Slice(links, func(i, j int) bool {
		if links[i][0] != links[j][0] {
			return links[i][0] < links[j][0]
		}
		return links[i][1] < links[j][1]
	})
	return links, deficit
}

// versionAt 返回记录时刻 t 生效的约束版本（线性扫描）。
func (m *Model) versionAt(t int64) ontology.ConstraintVersion {
	v := m.Versions[0]
	for _, cand := range m.Versions {
		if cand.EffectiveFrom <= t {
			v = cand
		}
	}
	return v
}

// ViolatorsAt 返回记录时刻 rt、有效时刻 vt 下违反基数的对象及其度数。
// 对称链接两侧共用 Left 约束，与主实现约定一致。
func (m *Model) ViolatorsAt(rt, vt int64) map[ontology.ObjectID]int {
	links, _ := m.LinksAt(rt, vt)
	degree := map[ontology.ObjectID]int{}
	asLeft := map[ontology.ObjectID]bool{}
	asRight := map[ontology.ObjectID]bool{}
	for _, l := range links {
		degree[l[0]]++
		degree[l[1]]++
		asLeft[l[0]] = true
		asRight[l[1]] = true
	}
	ver := m.versionAt(rt)
	out := map[ontology.ObjectID]int{}
	for obj, d := range degree {
		if d == 0 {
			continue
		}
		if asLeft[obj] || m.Def.Symmetric {
			if violates(ver.Left, d) {
				out[obj] = d
				continue
			}
		}
		if asRight[obj] && !m.Def.Symmetric && violates(ver.Right, d) {
			out[obj] = d
		}
	}
	return out
}

func violates(c ontology.Cardinality, degree int) bool {
	if degree == 0 {
		return false
	}
	if degree < c.Min {
		return true
	}
	return c.Max >= 0 && degree > c.Max
}
