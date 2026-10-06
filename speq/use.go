package speq

import "sort"

// UsableReport 设备在某日的可使用性判定结果。
type UsableReport struct {
	Usable     bool
	Reason     RejectReason
	Attachment string
	Detail     string
}

// usabilityLocked 判定设备在 date 日是否可使用。
//
// 原因次序（取第一个不满足项）：
//  1. 设备自身封存
//  2. 设备自身停用
//  3. 设备自身超期
//  4. 缺少安全阀
//  5. 附件不满足（编号最小的附件：封存 > 停用 > 超期）
//
// 开销只与该设备的附件数 k 相关：O(k)。
// 调用方须持锁。
func (s *System) usabilityLocked(d *obj, date int) UsableReport {
	if d.sealed {
		return UsableReport{Reason: RejectDeviceSealed, Detail: "设备封存中"}
	}
	if d.disabled {
		return UsableReport{Reason: RejectDeviceDisabled, Detail: "设备不合格停用"}
	}
	if date > d.expiry {
		return UsableReport{Reason: RejectDeviceExpired, Detail: "设备已超期"}
	}

	hasSafetyValve := false
	set := s.attachments[d.id]
	ids := make([]string, 0, len(set))
	for aid := range set {
		ids = append(ids, aid)
	}
	sort.Strings(ids)
	var worstID, worstDetail string
	var worstReason RejectReason
	// 附件按编号升序遍历，命中的第一个即为编号最小的不满足附件。
	for _, aid := range ids {
		a := s.objects[aid]
		if a == nil || a.scrapped {
			continue
		}
		if a.kind == KindSafetyValve {
			hasSafetyValve = true
		}
		var reason RejectReason
		var detail string
		switch {
		case a.sealed:
			reason = RejectAttachment
			detail = "封存中"
		case a.disabled:
			reason = RejectAttachment
			detail = "不合格停用"
		case date > a.expiry:
			reason = RejectAttachment
			detail = "已超期"
		}
		if reason == RejectAttachment && worstID == "" {
			worstID = aid
			worstDetail = detail
			worstReason = reason
		}
	}
	if !hasSafetyValve {
		return UsableReport{Reason: RejectNoSafetyValve, Detail: "未挂接任何安全阀"}
	}
	if worstReason == RejectAttachment {
		return UsableReport{Reason: RejectAttachment, Attachment: worstID, Detail: worstDetail}
	}
	return UsableReport{Usable: true}
}

// RegisterUse 在 date 日登记设备使用；被拒绝时返回 *RejectError，
// 不改变任何状态与日期。
func (s *System) RegisterUse(date int, deviceID string) error {
	if date < 0 || !validID(deviceID) {
		return errf(ErrInvalidParameter, "使用登记参数非法: date=%d device=%q", date, deviceID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// 日期回退属于操作级错误，优先于对象判定。
	if date < s.lastDate {
		return errf(ErrDateRegression, "操作日期 %d 早于上一接受日期 %d", date, s.lastDate)
	}
	d, ok := s.objects[deviceID]
	if !ok {
		return errf(ErrNotFound, "对象不存在: %q", deviceID)
	}
	if d.scrapped {
		return errf(ErrScrapped, "对象已报废: %q", deviceID)
	}
	if d.kind != KindDevice {
		return errf(ErrIllegalState, "对象不是设备: %q", deviceID)
	}

	r := s.usabilityLocked(d, date)
	if !r.Usable {
		return &RejectError{Reason: r.Reason, Attachment: r.Attachment, Detail: r.Detail}
	}
	// 接受使用登记即推进操作日期（设备无额外可写状态）。
	s.lastDate = date
	return nil
}

// CheckUsable 只读判定设备在 date 日的可使用性（不推进操作日期）。
func (s *System) CheckUsable(date int, deviceID string) (UsableReport, error) {
	if date < 0 || !validID(deviceID) {
		return UsableReport{}, errf(ErrInvalidParameter, "判定参数非法: date=%d device=%q", date, deviceID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.objects[deviceID]
	if !ok {
		return UsableReport{}, errf(ErrNotFound, "对象不存在: %q", deviceID)
	}
	if d.scrapped {
		return UsableReport{}, errf(ErrScrapped, "对象已报废: %q", deviceID)
	}
	if d.kind != KindDevice {
		return UsableReport{}, errf(ErrIllegalState, "对象不是设备: %q", deviceID)
	}
	return s.usabilityLocked(d, date), nil
}
