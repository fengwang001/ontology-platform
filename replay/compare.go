package replay

import (
	"fmt"
	"sort"
)

// 本文件实现第三阶段：重放结果与差异记录声明目标状态的等价性核对
// （NotEquivalent），不一致位置按结构/对象/链接三类报告。
//
// 核对方式：对目标状态中的每一项，逐一与重放结果比对；再用基数
// （计数）核对保证重放结果不存在目标之外的多余项。基数由覆盖层
// 在重放过程中增量维护，因此无需遍历快照中未被触及的对象与链接。

// compareToTarget 逐项核对重放结果（覆盖层当前有效状态）与差异记录
// 声明的目标状态。返回按确定顺序排序的全部不一致位置；空切片表示等价。
func compareToTarget(st *overlay, log *DeltaLog) []Mismatch {
	var out []Mismatch
	out = append(out, compareObjectTypes(st, log)...)
	out = append(out, compareLinkTypes(st, log)...)
	out = append(out, compareObjects(st, log)...)
	out = append(out, compareLinks(st, log)...)
	sort.Slice(out, func(a, b int) bool {
		if out[a].Category != out[b].Category {
			return out[a].Category < out[b].Category
		}
		if out[a].Key != out[b].Key {
			return out[a].Key < out[b].Key
		}
		return out[a].Detail < out[b].Detail
	})
	return out
}

func compareObjectTypes(st *overlay, log *DeltaLog) []Mismatch {
	var out []Mismatch
	target := log.TargetSchema.ObjectTypes
	if st.objectTypeCount != len(target) {
		out = append(out, Mismatch{
			Category: CategorySchema,
			Key:      "(objectTypeCount)",
			Detail: fmt.Sprintf("对象类型总数不一致: 重放结果 %d, 声明目标 %d",
				st.objectTypeCount, len(target)),
		})
	}
	for id, want := range target {
		got, ok := st.getObjectType(id)
		if !ok {
			out = append(out, Mismatch{Category: CategorySchema, Key: id,
				Detail: "重放结果缺少声明目标中的对象类型"})
			continue
		}
		if !objectTypeEqual(got, want) {
			out = append(out, Mismatch{Category: CategorySchema, Key: id,
				Detail: "对象类型定义与声明目标不一致"})
		}
	}
	return out
}

func compareLinkTypes(st *overlay, log *DeltaLog) []Mismatch {
	var out []Mismatch
	target := log.TargetSchema.LinkTypes
	if st.linkTypeCount != len(target) {
		out = append(out, Mismatch{
			Category: CategorySchema,
			Key:      "(linkTypeCount)",
			Detail: fmt.Sprintf("链接类型总数不一致: 重放结果 %d, 声明目标 %d",
				st.linkTypeCount, len(target)),
		})
	}
	for id, want := range target {
		got, ok := st.getLinkType(id)
		if !ok {
			out = append(out, Mismatch{Category: CategorySchema, Key: id,
				Detail: "重放结果缺少声明目标中的链接类型"})
			continue
		}
		if !linkTypeEqual(got, want) {
			out = append(out, Mismatch{Category: CategorySchema, Key: id,
				Detail: "链接类型定义与声明目标不一致"})
		}
	}
	return out
}

func compareObjects(st *overlay, log *DeltaLog) []Mismatch {
	var out []Mismatch
	if st.objectCount != len(log.TargetObjects) {
		out = append(out, Mismatch{
			Category: CategoryObject,
			Key:      "(count)",
			Detail: fmt.Sprintf("对象总数不一致: 重放结果 %d, 声明目标 %d",
				st.objectCount, len(log.TargetObjects)),
		})
	}
	for id, want := range log.TargetObjects {
		got, ok := st.getObject(id)
		if !ok {
			out = append(out, Mismatch{Category: CategoryObject, Key: id,
				Detail: "重放结果缺少声明目标中的对象"})
			continue
		}
		if !objectEqual(got, want) {
			out = append(out, Mismatch{Category: CategoryObject, Key: id,
				Detail: "对象取值与声明目标不一致"})
		}
	}
	return out
}

func compareLinks(st *overlay, log *DeltaLog) []Mismatch {
	var out []Mismatch
	if st.linkCount != len(log.TargetLinks) {
		out = append(out, Mismatch{
			Category: CategoryLink,
			Key:      "(count)",
			Detail: fmt.Sprintf("链接总数不一致: 重放结果 %d, 声明目标 %d",
				st.linkCount, len(log.TargetLinks)),
		})
	}
	for key, want := range log.TargetLinks {
		got, ok := st.getLink(key)
		if !ok {
			out = append(out, Mismatch{Category: CategoryLink, Key: fmt.Sprint(key),
				Detail: "重放结果缺少声明目标中的链接"})
			continue
		}
		if got != want {
			out = append(out, Mismatch{Category: CategoryLink, Key: fmt.Sprint(key),
				Detail: "链接与声明目标不一致"})
		}
	}
	return out
}
