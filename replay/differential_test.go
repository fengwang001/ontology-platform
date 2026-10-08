package replay

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// 本文件实现随机差异对照测试：在大量随机构造的快照与差异记录组合上，
// 对照优化实现与朴素参照模型的判定结果，并记录每次校验的输入、输出与判定依据。

var propTypes = []string{PropString, PropInt, PropBool}

func randPropValue(r *rand.Rand, t string) any {
	switch t {
	case PropString:
		return fmt.Sprintf("s%d", r.Intn(7))
	case PropInt:
		return r.Intn(11)
	default:
		return r.Intn(2) == 0
	}
}

func randObjectType(r *rand.Rand, name string) ObjectType {
	props := map[string]string{}
	for i := 0; i < r.Intn(4); i++ {
		props[fmt.Sprintf("p%c", 'a'+rune(i))] = propTypes[r.Intn(len(propTypes))]
	}
	return ObjectType{Name: name, Properties: props}
}

func randObject(r *rand.Rand, id string, t ObjectType) ObjectInstance {
	props := map[string]any{}
	for p, pt := range t.Properties {
		props[p] = randPropValue(r, pt)
	}
	return ObjectInstance{ID: id, Type: t.Name, Properties: props}
}

// genSnapshot 随机生成一份快照。
func genSnapshot(r *rand.Rand) *Snapshot {
	nTypes := 1 + r.Intn(3)
	objTypes := map[string]ObjectType{}
	typeNames := []string{}
	for i := 0; i < nTypes; i++ {
		name := fmt.Sprintf("T%d", i)
		objTypes[name] = randObjectType(r, name)
		typeNames = append(typeNames, name)
	}
	linkTypes := map[string]LinkType{}
	linkNames := []string{}
	for i := 0; i < r.Intn(3); i++ {
		name := fmt.Sprintf("L%d", i)
		lt := LinkType{
			Name:       name,
			SourceType: typeNames[r.Intn(len(typeNames))],
			TargetType: typeNames[r.Intn(len(typeNames))],
		}
		if r.Intn(2) == 0 {
			lt.Constraint.MaxOutgoingPerSource = 1 + r.Intn(2)
		}
		if r.Intn(2) == 0 {
			lt.Constraint.MaxIncomingPerTarget = 1 + r.Intn(2)
		}
		linkTypes[name] = lt
		linkNames = append(linkNames, name)
	}
	objects := map[string]ObjectInstance{}
	objIDs := []string{}
	for i := 0; i < r.Intn(7); i++ {
		id := fmt.Sprintf("o%d", i)
		t := objTypes[typeNames[r.Intn(len(typeNames))]]
		objects[id] = randObject(r, id, t)
		objIDs = append(objIDs, id)
	}
	links := map[string]LinkInstance{}
	if len(objIDs) > 0 {
		for i := 0; i < r.Intn(6); i++ {
			if len(linkNames) == 0 {
				break
			}
			id := fmt.Sprintf("k%d", i)
			links[id] = LinkInstance{
				ID:     id,
				Type:   linkNames[r.Intn(len(linkNames))],
				Source: objIDs[r.Intn(len(objIDs))],
				Target: objIDs[r.Intn(len(objIDs))],
			}
		}
	}
	return NewSnapshot(Schema{ObjectTypes: objTypes, LinkTypes: linkTypes}, objects, links)
}

