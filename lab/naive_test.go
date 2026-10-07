package lab

// 本文件是独立朴素模型：直接按需求条文用全量历史线性扫描实现，
// 不复用 System 的任何内部结构，用于随机操作序列的对照验证。
// 朴素模型的查询与签收都扫描全部历史，复杂度随历史总量增长，
// 与 System 的实现形成对照。

import "sort"

type naiveItem struct {
	itemID, appID, patientID string
	priority                 int
	spec                     ItemSpec
	status                   ItemStatus
	rejections               int
	lastReject               RejectReason
	pendingSince             int64
	collectTime              int64
	tubeID                   string
}

type naiveTube struct {
	id, tubeType, patientID string
	itemIDs                 []string
	cold                    bool
	state                   TubeState
}

type naiveSystem struct {
	last    int64
	started bool
	catalog map[string]ItemSpec
	history []*naiveItem // 全量历史，从不删除
	tubes   []*naiveTube
	appIDs  map[string]bool
}

func newNaiveSystem() *naiveSystem {
	return &naiveSystem{
		catalog: make(map[string]ItemSpec),
		appIDs:  make(map[string]bool),
	}
}

func (n *naiveSystem) checkClock(now int64) *Error {
	if now < 0 || now > MaxNow {
		return newError(ErrInvalidParam, "now=%d 超出 [0,%d]", now, MaxNow)
	}
	if n.started && now < n.last {
		return newError(ErrClockRollback, "now=%d 小于上次被接受操作的 now=%d", now, n.last)
	}
	return nil
}

// findOpen 线性扫描全量历史，找患者某项目当前未终结的申请项。
func (n *naiveSystem) findOpen(patientID, itemID string) *naiveItem {
	for _, it := range n.history {
		if it.patientID == patientID && it.itemID == itemID && !it.status.Terminal() {
			return it
		}
	}
	return nil
}

// hasFinished 线性扫描全量历史，判断患者某项目是否存在已终结的申请项。
func (n *naiveSystem) hasFinished(patientID, itemID string) (ItemStatus, bool) {
	for _, it := range n.history {
		if it.patientID == patientID && it.itemID == itemID && it.status.Terminal() {
			return it.status, true
		}
	}
	return 0, false
}

func (n *naiveSystem) findTube(tubeID string) *naiveTube {
	for _, tb := range n.tubes {
		if tb.id == tubeID {
			return tb
		}
	}
	return nil
}

