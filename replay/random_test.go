package replay

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"strconv"
	"testing"
)

// 本文件实现随机对照测试：在大量随机构造的快照与差异记录组合上，
// 对照主实现（Verifier）与朴素参照模型（ReferenceVerify）的判定，
// 并记录每次校验的输入、输出与判定依据。

// --- 规范化文本表示（用于日志记录与失败复现） ---

func canonicalSchema(s Schema) string {
	var b []byte
	otypes := make([]string, 0, len(s.ObjectTypes))
	for id := range s.ObjectTypes {
		otypes = append(otypes, id)
	}
	sort.Strings(otypes)
	for _, id := range otypes {
		def := s.ObjectTypes[id]
		props := make([]string, 0, len(def.Properties))
		for p := range def.Properties {
			props = append(props, p)
		}
		sort.Strings(props)
		b = append(b, fmt.Sprintf("OT %s %v;", id, props)...)
	}
	ltypes := make([]string, 0, len(s.LinkTypes))
	for id := range s.LinkTypes {
		ltypes = append(ltypes, id)
	}
	sort.Strings(ltypes)
	for _, id := range ltypes {
		def := s.LinkTypes[id]
		b = append(b, fmt.Sprintf("LT %s %s->%s %v;", id, def.SourceType, def.TargetType, def.Constraints)...)
	}
	return string(b)
}

func canonicalObjects(objects map[string]ObjectInstance) string {
	ids := make([]string, 0, len(objects))
	for id := range objects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b []byte
	for _, id := range ids {
		obj := objects[id]
		props := make([]string, 0, len(obj.Properties))
		for p := range obj.Properties {
			props = append(props, p)
		}
		sort.Strings(props)
		b = append(b, fmt.Sprintf("O %s %s [", id, obj.ObjectType)...)
		for _, p := range props {
			b = append(b, fmt.Sprintf("%s=%v ", p, obj.Properties[p])...)
		}
		b = append(b, "];"...)
	}
	return string(b)
}

func canonicalLinks(links map[LinkKey]LinkInstance) string {
	keys := make([]LinkKey, 0, len(links))
	for k := range links {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		if keys[a].LinkTypeID != keys[b].LinkTypeID {
			return keys[a].LinkTypeID < keys[b].LinkTypeID
		}
		if keys[a].SourceID != keys[b].SourceID {
			return keys[a].SourceID < keys[b].SourceID
		}
		return keys[a].TargetID < keys[b].TargetID
	})
	var b []byte
	for _, k := range keys {
		b = append(b, fmt.Sprintf("L %v;", k)...)
	}
	return string(b)
}

func canonicalSnapshot(snap *Snapshot) string {
	return canonicalSchema(snap.Schema) + "|" + canonicalObjects(snap.Objects) + "|" + canonicalLinks(snap.Links)
}

func canonicalDelta(log *DeltaLog) string {
	var b []byte
	for i, c := range log.Changes {
		b = append(b, fmt.Sprintf("#%d %s %+v;", i, c.Kind, c)...)
	}
	b = append(b, "=> "...)
	b = append(b, canonicalSchema(log.TargetSchema)...)
	b = append(b, "|"...)
	b = append(b, canonicalObjects(log.TargetObjects)...)
	b = append(b, "|"...)
	b = append(b, canonicalLinks(log.TargetLinks)...)
	return string(b)
}

// --- 随机生成器 ---

type generator struct {
	rng *rand.Rand
}

func newGenerator(seed int64) *generator {
	return &generator{rng: rand.New(rand.NewSource(seed))}
}

func (g *generator) intn(n int) int { return g.rng.Intn(n) }