// genDelta 随机生成一份差异记录。以 planned（快照深拷贝）为基准抽取
// 变化前状态，使大多数变化接近合法；ordered 为 true 时结构层变化集中在
// 实例层变化之前，提高合法差异记录的比例。
func genDelta(r *rand.Rand, snap *Snapshot) *Delta {
	planned := CloneSnapshot(snap)
	ordered := r.Intn(2) == 0
	n := r.Intn(8)
	changes := make([]Change, 0, n)
	fresh := 0

	objTypeNames := func() []string {
		names := []string{}
		for name := range planned.Schema.ObjectTypes {
			names = append(names, name)
		}
		return names
	}
	linkTypeNames := func() []string {
		names := []string{}
		for name := range planned.Schema.LinkTypes {
			names = append(names, name)
		}
		return names
	}
	objectIDs := func() []string {
		ids := []string{}
		for id := range planned.Objects {
			ids = append(ids, id)
		}
		return ids
	}
	linkIDs := func() []string {
		ids := []string{}
		for id := range planned.Links {
			ids = append(ids, id)
		}
		return ids
	}

	genOne := func(schemaOnly bool) {
		kindPool := []ChangeKind{AddObjectType, AddProperty, SetPropertyType, AddLinkType, SetLinkConstraint, RemoveProperty}
		if !schemaOnly {
			kindPool = append(kindPool,
				CreateObject, UpdateObject, DeleteObject, CreateLink, DeleteLink,
				UpdateObject, CreateObject, CreateLink)
		}
		kind := kindPool[r.Intn(len(kindPool))]
		var ch Change
		switch kind {
		case AddObjectType:
			fresh++
			name := fmt.Sprintf("NT%d", fresh)
			nt := randObjectType(r, name)
			ch = Change{Kind: kind, TypeName: name, DeclaredObjectType: &nt}
		case AddProperty:
			names := objTypeNames()
			if len(names) == 0 {
				return
			}
			tn := names[r.Intn(len(names))]
			fresh++
			ch = Change{Kind: kind, TypeName: tn, Property: fmt.Sprintf("q%d", fresh), AfterPropType: propTypes[r.Intn(3)]}
		case RemoveProperty:
			names := objTypeNames()
			if len(names) == 0 {
				return
			}
			tn := names[r.Intn(len(names))]
			t := planned.Schema.ObjectTypes[tn]
			props := []string{}
			for p := range t.Properties {
				props = append(props, p)
			}
			if len(props) == 0 {
				return
			}
			p := props[r.Intn(len(props))]
			ch = Change{Kind: kind, TypeName: tn, Property: p, BeforePropType: t.Properties[p]}
		case SetPropertyType:
			names := objTypeNames()
			if len(names) == 0 {
				return
			}
			tn := names[r.Intn(len(names))]
			t := planned.Schema.ObjectTypes[tn]
			props := []string{}
			for p := range t.Properties {
				props = append(props, p)
			}
			if len(props) == 0 {
				return
			}
			p := props[r.Intn(len(props))]
			ch = Change{Kind: kind, TypeName: tn, Property: p,
				BeforePropType: t.Properties[p], AfterPropType: propTypes[r.Intn(3)]}
		case AddLinkType:
			fresh++
			name := fmt.Sprintf("NL%d", fresh)
			names := objTypeNames()
			if len(names) == 0 {
				return
			}
			lt := LinkType{Name: name, SourceType: names[r.Intn(len(names))], TargetType: names[r.Intn(len(names))]}
			if r.Intn(2) == 0 {
				lt.Constraint.MaxOutgoingPerSource = 1
			}
			ch = Change{Kind: kind, TypeName: name, DeclaredLinkType: &lt}
		case SetLinkConstraint:
			names := linkTypeNames()
			if len(names) == 0 {
				return
			}
			tn := names[r.Intn(len(names))]
			cur := planned.Schema.LinkTypes[tn].Constraint
			after := Constraint{}
			if r.Intn(2) == 0 {
				after.MaxOutgoingPerSource = 1 + r.Intn(2)
			}
			if r.Intn(2) == 0 {
				after.MaxIncomingPerTarget = 1 + r.Intn(2)
			}
			ch = Change{Kind: kind, TypeName: tn, BeforeConstraint: &cur, AfterConstraint: &after}
		case CreateObject:
			names := objTypeNames()
			if len(names) == 0 {
				return
			}
			fresh++
			id := fmt.Sprintf("n%d", fresh)
			t := planned.Schema.ObjectTypes[names[r.Intn(len(names))]]
			o := randObject(r, id, t)
			ch = Change{Kind: kind, ObjectAfter: &o}
		case UpdateObject:
			ids := objectIDs()
			if len(ids) == 0 {
				return
			}
			id := ids[r.Intn(len(ids))]
			before := cloneObject(ptr(planned.Objects[id]))
			after := cloneObject(before)
			t := planned.Schema.ObjectTypes[after.Type]
			props := []string{}
			for p := range t.Properties {
				props = append(props, p)
			}
			if len(props) > 0 {
				p := props[r.Intn(len(props))]
				after.Properties[p] = randPropValue(r, t.Properties[p])
			}
			ch = Change{Kind: kind, ObjectBefore: before, ObjectAfter: after}
		case DeleteObject:
			ids := objectIDs()
			if len(ids) == 0 {
				return
			}
			id := ids[r.Intn(len(ids))]
			before := cloneObject(ptr(planned.Objects[id]))
			ch = Change{Kind: kind, ObjectBefore: before}
		case CreateLink:
			names := linkTypeNames()
			ids := objectIDs()
			if len(names) == 0 || len(ids) == 0 {
				return
			}
			fresh++
			l := LinkInstance{
				ID:     fmt.Sprintf("nl%d", fresh),
				Type:   names[r.Intn(len(names))],
				Source: ids[r.Intn(len(ids))],
				Target: ids[r.Intn(len(ids))],
			}
			ch = Change{Kind: kind, LinkAfter: &l}
		case DeleteLink:
			ids := linkIDs()
			if len(ids) == 0 {
				return
			}
			id := ids[r.Intn(len(ids))]
			before := cloneLink(ptr(planned.Links[id]))
			ch = Change{Kind: kind, LinkBefore: before}
		}
		changes = append(changes, ch)
		// 推进 planned，使后续变化的变化前状态取自最新投影。
		_ = naiveApply(planned, len(changes)-1, &changes[len(changes)-1])
	}

	if ordered {
		nSchema := r.Intn(n + 1)
		for i := 0; i < nSchema; i++ {
			genOne(true)
		}
		for i := nSchema; i < n; i++ {
			genOne(false)
		}
	} else {
		for i := 0; i < n; i++ {
			genOne(false)
		}
	}
	return &Delta{Changes: changes}
}