func (n *naiveSystem) registerItem(now int64, itemID, tubeType string, maxDeliverySec int64, requireCold bool, maxHemolysis int) error {
	if itemID == "" || tubeType == "" || maxDeliverySec <= 0 ||
		maxHemolysis < 0 || maxHemolysis > MaxHemolysisLevel {
		return newError(ErrInvalidParam, "登记项目参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	n.catalog[itemID] = ItemSpec{TubeType: tubeType, MaxDelivery: maxDeliverySec, RequireCold: requireCold, MaxHemolysis: maxHemolysis}
	n.last, n.started = now, true
	return nil
}

func (n *naiveSystem) submitApplication(now int64, appID, patientID string, itemIDs []string, priority int) error {
	if appID == "" || patientID == "" || !distinctIDs(itemIDs, 1, MaxItemsPerApplication) {
		return newError(ErrInvalidParam, "申请参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	for _, itemID := range itemIDs {
		if _, ok := n.catalog[itemID]; !ok {
			return newError(ErrNotFound, "检验项目 %q 未登记", itemID)
		}
	}
	if n.appIDs[appID] {
		return newError(ErrDuplicate, "申请标识 %q 已存在", appID)
	}
	for _, itemID := range itemIDs {
		if n.findOpen(patientID, itemID) != nil {
			return newError(ErrDuplicate, "患者 %q 的项目 %q 已有未终结申请项", patientID, itemID)
		}
	}
	for _, itemID := range itemIDs {
		n.history = append(n.history, &naiveItem{
			itemID:       itemID,
			appID:        appID,
			patientID:    patientID,
			priority:     priority,
			spec:         n.catalog[itemID],
			status:       StatusPending,
			lastReject:   RejectNone,
			pendingSince: now,
		})
	}
	n.appIDs[appID] = true
	n.last, n.started = now, true
	return nil
}

func (n *naiveSystem) collect(now int64, tubeID, tubeType, patientID string, itemIDs []string, collectTime int64) error {
	if tubeID == "" || tubeType == "" || patientID == "" || !distinctIDs(itemIDs, 1, 0) {
		return newError(ErrInvalidParam, "采集参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	patientKnown := false
	for _, it := range n.history {
		if it.patientID == patientID {
			patientKnown = true
			break
		}
	}
	if !patientKnown {
		return newError(ErrNotFound, "患者 %q 不存在", patientID)
	}
	for _, itemID := range itemIDs {
		if n.findOpen(patientID, itemID) == nil {
			if _, finished := n.hasFinished(patientID, itemID); !finished {
				return newError(ErrNotFound, "患者 %q 没有项目 %q 的申请项", patientID, itemID)
			}
		}
	}
	open := make([]*naiveItem, 0, len(itemIDs))
	for _, itemID := range itemIDs {
		if it := n.findOpen(patientID, itemID); it != nil {
			open = append(open, it)
		}
	}
	for _, it := range open {
		if it.spec.TubeType != tubeType {
			return newError(ErrTubeTypeMismatch, "项目 %q 管类别不一致", it.itemID)
		}
	}
	if n.findTube(tubeID) != nil {
		return newError(ErrStateMismatch, "标本管 %q 已存在", tubeID)
	}
	for _, itemID := range itemIDs {
		if n.findOpen(patientID, itemID) == nil {
			if _, finished := n.hasFinished(patientID, itemID); finished {
				return newError(ErrStateMismatch, "项目 %q 已终结", itemID)
			}
		}
	}
	for _, it := range open {
		if it.status != StatusPending {
			return newError(ErrStateMismatch, "项目 %q 不是待采集", it.itemID)
		}
	}
	if collectTime > now {
		return newError(ErrTimeUnreasonable, "采集时刻晚于 now")
	}
	for _, it := range open {
		if collectTime < it.pendingSince {
			return newError(ErrTimeUnreasonable, "采集时刻早于进入待采集时刻")
		}
	}
	tb := &naiveTube{id: tubeID, tubeType: tubeType, patientID: patientID, state: TubeAwaitingSign}
	for _, it := range open {
		it.status = StatusCollected
		it.collectTime = collectTime
		it.tubeID = tubeID
		tb.itemIDs = append(tb.itemIDs, it.itemID)
	}
	n.tubes = append(n.tubes, tb)
	n.last, n.started = now, true
	return nil
}

func (n *naiveSystem) registerTransport(now int64, tubeID string, cold bool) error {
	if tubeID == "" {
		return newError(ErrInvalidParam, "送出登记参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	tb := n.findTube(tubeID)
	if tb == nil {
		return newError(ErrNotFound, "标本管 %q 不存在", tubeID)
	}
	if tb.state != TubeAwaitingSign {
		return newError(ErrStateMismatch, "标本管 %q 不能登记运送方式", tubeID)
	}
	tb.cold = cold
	n.last, n.started = now, true
	return nil
}

func (n *naiveSystem) sign(now int64, tubeID string, hemolysis int) ([]ItemVerdict, error) {
	if tubeID == "" || hemolysis < 0 || hemolysis > MaxHemolysisLevel {
		return nil, newError(ErrInvalidParam, "签收参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return nil, err
	}
	tb := n.findTube(tubeID)
	if tb == nil {
		return nil, newError(ErrNotFound, "标本管 %q 不存在", tubeID)
	}
	if tb.state != TubeAwaitingSign || len(tb.itemIDs) == 0 {
		return nil, newError(ErrStateMismatch, "标本管 %q 不在待签收状态", tubeID)
	}
	sorted := append([]string(nil), tb.itemIDs...)
	sort.Strings(sorted)
	verdicts := make([]ItemVerdict, 0, len(sorted))
	for _, itemID := range sorted {
		it := n.findOpen(tb.patientID, itemID)
		var reason RejectReason
		switch {
		case now-it.collectTime > it.spec.MaxDelivery:
			reason = RejectTimeout
		case it.spec.RequireCold && !tb.cold:
			reason = RejectColdChain
		case hemolysis > it.spec.MaxHemolysis:
			reason = RejectHemolysis
		default:
			reason = RejectNone
		}
		verdict := ItemVerdict{ItemID: itemID, Reason: reason}
		if reason == RejectNone {
			verdict.Accepted = true
			verdict.NewStatus = StatusQualified
			it.status = StatusQualified
		} else {
			it.rejections++
			it.lastReject = reason
			if it.rejections >= MaxRejections {
				verdict.NewStatus = StatusTerminated
				it.status = StatusTerminated
			} else {
				verdict.NewStatus = StatusPending
				it.status = StatusPending
				it.pendingSince = now
				it.tubeID = ""
			}
		}
		verdicts = append(verdicts, verdict)
	}
	tb.state = TubeSigned
	tb.itemIDs = nil
	n.last, n.started = now, true
	return verdicts, nil
}

func (n *naiveSystem) cancel(now int64, patientID, itemID string) error {
	if patientID == "" || itemID == "" {
		return newError(ErrInvalidParam, "取消参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	patientKnown := false
	for _, it := range n.history {
		if it.patientID == patientID {
			patientKnown = true
			break
		}
	}
	if !patientKnown {
		return newError(ErrNotFound, "患者 %q 不存在", patientID)
	}
	it := n.findOpen(patientID, itemID)
	if it == nil {
		if _, finished := n.hasFinished(patientID, itemID); finished {
			return newError(ErrStateMismatch, "项目 %q 已终结，不能取消", itemID)
		}
		return newError(ErrNotFound, "患者 %q 没有项目 %q 的申请项", patientID, itemID)
	}
	switch it.status {
	case StatusPending:
		it.status = StatusCancelled
	case StatusCollected:
		it.status = StatusCancelled
		tb := n.findTube(it.tubeID)
		rest := tb.itemIDs[:0]
		for _, id := range tb.itemIDs {
			if id != itemID {
				rest = append(rest, id)
			}
		}
		tb.itemIDs = rest
		if len(tb.itemIDs) == 0 {
			tb.state = TubeVoided
		}
	default:
		return newError(ErrStateMismatch, "项目 %q 状态不符", itemID)
	}
	n.last, n.started = now, true
	return nil
}

func (n *naiveSystem) queryPatient(now int64, patientID string) ([]ItemView, error) {
	if patientID == "" {
		return nil, newError(ErrInvalidParam, "查询参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return nil, err
	}
	patientKnown := false
	for _, it := range n.history {
		if it.patientID == patientID {
			patientKnown = true
			break
		}
	}
	if !patientKnown {
		return nil, newError(ErrNotFound, "患者 %q 不存在", patientID)
	}
	views := make([]ItemView, 0)
	for _, it := range n.history { // 朴素：扫描全量历史
		if it.patientID != patientID || it.status.Terminal() {
			continue
		}
		view := ItemView{
			ItemID:           it.itemID,
			AppID:            it.appID,
			Priority:         it.priority,
			Status:           it.status,
			Rejections:       it.rejections,
			LastRejectReason: it.lastReject,
		}
		if it.status == StatusCollected {
			remaining := it.spec.MaxDelivery - (now - it.collectTime)
			view.RemainingSec = &remaining
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool { return views[i].ItemID < views[j].ItemID })
	n.last, n.started = now, true
	return views, nil
}
