package ontology

import "sort"

// StatusCode 是展开结果中单个属性的状态标记。
type StatusCode int

const (
	// StatusValue 属性在当时版本中存在且事实带有取值。
	StatusValue StatusCode = iota
	// StatusMissingRequired 属性在当时版本中已存在且为必填，
	// 但历史事实本身遗漏了取值。
	StatusMissingRequired
	// StatusUnsetOptional 属性在当时版本中已存在但为可选，
	// 事实未携带取值。
	StatusUnsetOptional
	// StatusNotApplicable 属性在事实写入时刻生效的版本中尚不存在，
	// 因而无取值（与遗漏取值严格区分）。
	StatusNotApplicable
)

func (c StatusCode) String() string {
	switch c {
	case StatusValue:
		return "value"
	case StatusMissingRequired:
		return "missing-required"
	case StatusUnsetOptional:
		return "unset-optional"
	case StatusNotApplicable:
		return "not-applicable"
	}
	return "unknown"
}

// PropStatus 是展开结果中单个属性的状态与（可选）取值。
type PropStatus struct {
	Status StatusCode
	Value  Value
}

// FactView 是一条历史事实按其写入时刻生效的属性定义版本
// 重新解释后的展开视图。
type FactView struct {
	ValidTime       ValidTime
	RecordTime      RecordTime
	SchemaVersionID int64
	Props           map[string]PropStatus
}

// ExpandResult 是一次历史展开的完整结果。
type ExpandResult struct {
	ObjectID string
	Facts    []FactView // 按有效时间升序
}

// Expand 在记录时刻 req.AsOfRecord 可见的范围内，展开对象在有效时间
// 区间 [ValidFrom, ValidTo] 内的全部历史事实。每条事实严格按其写入
// 记录时刻生效的属性定义版本解释取值含义。
//
// 错误检查按 ErrorCode 声明顺序（即优先级）进行，只报告一类；
// 任何错误都不会对历史轨迹或属性定义版本产生可观察改动。
func (s *Store) Expand(req ExpandRequest) (ExpandResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	h, ok := s.objects[req.ObjectID]
	if !ok || !h.hasFact {
		return ExpandResult{}, newError(ErrCodeRecordBeforeFirstFact,
			"object %q has no facts", req.ObjectID)
	}

	// 优先级 1：记录时刻早于首条事实。
	if req.AsOfRecord < h.firstRecordTime() {
		return ExpandResult{}, newError(ErrCodeRecordBeforeFirstFact,
			"as-of record time %d is before first fact of object %q",
			req.AsOfRecord, req.ObjectID)
	}

	ts := s.types[h.typeID]

	// 优先级 2：指定的属性定义版本已被作废。
	if req.PinSchemaVersion != nil {
		if _, ok := ts.findVersion(*req.PinSchemaVersion); !ok {
			return ExpandResult{}, newError(ErrCodeSchemaInvalidated,
				"schema version %d has been invalidated by migration",
				*req.PinSchemaVersion)
		}
	}

	// 优先级 3：区间自相矛盾。
	if req.ValidFrom > req.ValidTo {
		return ExpandResult{}, newError(ErrCodeContradictoryRange,
			"valid range [%d, %d] is contradictory", req.ValidFrom, req.ValidTo)
	}

	// 二分定位有效时间区间，展开开销与历史总量无关。
	lo := sort.Search(len(h.validTimes), func(i int) bool {
		return h.validTimes[i] >= req.ValidFrom
	})
	hi := sort.Search(len(h.validTimes), func(i int) bool {
		return h.validTimes[i] > req.ValidTo
	})

	// 属性全集：跨所有版本出现过的属性名，用于给出 NotApplicable 标记。
	// 在创建类型与迁移提交时预先计算（写锁内），展开只读缓存，
	// 避免开销随迁移次数线性增长。
	allProps := ts.allPropsCache

	res := ExpandResult{ObjectID: req.ObjectID}
	var factProbes, versionProbes int64
	for _, vt := range h.validTimes[lo:hi] {
		f, ok := h.byValid[vt].latestAtOrBefore(req.AsOfRecord, &factProbes)
		if !ok {
			continue
		}
		sv, ok := ts.versionAt(f.RecordTime, &versionProbes)
		if !ok {
			continue
		}
		res.Facts = append(res.Facts, interpretFact(f, sv, allProps))
	}
	s.factProbes.Add(factProbes)
	s.versionProbes.Add(versionProbes)
	s.recordAudit(DecisionRecord{
		Op:       OpExpand,
		Input:    expandInputSummary(req),
		Verdict:  VerdictOK,
		Versions: versionIDsOf(res.Facts),
	})
	return res, nil
}

// interpretFact 按事实写入时刻生效的版本 sv 解释取值。
func interpretFact(f Fact, sv SchemaVersion, allProps []string) FactView {
	view := FactView{
		ValidTime:       f.ValidTime,
		RecordTime:      f.RecordTime,
		SchemaVersionID: sv.ID,
		Props:           make(map[string]PropStatus, len(allProps)),
	}
	for _, name := range allProps {
		pd, existed := sv.Props[name]
		if !existed {
			view.Props[name] = PropStatus{Status: StatusNotApplicable}
			continue
		}
		if v, present := f.Values[name]; present {
			if cv, ok := v.CoerceTo(pd.Type); ok {
				view.Props[name] = PropStatus{Status: StatusValue, Value: cv}
			} else {
				view.Props[name] = PropStatus{Status: StatusValue, Value: v}
			}
			continue
		}
		if pd.Required {
			view.Props[name] = PropStatus{Status: StatusMissingRequired}
		} else {
			view.Props[name] = PropStatus{Status: StatusUnsetOptional}
		}
	}
	return view
}

func unionPropNames(versions []SchemaVersion) []string {
	seen := make(map[string]struct{})
	var names []string
	for _, v := range versions {
		for name := range v.Props {
			if _, ok := seen[name]; !ok {
				seen[name] = struct{}{}
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	return names
}

func versionIDsOf(facts []FactView) []int64 {
	seen := make(map[int64]struct{})
	var ids []int64
	for _, f := range facts {
		if _, ok := seen[f.SchemaVersionID]; !ok {
			seen[f.SchemaVersionID] = struct{}{}
			ids = append(ids, f.SchemaVersionID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
