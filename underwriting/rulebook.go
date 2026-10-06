package underwriting

import "sort"

// rulebook 规则库：按职业类别分桶索引。
// 裁定查询只访问投保单职业类别对应的桶，因此开销不随
// 与该投保单年龄、职业都不相关的规则数量增长。
type rulebook struct {
	all     map[string]Rule // 编号 -> 规则
	buckets [7][]string     // 职业类别 -> 桶内规则编号（含不限职业的规则）
	dirty   bool            // 增删改后置位，查询前惰性重建索引
}

func newRulebook() *rulebook {
	return &rulebook{all: map[string]Rule{}}
}

// validateRule 参数校验，任一不满足报参数非法。
func validateRule(r Rule) error {
	if r.ID == "" {
		return errf(ErrInvalidParam, "规则编号为空")
	}
	if r.Start < 0 || r.Start >= r.End {
		return errf(ErrInvalidParam, "规则 %s 生效区间非法: [%d, %d)", r.ID, r.Start, r.End)
	}
	if r.Cond.AgeLo != nil && *r.Cond.AgeLo < 0 {
		return errf(ErrInvalidParam, "规则 %s 年龄下限为负", r.ID)
	}
	if r.Cond.AgeHi != nil && *r.Cond.AgeHi < 0 {
		return errf(ErrInvalidParam, "规则 %s 年龄上限为负", r.ID)
	}
	if r.Cond.AgeLo != nil && r.Cond.AgeHi != nil && *r.Cond.AgeLo > *r.Cond.AgeHi {
		return errf(ErrInvalidParam, "规则 %s 年龄区间非法", r.ID)
	}
	for occ := range r.Cond.Occupations {
		if occ < 1 || occ > 6 {
			return errf(ErrInvalidParam, "规则 %s 职业类别 %d 不在 1 到 6", r.ID, occ)
		}
	}
	switch r.Act.Kind {
	case ActionStandard, ActionReject:
	case ActionLoading:
		if r.Act.Percent <= 0 {
			return errf(ErrInvalidParam, "规则 %s 加费百分比非正: %d", r.ID, r.Act.Percent)
		}
	case ActionExclusion:
		if r.Act.ExclusionCode == "" {
			return errf(ErrInvalidParam, "规则 %s 除外编码为空", r.ID)
		}
	case ActionPostpone:
		if r.Act.PostponeUntil < 0 {
			return errf(ErrInvalidParam, "规则 %s 延期时刻为负", r.ID)
		}
	default:
		return errf(ErrInvalidParam, "规则 %s 动作类型未知: %d", r.ID, r.Act.Kind)
	}
	return nil
}

func (rb *rulebook) add(r Rule) error {
	if err := validateRule(r); err != nil {
		return err
	}
	if _, ok := rb.all[r.ID]; ok {
		return errf(ErrRuleDuplicate, "规则 %s 已存在", r.ID)
	}
	rb.all[r.ID] = r.clone()
	rb.dirty = true
	return nil
}

func (rb *rulebook) update(r Rule) error {
	if err := validateRule(r); err != nil {
		return err
	}
	if _, ok := rb.all[r.ID]; !ok {
		return errf(ErrRuleNotFound, "规则 %s 不存在", r.ID)
	}
	rb.all[r.ID] = r.clone()
	rb.dirty = true
	return nil
}

func (rb *rulebook) remove(id string) error {
	if id == "" {
		return errf(ErrInvalidParam, "规则编号为空")
	}
	if _, ok := rb.all[id]; !ok {
		return errf(ErrRuleNotFound, "规则 %s 不存在", id)
	}
	delete(rb.all, id)
	rb.dirty = true
	return nil
}

// rebuild 按职业类别重建分桶索引：规则不限职业时进入全部 6 个桶，
// 否则只进入其职业集合包含的类别桶。
func (rb *rulebook) rebuild() {
	for i := range rb.buckets {
		rb.buckets[i] = rb.buckets[i][:0]
	}
	for id, r := range rb.all {
		if r.Cond.Occupations == nil {
			for occ := 1; occ <= 6; occ++ {
				rb.buckets[occ] = append(rb.buckets[occ], id)
			}
		} else {
			for occ := range r.Cond.Occupations {
				rb.buckets[occ] = append(rb.buckets[occ], id)
			}
		}
	}
	rb.dirty = false
}

// query 返回在 t 时刻生效、且年龄与职业均命中的规则，按编号排序保证可复现。
// 只扫描投保单职业类别对应的桶，与年龄、职业都不相关的规则不参与开销。
func (rb *rulebook) query(age, occupation int, t int64) []Rule {
	if rb.dirty {
		rb.rebuild()
	}
	var out []Rule
	for _, id := range rb.buckets[occupation] {
		r := rb.all[id]
		if r.effectiveAt(t) && r.Cond.matchesAgeOcc(age, occupation) {
			out = append(out, r.clone())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