// genSnapshot 随机生成一份自身一致的快照。
func (g *generator) genSnapshot() *Snapshot {
	nTypes := 2 + g.intn(3)
	typeIDs := make([]string, nTypes)
	objectTypes := map[string]ObjectTypeDef{}
	for i := 0; i < nTypes; i++ {
		id := "T" + strconv.Itoa(i)
		typeIDs[i] = id
		nProps := 1 + g.intn(3)
		props := map[string]struct{}{}
		for j := 0; j < nProps; j++ {
			props["p"+strconv.Itoa(j)] = struct{}{}
		}
		objectTypes[id] = ObjectTypeDef{TypeID: id, Properties: props}
	}

	nLinkTypes := 1 + g.intn(3)
	linkTypes := map[string]LinkTypeDef{}
	for i := 0; i < nLinkTypes; i++ {
		id := "L" + strconv.Itoa(i)
		constraints := map[string]PropertyValue{}
		if g.intn(3) == 0 {
			constraints[ConstraintUniqueSource] = true
		}
		linkTypes[id] = LinkTypeDef{
			TypeID:      id,
			SourceType:  typeIDs[g.intn(nTypes)],
			TargetType:  typeIDs[g.intn(nTypes)],
			Constraints: constraints,
		}
	}

	nObjects := 3 + g.intn(8)
	objects := map[string]ObjectInstance{}
	for i := 0; i < nObjects; i++ {
		id := "o" + strconv.Itoa(i)
		typeID := typeIDs[g.intn(nTypes)]
		props := map[string]PropertyValue{}
		for p := range objectTypes[typeID].Properties {
			if g.intn(2) == 0 {
				props[p] = g.intn(100)
			}
		}
		objects[id] = ObjectInstance{ObjectID: id, ObjectType: typeID, Properties: props}
	}

	links := map[LinkKey]LinkInstance{}
	usedSource := map[LinkSourceKey]bool{}
	objIDs := make([]string, 0, len(objects))
	for id := range objects {
		objIDs = append(objIDs, id)
	}
	sort.Strings(objIDs)
	for _, lt := range linkTypes {
		var sources, targets []string
		for _, id := range objIDs {
			if objects[id].ObjectType == lt.SourceType {
				sources = append(sources, id)
			}
			if objects[id].ObjectType == lt.TargetType {
				targets = append(targets, id)
			}
		}
		nLinks := g.intn(4)
		for j := 0; j < nLinks && len(sources) > 0 && len(targets) > 0; j++ {
			s := sources[g.intn(len(sources))]
			t := targets[g.intn(len(targets))]
			key := LinkKey{LinkTypeID: lt.TypeID, SourceID: s, TargetID: t}
			if _, dup := links[key]; dup {
				continue
			}
			sk := LinkSourceKey{LinkTypeID: lt.TypeID, SourceID: s}
			if isUniqueSource(lt) && usedSource[sk] {
				continue
			}
			links[key] = LinkInstance{LinkTypeID: lt.TypeID, SourceID: s, TargetID: t}
			usedSource[sk] = true
		}
	}

	return NewSnapshot(
		Schema{ObjectTypes: objectTypes, LinkTypes: linkTypes},
		objects, links)
}

