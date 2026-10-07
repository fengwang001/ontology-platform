package ontology

import "math"

// 本文件实现“受影响病例”的精确失效传播。
//
// 不变式：closeOf 提示索引对所有缓存有效的病例是精确的；
// 对缓存已失效的病例，其提示可能过时，但过时只会导致失效集合
// 适度放大（保守），绝不会遗漏——因为任何会改变病例推导的住宿
// 变更发生的那一刻，该病例就会被下面的规则标记失效。

// invalidateCases 将给定病例标记为失效（下次查询时重新推导）。
func (e *Engine) invalidateCases(ids map[string]bool) {
	for id := range ids {
		c := e.cases.get(id)
		if c == nil || c.revoked || !c.valid {
			continue
		}
		c.valid = false
		e.stats.CasesInvalidated++
		e.stats.InvalidatedIDs = append(e.stats.InvalidatedIDs, id)
	}
}

// invalidateStaySpan 患者 patient 在病房 ward 的住宿区间 [lo, hi) 发生变化
// （新增区间，或开区间被关闭）后，标记所有推导结果可能受影响的病例：
//
//  1. patient 自己的病例（其住宿集合变了）；
//  2. 在 ward 内与 [lo, hi) 有正时长重叠的其他患者的病例
//     （他们与 patient 之间的密接边可能变化）；
//  3. 上述患者当前作为密切接触者的病例（经 closeOf 提示，
//     他们与 patient 之间的次密接边可能变化）。
//
// 病例的推导只读取：病例患者及其同病房重叠者的住宿、其密接者及其
// 同病房重叠者的住宿，因此该集合是精确的（至多因保守提示略大）。
func (e *Engine) invalidateStaySpan(patient, ward string, lo, hi int64, now int64) {
	ids := make(map[string]bool)
	for _, c := range e.cases.ofPatient(patient) {
		ids[c.id] = true
	}
	for _, t := range e.stays.ofWard(ward) {
		e.stats.StaysScanned++
		if !(t.in < hi && lo < t.end(now)) {
			continue
		}
		for _, c := range e.cases.ofPatient(t.patient) {
			ids[c.id] = true
		}
		for id := range e.closeOf[t.patient] {
			ids[id] = true
		}
	}
	e.invalidateCases(ids)
}

// invalidateOpenEnded 处理新增开区间（入住）或关闭开区间（出住）：
// 变化区间视为 [lo, +∞)。
func (e *Engine) invalidateOpenEnded(patient, ward string, lo int64, now int64) {
	e.invalidateStaySpan(patient, ward, lo, math.MaxInt64, now)
}
