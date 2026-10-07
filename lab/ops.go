package lab

import "sort"

// 本文件实现 System 的各个操作。所有实现遵循统一的校验次序：
// 参数非法 > 时钟回退 > 对象不存在 > 重复申请 > 管类别不一致 > 状态不符 > 时间不合理，
// 全部校验通过后才修改任何状态并推进时钟；被拒绝的操作不改变任何状态与时钟。

// distinctIDs 校验标识列表长度在 [min,max] 内、元素非空且互不重复。
func distinctIDs(ids []string, min, max int) bool {
	if len(ids) < min || (max > 0 && len(ids) > max) {
		return false
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			return false
		}
		if _, dup := seen[id]; dup {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

func (s *System) registerItem(now int64, itemID, tubeType string, maxDeliverySec int64, requireCold bool, maxHemolysis int) error {
	if itemID == "" || tubeType == "" || maxDeliverySec <= 0 ||
		maxHemolysis < 0 || maxHemolysis > MaxHemolysisLevel {
		return newError(ErrInvalidParam, "登记项目参数非法: itemID=%q tubeType=%q maxDelivery=%d hemolysis=%d",
			itemID, tubeType, maxDeliverySec, maxHemolysis)
	}
	if err := s.clock.Check(now); err != nil {
		return err
	}
	s.catalog.upsert(itemID, ItemSpec{
		TubeType:     tubeType,
		MaxDelivery:  maxDeliverySec,
		RequireCold:  requireCold,
		MaxHemolysis: maxHemolysis,
	})
	s.clock.Advance(now)
	return nil
}

func (s *System) submitApplication(now int64, appID, patientID string, itemIDs []string, priority int) error {
	if appID == "" || patientID == "" || !distinctIDs(itemIDs, 1, MaxItemsPerApplication) {
		return newError(ErrInvalidParam, "申请参数非法: appID=%q patientID=%q items=%v", appID, patientID, itemIDs)
	}
	if err := s.clock.Check(now); err != nil {
		return err
	}
	for _, itemID := range itemIDs {
		if _, ok := s.catalog.snapshot(itemID); !ok {
			return newError(ErrNotFound, "检验项目 %q 未在目录登记", itemID)
		}
	}
	if _, dup := s.appIDs[appID]; dup {
		return newError(ErrDuplicate, "申请标识 %q 已存在", appID)
	}
	patient := s.patients[patientID]
	if patient != nil {
		for _, itemID := range itemIDs {
			if _, dup := patient.items[itemID]; dup {
				return newError(ErrDuplicate, "患者 %q 的项目 %q 已有未终结申请项", patientID, itemID)
			}
		}
	}
	if patient == nil {
		patient = newPatient(patientID)
		s.patients[patientID] = patient
	}
	for _, itemID := range itemIDs {
		spec, _ := s.catalog.snapshot(itemID)
		patient.items[itemID] = &AppItem{
			ItemID:       itemID,
			AppID:        appID,
			PatientID:    patientID,
			Priority:     priority,
			spec:         spec,
			Status:       StatusPending,
			LastReject:   RejectNone,
			PendingSince: now,
		}
		delete(patient.finished, itemID)
	}
	s.appIDs[appID] = struct{}{}
	s.clock.Advance(now)
	return nil
}

func (s *System) collect(now int64, tubeID, tubeType, patientID string, itemIDs []string, collectTime int64) error {
	if tubeID == "" || tubeType == "" || patientID == "" || !distinctIDs(itemIDs, 1, 0) {
		return newError(ErrInvalidParam, "采集参数非法: tubeID=%q tubeType=%q patientID=%q items=%v",
			tubeID, tubeType, patientID, itemIDs)
	}
	if err := s.clock.Check(now); err != nil {
		return err
	}
	patient := s.patients[patientID]
	if patient == nil {
		return newError(ErrNotFound, "患者 %q 不存在", patientID)
	}
	for _, itemID := range itemIDs {
		if _, ok := patient.items[itemID]; !ok {
			if _, finished := patient.finished[itemID]; !finished {
				return newError(ErrNotFound, "患者 %q 没有项目 %q 的未终结申请项", patientID, itemID)
			}
		}
	}
	items := make([]*AppItem, 0, len(itemIDs))
	for _, itemID := range itemIDs {
		if it, ok := patient.items[itemID]; ok {
			items = append(items, it)
		}
	}
	for _, it := range items {
		if it.spec.TubeType != tubeType {
			return newError(ErrTubeTypeMismatch, "项目 %q 登记管类别为 %q，与采集管类别 %q 不一致",
				it.ItemID, it.spec.TubeType, tubeType)
		}
	}
	if _, exists := s.tubes[tubeID]; exists {
		return newError(ErrStateMismatch, "标本管 %q 已存在，不能重复登记", tubeID)
	}
	for _, itemID := range itemIDs {
		if status, finished := patient.finished[itemID]; finished {
			return newError(ErrStateMismatch, "患者 %q 的项目 %q 已终结（%s），不能采集", patientID, itemID, status)
		}
	}
	for _, it := range items {
		if it.Status != StatusPending {
			return newError(ErrStateMismatch, "项目 %q 状态为 %s，不是待采集", it.ItemID, it.Status)
		}
	}
	if collectTime > now {
		return newError(ErrTimeUnreasonable, "采集时刻 %d 晚于 now=%d", collectTime, now)
	}
	for _, it := range items {
		if collectTime < it.PendingSince {
			return newError(ErrTimeUnreasonable, "采集时刻 %d 早于项目 %q 进入待采集的时刻 %d",
				collectTime, it.ItemID, it.PendingSince)
		}
	}
	tube := newTube(tubeID, tubeType, patientID)
	for _, it := range items {
		it.Status = StatusCollected
		it.CollectTime = collectTime
		it.TubeID = tubeID
		tube.items[it.ItemID] = it
	}
	s.tubes[tubeID] = tube
	s.clock.Advance(now)
	return nil
}

func (s *System) registerTransport(now int64, tubeID string, cold bool) error {
	if tubeID == "" {
		return newError(ErrInvalidParam, "送出登记参数非法: tubeID 为空")
	}
	if err := s.clock.Check(now); err != nil {
		return err
	}
	tube, ok := s.tubes[tubeID]
	if !ok {
		return newError(ErrNotFound, "标本管 %q 不存在", tubeID)
	}
	if tube.State != TubeAwaitingSign {
		return newError(ErrStateMismatch, "标本管 %q 已签收或已作废，不能登记运送方式", tubeID)
	}
	tube.Cold = cold
	tube.Shipped = true
	s.clock.Advance(now)
	return nil
}

// judge 按 超时 > 冷链不符 > 溶血超限 的优先次序给出第一个拒收原因。
func judge(it *AppItem, tube *Tube, signTime int64, hemolysis int) RejectReason {
	if signTime-it.CollectTime > it.spec.MaxDelivery {
		return RejectTimeout
	}
	if it.spec.RequireCold && !tube.Cold {
		return RejectColdChain
	}
	if hemolysis > it.spec.MaxHemolysis {
		return RejectHemolysis
	}
	return RejectNone
}

func (s *System) sign(now int64, tubeID string, hemolysis int) ([]ItemVerdict, error) {
	if tubeID == "" || hemolysis < 0 || hemolysis > MaxHemolysisLevel {
		return nil, newError(ErrInvalidParam, "签收参数非法: tubeID=%q hemolysis=%d", tubeID, hemolysis)
	}
	if err := s.clock.Check(now); err != nil {
		return nil, err
	}
	tube, ok := s.tubes[tubeID]
	if !ok {
		return nil, newError(ErrNotFound, "标本管 %q 不存在", tubeID)
	}
	if tube.State != TubeAwaitingSign || len(tube.items) == 0 {
		return nil, newError(ErrStateMismatch, "标本管 %q 不在待签收状态", tubeID)
	}
	patient := s.patients[tube.PatientID]
	keys := make([]string, 0, len(tube.items))
	for itemID := range tube.items {
		keys = append(keys, itemID)
	}
	sort.Strings(keys)
	s.Metrics.scan(len(keys))
	verdicts := make([]ItemVerdict, 0, len(keys))
	for _, itemID := range keys {
		it := tube.items[itemID]
		reason := judge(it, tube, now, hemolysis)
		verdict := ItemVerdict{ItemID: itemID, Reason: reason}
		if reason == RejectNone {
			verdict.Accepted = true
			verdict.NewStatus = StatusQualified
			patient.finish(it, StatusQualified)
		} else {
			it.Rejections++
			it.LastReject = reason
			if it.Rejections >= MaxRejections {
				verdict.NewStatus = StatusTerminated
				patient.finish(it, StatusTerminated)
			} else {
				verdict.NewStatus = StatusPending
				it.Status = StatusPending
				it.PendingSince = now
				it.TubeID = ""
			}
		}
		verdicts = append(verdicts, verdict)
	}
	tube.State = TubeSigned
	tube.SignTime = now
	tube.items = make(map[string]*AppItem)
	s.clock.Advance(now)
	return verdicts, nil
}

func (s *System) cancel(now int64, patientID, itemID string) error {
	if patientID == "" || itemID == "" {
		return newError(ErrInvalidParam, "取消参数非法: patientID=%q itemID=%q", patientID, itemID)
	}
	if err := s.clock.Check(now); err != nil {
		return err
	}
	patient := s.patients[patientID]
	if patient == nil {
		return newError(ErrNotFound, "患者 %q 不存在", patientID)
	}
	it, ok := patient.items[itemID]
	if !ok {
		if status, finished := patient.finished[itemID]; finished {
			return newError(ErrStateMismatch, "项目 %q 已终结（%s），不能取消", itemID, status)
		}
		return newError(ErrNotFound, "患者 %q 没有项目 %q 的申请项", patientID, itemID)
	}
	switch it.Status {
	case StatusPending:
		// 直接取消。
	case StatusCollected:
		tube := s.tubes[it.TubeID]
		delete(tube.items, it.ItemID)
		if len(tube.items) == 0 {
			tube.State = TubeVoided
		}
	default:
		return newError(ErrStateMismatch, "项目 %q 状态为 %s，不能取消", itemID, it.Status)
	}
	patient.finish(it, StatusCancelled)
	s.clock.Advance(now)
	return nil
}

func (s *System) queryPatient(now int64, patientID string) ([]ItemView, error) {
	if patientID == "" {
		return nil, newError(ErrInvalidParam, "查询参数非法: patientID 为空")
	}
	if err := s.clock.Check(now); err != nil {
		return nil, err
	}
	patient, ok := s.patients[patientID]
	if !ok {
		return nil, newError(ErrNotFound, "患者 %q 不存在", patientID)
	}
	keys := make([]string, 0, len(patient.items))
	for itemID := range patient.items {
		keys = append(keys, itemID)
	}
	sort.Strings(keys)
	s.Metrics.scan(len(keys))
	views := make([]ItemView, 0, len(keys))
	for _, itemID := range keys {
		it := patient.items[itemID]
		view := ItemView{
			ItemID:           it.ItemID,
			AppID:            it.AppID,
			Priority:         it.Priority,
			Status:           it.Status,
			Rejections:       it.Rejections,
			LastRejectReason: it.LastReject,
		}
		if it.Status == StatusCollected {
			remaining := it.spec.MaxDelivery - (now - it.CollectTime)
			view.RemainingSec = &remaining
		}
		views = append(views, view)
	}
	s.clock.Advance(now)
	return views, nil
}