// injectChangeFault 以一定概率向差异记录注入一类故障。
func injectChangeFault(r *rand.Rand, delta *Delta) {
	if len(delta.Changes) == 0 || r.Intn(100) >= 40 {
		return
	}
	switch r.Intn(4) {
	case 0: // 交换两条变化（可能制造顺序错位）
		if len(delta.Changes) >= 2 {
			i, j := r.Intn(len(delta.Changes)), r.Intn(len(delta.Changes))
			delta.Changes[i], delta.Changes[j] = delta.Changes[j], delta.Changes[i]
		}
	case 1: // 破坏某条实例变化声明的变化前状态
		for i := range delta.Changes {
			ch := &delta.Changes[i]
			if ch.ObjectBefore != nil && len(ch.ObjectBefore.Properties) > 0 {
				for p := range ch.ObjectBefore.Properties {
					ch.ObjectBefore.Properties[p] = "被篡改"
					break
				}
				return
			}
		}
	case 2: // 让更新引用快照中不存在的对象
		for i := range delta.Changes {
			ch := &delta.Changes[i]
			if ch.Kind == UpdateObject {
				ghost := obj("ghost", ch.ObjectBefore.Type, map[string]any{})
				ch.ObjectBefore = &ghost
				return
			}
		}
	case 3: // 篡改约束变化声明的变化前约束
		for i := range delta.Changes {
			ch := &delta.Changes[i]
			if ch.Kind == SetLinkConstraint {
				ch.BeforeConstraint = &Constraint{MaxOutgoingPerSource: 99}
				return
			}
		}
	}
}

// buildTarget 依据朴素重放的最终状态构造声明目标（仅覆盖被触及投影）。
func buildTarget(snap *Snapshot, delta *Delta) DeclaredTarget {
	work := CloneSnapshot(snap)
	for i := range delta.Changes {
		if f := naiveApply(work, i, &delta.Changes[i]); f != nil {
			break // 重放失败时目标内容不影响判定（更早阶段已命中）
		}
	}
	objTypes, linkTypes, objects, links := naiveTouched(delta)
	target := DeclaredTarget{
		ObjectTypes: map[string]*ObjectType{},
		LinkTypes:   map[string]*LinkType{},
		Objects:     map[string]*ObjectInstance{},
		Links:       map[string]*LinkInstance{},
	}
	for name := range objTypes {
		if t, ok := work.Schema.ObjectTypes[name]; ok {
			target.ObjectTypes[name] = cloneObjectType(&t)
		} else {
			target.ObjectTypes[name] = nil
		}
	}
	for name := range linkTypes {
		if t, ok := work.Schema.LinkTypes[name]; ok {
			target.LinkTypes[name] = cloneLinkType(&t)
		} else {
			target.LinkTypes[name] = nil
		}
	}
	for id := range objects {
		if o, ok := work.Objects[id]; ok {
			target.Objects[id] = cloneObject(&o)
		} else {
			target.Objects[id] = nil
		}
	}
	for id := range links {
		if l, ok := work.Links[id]; ok {
			target.Links[id] = cloneLink(&l)
		} else {
			target.Links[id] = nil
		}
	}
	return target
}

