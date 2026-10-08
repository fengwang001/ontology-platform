// Package naive 是历史展开的独立朴素参考实现：
// 全部事实与全部版本都以无序切片保存，展开时线性扫描。
// 它与 ontology.Store 不共享任何查询逻辑，用于在随机操作
// 序列下与快速实现逐条对照，验证快速实现的正确性。
package naive

import (
	"fmt"
	"sort"

	"ontology/ontology"
)

// Model 是朴素参考模型。
type Model struct {
	props    map[string]ontology.PropertyDef
	versions []ontology.SchemaVersion
	facts    []ontology.Fact
	nextID   int64
}

func New(props map[string]ontology.PropertyDef, effectiveFrom ontology.RecordTime) *Model {
	return &Model{
		versions: []ontology.SchemaVersion{{ID: 1, Props: props, From: effectiveFrom, To: ontology.RecordTime(1<<62 - 1)}},
		nextID:   2,
	}
}

// WriteFact 追加一条事实（假定记录时间单调，与快速实现的前置条件一致）。
func (m *Model) WriteFact(f ontology.Fact) {
	m.facts = append(m.facts, f)
}

// Migrate 朴素迁移：校验线性扫描，版本序列重建。
func (m *Model) Migrate(mg ontology.Migration) error {
	for _, f := range m.facts {
		if f.RecordTime < mg.EffectiveFrom {
			continue
		}
		for name, pd := range mg.NewProps {
			v, present := f.Values[name]
			if !present {
				if pd.Required {
					return fmt.Errorf("required property %q missing", name)
				}
				continue
			}
			if _, ok := v.CoerceTo(pd.Type); !ok {
				return fmt.Errorf("property %q not coercible", name)
			}
		}
	}
	nv := ontology.SchemaVersion{
		ID:    m.nextID,
		Props: mg.NewProps,
		From:  mg.EffectiveFrom,
		To:    ontology.RecordTime(1<<62 - 1),
	}
	m.nextID++
	kept := m.versions[:0]
	for _, v := range m.versions {
		if v.From >= mg.EffectiveFrom {
			continue
		}
		if v.To > mg.EffectiveFrom {
			v.To = mg.EffectiveFrom
		}
		kept = append(kept, v)
	}
	m.versions = append(kept, nv)
	return nil
}

// Expand 朴素展开：线性扫描全部事实与全部版本。
func (m *Model) Expand(req ontology.ExpandRequest) ontology.ExpandResult {
	// 每个有效时间取记录时刻 <= AsOfRecord 的最新一条修正。
	latest := make(map[ontology.ValidTime]ontology.Fact)
	for _, f := range m.facts {
		if f.RecordTime > req.AsOfRecord {
			continue
		}
		if f.ValidTime < req.ValidFrom || f.ValidTime > req.ValidTo {
			continue
		}
		cur, ok := latest[f.ValidTime]
		if !ok || f.RecordTime >= cur.RecordTime {
			latest[f.ValidTime] = f
		}
	}
	vts := make([]int, 0, len(latest))
	for vt := range latest {
		vts = append(vts, int(vt))
	}
	sort.Ints(vts)

	allProps := map[string]struct{}{}
	for _, v := range m.versions {
		for name := range v.Props {
			allProps[name] = struct{}{}
		}
	}

	res := ontology.ExpandResult{ObjectID: req.ObjectID}
	for _, vt := range vts {
		f := latest[ontology.ValidTime(vt)]
		sv, ok := m.versionAt(f.RecordTime)
		if !ok {
			continue
		}
		view := ontology.FactView{
			ValidTime:       f.ValidTime,
			RecordTime:      f.RecordTime,
			SchemaVersionID: sv.ID,
			Props:           make(map[string]ontology.PropStatus),
		}
		for name := range allProps {
			pd, existed := sv.Props[name]
			if !existed {
				view.Props[name] = ontology.PropStatus{Status: ontology.StatusNotApplicable}
				continue
			}
			if v, present := f.Values[name]; present {
				if cv, ok := v.CoerceTo(pd.Type); ok {
					view.Props[name] = ontology.PropStatus{Status: ontology.StatusValue, Value: cv}
				} else {
					view.Props[name] = ontology.PropStatus{Status: ontology.StatusValue, Value: v}
				}
				continue
			}
			if pd.Required {
				view.Props[name] = ontology.PropStatus{Status: ontology.StatusMissingRequired}
			} else {
				view.Props[name] = ontology.PropStatus{Status: ontology.StatusUnsetOptional}
			}
		}
		res.Facts = append(res.Facts, view)
	}
	return res
}

// versionAt 线性查找记录时刻落入的版本（左闭右开）。
func (m *Model) versionAt(rt ontology.RecordTime) (ontology.SchemaVersion, bool) {
	for _, v := range m.versions {
		if v.Contains(rt) {
			return v, true
		}
	}
	return ontology.SchemaVersion{}, false
}
