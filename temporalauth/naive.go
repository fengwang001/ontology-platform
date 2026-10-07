package temporalauth

// NaiveDecider 是独立的“朴素逐条归一化”参考实现：版本链一律线性扫描，
// 时间一律按统一约定逐步换算。它刻意不调用生产路径的任何二分索引、缓存或
// 快捷路径，用于在随机操作序列下逐条对照生产判定结论。
type NaiveDecider struct{}

// NewNaiveDecider 构造朴素判定器。
func NewNaiveDecider() *NaiveDecider { return &NaiveDecider{} }

// naiveRegionIndex 线性扫描返回时刻 t 生效的地区版本下标。
func naiveRegionIndex(r *Region, t Instant) (int, bool) {
	idx := -1
	for i := 0; i < len(r.Versions); i++ {
		if r.Versions[i].ValidFrom <= t {
			idx = i
		}
	}
	if idx < 0 {
		return 0, false
	}
	return idx, true
}

// naiveObjectTypeIndex 线性扫描返回时刻 t 生效的对象类型版本下标。
func naiveObjectTypeIndex(ot *ObjectType, t Instant) (int, bool) {
	idx := -1
	for i := 0; i < len(ot.Versions); i++ {
		if ot.Versions[i].ValidFrom <= t {
			idx = i
		}
	}
	if idx < 0 {
		return 0, false
	}
	return idx, true
}

// naiveWindowIndex 线性扫描返回时刻 t 生效的窗口规则版本下标。
func naiveWindowIndex(wc *WindowChain, t Instant) (int, bool) {
	idx := -1
	for i := 0; i < len(wc.Versions); i++ {
		if wc.Versions[i].ValidFrom <= t {
			idx = i
		}
	}
	if idx < 0 {
		return 0, false
	}
	return idx, true
}

// NaiveOutcome 是朴素判定的结论。
type NaiveOutcome struct {
	Allowed      bool
	Code         ErrorCode
	BasisVersion int
	BasisZoneID  string
	WindowStart  Instant
	WindowEnd    Instant
}

// Decide 对一次查看请求执行朴素归一化与判定。它与生产 Service 遵循完全相同
// 的基准选取、错误优先级与半开窗口约定，但实现彼此独立。
func (d *NaiveDecider) Decide(snap Snapshot, req ViewRequest) NaiveOutcome {
	// 1. 对象归属。
	objectTypeID, ok := snap.objects[req.ObjectID]
	_ = objectTypeID
	if !ok {
		// 未知对象与“地区时区无法确定”归为同一对外错误，避免泄露存在性。
		return NaiveOutcome{Code: ErrRegionTimezoneUnresolved}
	}
	regionID := snap.objectRegion[req.ObjectID]
	region, ok := snap.regions[regionID]
	if !ok {
		return NaiveOutcome{Code: ErrRegionTimezoneUnresolved}
	}
	regIdx, ok := naiveRegionIndex(&region, req.Now)
	if !ok {
		return NaiveOutcome{Code: ErrRegionTimezoneUnresolved}
	}
	zone, ok := snap.catalog.Get(region.Versions[regIdx].ZoneID)
	if !ok {
		return NaiveOutcome{Code: ErrRegionTimezoneUnresolved}
	}

	// 2. 窗口规则。
	chain, hasChain := snap.windows[objectTypeID][req.Property]
	if !hasChain {
		return NaiveOutcome{Code: ErrWindowRuleInvalid}
	}
	wIdx, ok := naiveWindowIndex(&chain, req.Now)
	if !ok {
		return NaiveOutcome{Code: ErrWindowRuleInvalid}
	}
	rule := chain.Versions[wIdx].Rule
	if err := rule.Validate(); err != nil {
		return NaiveOutcome{Code: ErrWindowRuleInvalid}
	}
	start, end := ResolveBounds(rule, zone)

	// 3. 查询者身份。
	if req.Viewer == nil || req.Viewer.ID == "" {
		return NaiveOutcome{Code: ErrViewerMissing, BasisVersion: regIdx,
			BasisZoneID: zone.ID, WindowStart: start, WindowEnd: end}
	}

	// 4. 属性废弃。
	ot := snap.objectTypes[objectTypeID]
	otIdx, ok := naiveObjectTypeIndex(&ot, req.Now)
	if !ok {
		return NaiveOutcome{Code: ErrPropertyDeprecated, BasisVersion: regIdx,
			BasisZoneID: zone.ID, WindowStart: start, WindowEnd: end}
	}
	spec, found := ot.Versions[otIdx].Properties[req.Property]
	if !found || spec.Deprecated {
		return NaiveOutcome{Code: ErrPropertyDeprecated, BasisVersion: regIdx,
			BasisZoneID: zone.ID, WindowStart: start, WindowEnd: end}
	}

	// 5. 归一化记录存在性 + 窗口判定。
	rec, found := snap.records[req.ObjectID][req.Property]
	if !found {
		return NaiveOutcome{Code: ErrDenied, BasisVersion: regIdx,
			BasisZoneID: zone.ID, WindowStart: start, WindowEnd: end}
	}
	if !WithinWindow(req.Now, start, end) {
		return NaiveOutcome{Code: ErrDenied, BasisVersion: rec.basisVersion,
			BasisZoneID: zone.ID, WindowStart: start, WindowEnd: end}
	}
	return NaiveOutcome{Allowed: true, BasisVersion: rec.basisVersion,
		BasisZoneID: zone.ID, WindowStart: start, WindowEnd: end}
}