// injectTargetFault 以一定概率向声明目标注入一类故障。
func injectTargetFault(r *rand.Rand, delta *Delta) {
	if r.Intn(100) >= 30 {
		return
	}
	t := &delta.Target
	switch r.Intn(5) {
	case 0:
		for _, o := range t.Objects {
			if o != nil && len(o.Properties) > 0 {
				for p := range o.Properties {
					o.Properties[p] = "目标被篡改"
					break
				}
				return
			}
		}
	case 1:
		for _, ot := range t.ObjectTypes {
			if ot != nil {
				ot.Properties["ghost_prop"] = PropString
				return
			}
		}
	case 2:
		for _, l := range t.Links {
			if l != nil {
				l.Source = "ghost_endpoint"
				return
			}
		}
	case 3:
		for k := range t.Objects {
			delete(t.Objects, k)
			return
		}
		for k := range t.Links {
			delete(t.Links, k)
			return
		}
	case 4:
		extra := obj("extra", "TX", map[string]any{})
		t.Objects["extra"] = &extra
	}
}

// diffCaseLog 为一次对照校验的完整记录：输入、双方输出与判定依据。
type diffCaseLog struct {
	Case      int       `json:"case"`
	Seed      int64     `json:"seed"`
	Snapshot  *Snapshot `json:"snapshot"`
	Delta     *Delta    `json:"delta"`
	Optimized Verdict   `json:"optimized"`
	Naive     Verdict   `json:"naive"`
	Agree     bool      `json:"agree"`
}

// TestDifferentialAgainstNaive 在大量随机构造的快照与差异记录组合上，
// 对照优化实现与朴素参照模型的判定类别与不等价位置，并记录每次校验的
// 输入、输出与判定依据（JSONL 日志路径见测试输出）。
func TestDifferentialAgainstNaive(t *testing.T) {
	const cases = 3000
	logPath := filepath.Join(os.TempDir(), "replaycheck_differential.jsonl")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("无法创建对照日志：%v", err)
	}
	defer logFile.Close()
	t.Logf("差异对照日志（每次校验的输入、输出与判定依据）：%s", logPath)

	enc := json.NewEncoder(logFile)
	validator := NewValidator()
	categoryCount := map[Category]int{}

	for i := 0; i < cases; i++ {
		seed := int64(1749000 + i)
		r := rand.New(rand.NewSource(seed))
		snap := genSnapshot(r)
		delta := genDelta(r, snap)
		injectChangeFault(r, delta)
		delta.Target = buildTarget(snap, delta)
		injectTargetFault(r, delta)

		got := validator.Validate(snap, delta)
		want := naiveValidate(snap, delta)
		agree := got.Category == want.Category &&
			(got.Category != NotEquivalent || got.Location == want.Location)
		categoryCount[got.Category]++

		if err := enc.Encode(diffCaseLog{
			Case: i, Seed: seed, Snapshot: snap, Delta: delta,
			Optimized: got, Naive: want, Agree: agree,
		}); err != nil {
			t.Fatalf("写入对照日志失败：%v", err)
		}
		if !agree {
			t.Fatalf("用例 %d（种子 %d）判定不一致：优化实现 %v/%v（%s），朴素模型 %v/%v（%s）",
				i, seed, got.Category, got.Location, got.Reason, want.Category, want.Location, want.Reason)
		}
	}
	t.Logf("判定类别分布：%v", categoryCount)
}