// objectsOfType 返回状态中某类型的全部对象 ID（排序保证确定性）。
func objectsOfType(st *State, typeID string) []string {
	var out []string
	for id, obj := range st.Objects {
		if obj.ObjectType == typeID {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// genValidDelta 在快照上随机应用若干合法变化，返回差异记录
// （目标状态为重放后的真实状态）。
func (g *generator) genValidDelta(snap *Snapshot) *DeltaLog {
	st := stateFromSnapshot(snap)
	var changes []Change
	n := 1 + g.intn(8)
	for i := 0; i < n; i++ {
		c, ok := g.genValidChange(st, i)
		if !ok {
			continue
		}
		if reason := refApply(st, &c); reason != "" {
			continue
		}
		changes = append(changes, c)
	}
	return &DeltaLog{
		Changes:       changes,
		TargetSchema:  st.Schema,
		TargetObjects: st.Objects,
		TargetLinks:   st.Links,
	}
}

// genValidChange 生成一条对当前状态合法的变化。
func (g *generator) genValidChange(st *State, seq int) (Change, bool) {
	typeIDs := make([]string, 0, len(st.Schema.ObjectTypes))
	for id := range st.Schema.ObjectTypes {
		typeIDs = append(typeIDs, id)
	}
	sort.Strings(typeIDs)
	linkTypeIDs := make([]string, 0, len(st.Schema.LinkTypes))
	for id := range st.Schema.LinkTypes {
		linkTypeIDs = append(linkTypeIDs, id)
	}
	sort.Strings(linkTypeIDs)

	for attempt := 0; attempt < 20; attempt++ {
		switch g.intn(13) {
		case 0: // AddObjectType
			id := fmt.Sprintf("NT%d-%d", seq, attempt)
			return Change{Kind: ChangeAddObjectType, TypeID: id,
				ObjectTypeAfter: ObjectTypeDef{TypeID: id,
					Properties: map[string]struct{}{"np": {}}}}, true
		case 1: // AddProperty
			if len(typeIDs) == 0 {
				break
			}
			tid := typeIDs[g.intn(len(typeIDs))]
			prop := fmt.Sprintf("np%d-%d", seq, attempt)
			return Change{Kind: ChangeAddProperty, TypeID: tid, Property: prop}, true
		case 2: // RemoveProperty（无人使用时）
			if len(typeIDs) == 0 {
				break
			}
			tid := typeIDs[g.intn(len(typeIDs))]
			for p := range st.Schema.ObjectTypes[tid].Properties {
				used := false
				for _, obj := range st.Objects {
					if obj.ObjectType != tid {
						continue
					}
					if _, has := obj.Properties[p]; has {
						used = true
						break
					}
				}
				if !used {
					return Change{Kind: ChangeRemoveProperty, TypeID: tid, Property: p}, true
				}
			}
		case 3: // AddLinkType
			if len(typeIDs) == 0 {
				break
			}
			id := fmt.Sprintf("NL%d-%d", seq, attempt)
			return Change{Kind: ChangeAddLinkType, LinkTypeID: id,
				LinkTypeAfter: LinkTypeDef{TypeID: id,
					SourceType:  typeIDs[g.intn(len(typeIDs))],
					TargetType:  typeIDs[g.intn(len(typeIDs))],
					Constraints: map[string]PropertyValue{}}}, true
		case 4: // AddLinkTypeConstraint
			for _, ltid := range linkTypeIDs {
				lt := st.Schema.LinkTypes[ltid]
				if _, has := lt.Constraints[ConstraintUniqueSource]; !has {
					return Change{Kind: ChangeAddLinkTypeConstraint, LinkTypeID: ltid,
						Constraint: ConstraintUniqueSource, ConstraintValue: true}, true
				}
			}
		case 5: // RemoveLinkTypeConstraint
			for _, ltid := range linkTypeIDs {
				lt := st.Schema.LinkTypes[ltid]
				if _, has := lt.Constraints[ConstraintUniqueSource]; has {
					return Change{Kind: ChangeRemoveLinkTypeConstraint, LinkTypeID: ltid,
						Constraint: ConstraintUniqueSource}, true
				}
			}
		case 6: // AddObject
			if len(typeIDs) == 0 {
				break
			}
			id := fmt.Sprintf("no%d-%d", seq, attempt)
			tid := typeIDs[g.intn(len(typeIDs))]
			props := map[string]PropertyValue{}
			for p := range st.Schema.ObjectTypes[tid].Properties {
				if g.intn(2) == 0 {
					props[p] = g.intn(100)
				}
			}
			return Change{Kind: ChangeAddObject,
				ObjectAfter: ObjectInstance{ObjectID: id, ObjectType: tid, Properties: props}}, true
		case 7: // UpdateObject
			if len(st.Objects) == 0 {
				break
			}
			ids := make([]string, 0, len(st.Objects))
			for id := range st.Objects {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			id := ids[g.intn(len(ids))]
			cur := st.Objects[id]
			after := cloneObject(cur)
			for p := range st.Schema.ObjectTypes[cur.ObjectType].Properties {
				if g.intn(2) == 0 {
					after.Properties[p] = g.intn(100)
				}
			}
			return Change{Kind: ChangeUpdateObject, ObjectBefore: cur, ObjectAfter: after}, true
		case 8: // RemoveObject（无链接引用时）
			candidates := []string{}
			for id := range st.Objects {
				referenced := false
				for _, l := range st.Links {
					if l.SourceID == id || l.TargetID == id {
						referenced = true
						break
					}
				}
				if !referenced {
					candidates = append(candidates, id)
				}
			}
			if len(candidates) == 0 {
				break
			}
			sort.Strings(candidates)
			id := candidates[g.intn(len(candidates))]
			return Change{Kind: ChangeRemoveObject, ObjectBefore: st.Objects[id]}, true
		case 9: // AddLink
			if len(linkTypeIDs) == 0 {
				break
			}
			ltid := linkTypeIDs[g.intn(len(linkTypeIDs))]
			lt := st.Schema.LinkTypes[ltid]
			sources := objectsOfType(st, lt.SourceType)
			targets := objectsOfType(st, lt.TargetType)
			if len(sources) == 0 || len(targets) == 0 {
				break
			}
			s := sources[g.intn(len(sources))]
			t := targets[g.intn(len(targets))]
			key := LinkKey{LinkTypeID: ltid, SourceID: s, TargetID: t}
			if _, dup := st.Links[key]; dup {
				break
			}
			if isUniqueSource(lt) {
				conflict := false
				for _, l := range st.Links {
					if l.LinkTypeID == ltid && l.SourceID == s {
						conflict = true
						break
					}
				}
				if conflict {
					break
				}
			}
			return Change{Kind: ChangeAddLink,
				LinkAfter: LinkInstance{LinkTypeID: ltid, SourceID: s, TargetID: t}}, true
		case 10: // RemoveLink
			if len(st.Links) == 0 {
				break
			}
			keys := make([]LinkKey, 0, len(st.Links))
			for k := range st.Links {
				keys = append(keys, k)
			}
			sort.Slice(keys, func(a, b int) bool {
				if keys[a].LinkTypeID != keys[b].LinkTypeID {
					return keys[a].LinkTypeID < keys[b].LinkTypeID
				}
				if keys[a].SourceID != keys[b].SourceID {
					return keys[a].SourceID < keys[b].SourceID
				}
				return keys[a].TargetID < keys[b].TargetID
			})
			key := keys[g.intn(len(keys))]
			return Change{Kind: ChangeRemoveLink, LinkBefore: st.Links[key]}, true
		case 11: // RemoveLinkType（无链接时）
			for _, ltid := range linkTypeIDs {
				used := false
				for _, l := range st.Links {
					if l.LinkTypeID == ltid {
						used = true
						break
					}
				}
				if !used {
					return Change{Kind: ChangeRemoveLinkType, LinkTypeID: ltid}, true
				}
			}
		case 12: // RemoveObjectType（无对象且未被链接类型引用时）
			for _, tid := range typeIDs {
				if len(objectsOfType(st, tid)) > 0 {
					continue
				}
				referenced := false
				for _, lt := range st.Schema.LinkTypes {
					if lt.SourceType == tid || lt.TargetType == tid {
						referenced = true
						break
					}
				}
				if !referenced {
					return Change{Kind: ChangeRemoveObjectType, TypeID: tid}, true
				}
			}
		}
	}
	return Change{}, false
}

// --- 破坏器：从有效输入构造各类失败场景 ---

// corruptDelta 以随机方式破坏差异记录（可能就绪保持不变）。
func (g *generator) corruptDelta(log *DeltaLog) {
	n := g.intn(10)
	switch n {
	case 0, 1: // 交换两条变化（可能制造结构/实例顺序错位）
		if len(log.Changes) >= 2 {
			a, b := g.intn(len(log.Changes)), g.intn(len(log.Changes))
			log.Changes[a], log.Changes[b] = log.Changes[b], log.Changes[a]
		}
	case 2: // 删除一条变化
		if len(log.Changes) > 0 {
			i := g.intn(len(log.Changes))
			log.Changes = append(log.Changes[:i], log.Changes[i+1:]...)
		}
	case 3: // 复制一条变化插入随机位置（可能制造重复添加等）
		if len(log.Changes) > 0 {
			c := log.Changes[g.intn(len(log.Changes))]
			i := g.intn(len(log.Changes) + 1)
			log.Changes = append(log.Changes[:i], append([]Change{c}, log.Changes[i:]...)...)
		}
	case 4: // 破坏某条变化的变化前状态
		for i := range log.Changes {
			c := &log.Changes[i]
			switch c.Kind {
			case ChangeUpdateObject, ChangeRemoveObject:
				if len(c.ObjectBefore.Properties) > 0 {
					for p := range c.ObjectBefore.Properties {
						c.ObjectBefore.Properties[p] = "corrupted"
						break
					}
					return
				}
			case ChangeRemoveLink:
				c.LinkBefore.TargetID = "corrupted-target"
				return
			}
		}
	case 5: // 破坏某条变化的变化后状态或结构引用
		for i := range log.Changes {
			c := &log.Changes[i]
			switch c.Kind {
			case ChangeAddObject, ChangeUpdateObject:
				c.ObjectAfter.Properties["undeclared-prop"] = 1
				return
			case ChangeAddLink:
				c.LinkAfter.LinkTypeID = "no-such-link-type"
				return
			case ChangeAddObjectType:
				c.ObjectTypeAfter.TypeID = "mismatched-id"
				return
			}
		}
	case 6: // 破坏声明目标：结构层
		for id := range log.TargetSchema.ObjectTypes {
			def := log.TargetSchema.ObjectTypes[id]
			def.Properties["ghost-prop"] = struct{}{}
			log.TargetSchema.ObjectTypes[id] = def
			return
		}
	case 7: // 破坏声明目标：对象或链接
		for id, obj := range log.TargetObjects {
			obj.Properties["ghost"] = true
			log.TargetObjects[id] = obj
			return
		}
		for k := range log.TargetLinks {
			delete(log.TargetLinks, k)
			return
		}
	case 8: // 破坏变化种类
		if len(log.Changes) > 0 {
			log.Changes[g.intn(len(log.Changes))].Kind = ChangeKind(99)
		}
	case 9: // 破坏结构变化引用的类型 ID
		for i := range log.Changes {
			c := &log.Changes[i]
			switch c.Kind {
			case ChangeAddProperty, ChangeRemoveProperty, ChangeRemoveObjectType:
				c.TypeID = "no-such-type"
				return
			case ChangeAddLinkTypeConstraint, ChangeRemoveLinkTypeConstraint, ChangeRemoveLinkType:
				c.LinkTypeID = "no-such-link-type"
				return
			}
		}
	}
}

// corruptSnapshot 以随机方式破坏快照（并重派生索引），
// 模拟重放环境与差异记录产生环境不一致。
func (g *generator) corruptSnapshot(snap *Snapshot) {
	switch g.intn(4) {
	case 0: // 删除一个对象（差异记录可能依赖它）
		for id := range snap.Objects {
			delete(snap.Objects, id)
			// 同时删除挂在它上面的链接，保持快照自身一致。
			for k, l := range snap.Links {
				if l.SourceID == id || l.TargetID == id {
					delete(snap.Links, k)
				}
			}
			return
		}
	case 1: // 修改一个对象的取值
		for id, obj := range snap.Objects {
			obj.Properties["tampered"] = true
			snap.Objects[id] = obj
			return
		}
	case 2: // 删除一条链接
		for k := range snap.Links {
			delete(snap.Links, k)
			return
		}
	case 3: // 删除一个未被引用的对象类型
		for id := range snap.Schema.ObjectTypes {
			used := false
			for _, obj := range snap.Objects {
				if obj.ObjectType == id {
					used = true
					break
				}
			}
			for _, lt := range snap.Schema.LinkTypes {
				if lt.SourceType == id || lt.TargetType == id {
					used = true
					break
				}
			}
			if !used {
				delete(snap.Schema.ObjectTypes, id)
				return
			}
		}
	}
}

// --- 随机对照测试 ---

// caseLog 是单次校验的结构化日志记录。
type caseLog struct {
	Case         int      `json:"case"`
	Seed         int64    `json:"seed"`
	CorruptDelta bool     `json:"corruptDelta"`
	CorruptSnap  bool     `json:"corruptSnapshot"`
	Snapshot     string   `json:"snapshot"`
	Delta        string   `json:"delta"`
	Verdict      string   `json:"verdict"`
	ChangeIndex  int      `json:"changeIndex"`
	Reason       string   `json:"reason"`
	Mismatches   []string `json:"mismatches,omitempty"`
}

// TestRandomDifferential 在大量随机构造的快照与差异记录组合上，
// 对照主实现与朴素参照模型的判定结果，并记录每次校验的
// 输入、输出与判定依据（-v 可见；设置 REPLAY_FUZZ_LOG 环境变量
// 可将 JSONL 日志写入指定文件）。
func TestRandomDifferential(t *testing.T) {
	const cases = 3000

	var logFile *os.File
	if path := os.Getenv("REPLAY_FUZZ_LOG"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatalf("无法创建日志文件: %v", err)
		}
		defer f.Close()
		logFile = f
	}

	verdicts := map[Verdict]int{}
	v := NewVerifier()

	for i := 0; i < cases; i++ {
		seed := int64(i)*2654435761 + 1
		g := newGenerator(seed)

		snap := g.genSnapshot()
		log := g.genValidDelta(snap)

		corruptD, corruptS := false, false
		if g.intn(10) < 7 { // 70% 破坏差异记录
			g.corruptDelta(log)
			corruptD = true
		}
		if g.intn(10) < 3 { // 30% 破坏快照（重派生索引）
			g.corruptSnapshot(snap)
			snap.Indexes = BuildIndexes(snap.Objects, snap.Links)
			corruptS = true
		}

		snapBefore := cloneSnapshot(snap)

		got := v.Verify(snap, log)
		want := ReferenceVerify(snap, log)

		entry := caseLog{
			Case:         i,
			Seed:         seed,
			CorruptDelta: corruptD,
			CorruptSnap:  corruptS,
			Snapshot:     canonicalSnapshot(snap),
			Delta:        canonicalDelta(log),
			Verdict:      got.Verdict.String(),
			ChangeIndex:  got.ChangeIndex,
			Reason:       got.Reason,
		}
		for _, m := range got.Mismatches {
			entry.Mismatches = append(entry.Mismatches,
				fmt.Sprintf("%s:%s:%s", m.Category, m.Key, m.Detail))
		}
		if logFile != nil {
			data, _ := json.Marshal(entry)
			logFile.Write(append(data, '\n'))
		}
		t.Logf("case=%d seed=%d corruptD=%v corruptS=%v verdict=%v changeIndex=%d reason=%q",
			i, seed, corruptD, corruptS, got.Verdict, got.ChangeIndex, got.Reason)

		if got.Verdict != want.Verdict || got.ChangeIndex != want.ChangeIndex ||
			!reflect.DeepEqual(got.Mismatches, want.Mismatches) {
			t.Fatalf("case %d (seed %d) 主实现与参照模型判定不一致:\n主实现: %+v\n参照:   %+v\n快照: %s\n差异记录: %s",
				i, seed, got, want, canonicalSnapshot(snap), canonicalDelta(log))
		}
		if !reflect.DeepEqual(snapBefore, snap) {
			t.Fatalf("case %d (seed %d) 校验修改了原始快照", i, seed)
		}
		verdicts[got.Verdict]++
	}

	t.Logf("判定分布: %v", verdicts)
	for _, want := range []Verdict{
		VerdictEquivalent, VerdictInconsistentDelta,
		VerdictPreconditionFailed, VerdictNotEquivalent,
	} {
		if verdicts[want] == 0 {
			t.Fatalf("随机用例未覆盖判定类别 %v", want)
		}
	}
}
