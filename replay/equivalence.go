package replay

import (
	"fmt"
	"sort"
)

func notEquivf(loc Location, format string, args ...any) *failure {
	return &failure{
		location:    loc,
		changeIndex: -1,
		reason:      "重放结果与差异记录声明的目标状态不等价：" + fmt.Sprintf(format, args...),
	}
}

// checkEquivalence 阶段四：将重放结果与差异记录声明的目标状态
// 在结构、对象、链接三个层面逐项核对，仅覆盖被差异记录触及的投影
// （未被触及的部分由重放语义保证与快照一致，无需核对）。
// 按结构、对象、链接的固定顺序检查，键排序后逐个比较以保证确定性。
func checkEquivalence(final *replayState, self *selfModel) *failure {
	t := self.target
	for _, name := range sortedKeys(self.touchedObjectTypes) {
		replayed, ok := final.getObjectType(name)
		if !equalObjectTypeOpt(replayed, ok, t.ObjectTypes[name]) {
			return notEquivf(SchemaLocation, "对象类型 %q 的重放结果与声明目标不一致", name)
		}
	}
	for _, name := range sortedKeys(self.touchedLinkTypes) {
		replayed, ok := final.getLinkType(name)
		if !equalLinkTypeOpt(replayed, ok, t.LinkTypes[name]) {
			return notEquivf(SchemaLocation, "链接类型 %q 的重放结果与声明目标不一致", name)
		}
	}
	for _, id := range sortedKeys(self.touchedObjects) {
		replayed, ok := final.getObject(id)
		if !equalObjectOpt(replayed, ok, t.Objects[id]) {
			return notEquivf(ObjectLocation, "对象 %q 的重放结果与声明目标不一致", id)
		}
	}
	for _, id := range sortedKeys(self.touchedLinks) {
		replayed, ok := final.getLink(id)
		if !equalLinkOpt(replayed, ok, t.Links[id]) {
			return notEquivf(LinkLocation, "链接 %q 的重放结果与声明目标不一致", id)
		}
	}
	return nil
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// equalXxxOpt 比较“重放结果（可能不存在）”与“声明目标（nil 表示应不存在）”。
func equalObjectTypeOpt(replayed *ObjectType, ok bool, expected *ObjectType) bool {
	if expected == nil {
		return !ok
	}
	return ok && equalObjectType(replayed, expected)
}

func equalLinkTypeOpt(replayed *LinkType, ok bool, expected *LinkType) bool {
	if expected == nil {
		return !ok
	}
	return ok && equalLinkType(replayed, expected)
}

func equalObjectOpt(replayed *ObjectInstance, ok bool, expected *ObjectInstance) bool {
	if expected == nil {
		return !ok
	}
	return ok && equalObject(replayed, expected)
}

func equalLinkOpt(replayed *LinkInstance, ok bool, expected *LinkInstance) bool {
	if expected == nil {
		return !ok
	}
	return ok && equalLink(replayed, expected)
}
