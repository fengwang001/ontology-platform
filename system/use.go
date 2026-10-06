package system

import (
	"fmt"
	"sort"

	"ontology/domain"
)

// UseDenial 是使用登记被拒绝时的原因（第一个不满足的项）。
type UseDenial struct {
	Reason string // 原因类别：sealed / suspended / overdue / no_safety_valve / accessory
	AccID  string // 附件不满足时，编号最小的附件编号
	Detail string // 可读说明
}

// usableLocked 判定设备在 date 当日是否可使用，并给出第一个不满足的原因。
// 原因次序：设备自身封存 > 设备自身停用 > 设备自身超期 > 缺少安全阀 > 附件不满足。
// 开销仅与该设备的附件数相关。
func usableLocked(dev *Object, s *System, date int) (bool, *UseDenial) {
	switch dev.Status {
	case domain.StatusSealed:
		return false, &UseDenial{Reason: "sealed", Detail: "设备自身封存"}
	case domain.StatusSuspended:
		return false, &UseDenial{Reason: "suspended", Detail: "设备自身停用"}
	}
	if dev.overdue(date) {
		return false, &UseDenial{Reason: "overdue",
			Detail: fmt.Sprintf("设备自身超期（到期日 %d，当日 %d）", dev.Expiry, date)}
	}
	accs := make([]string, 0, len(dev.Attach))
	for id := range dev.Attach {
		accs = append(accs, id)
	}
	sort.Strings(accs)
	hasValve := false
	for _, id := range accs {
		if s.objects[id].Cat == domain.CatSafetyValve {
			hasValve = true
			break
		}
	}
	if !hasValve {
		return false, &UseDenial{Reason: "no_safety_valve", Detail: "缺少安全阀"}
	}
	for _, id := range accs {
		acc := s.objects[id]
		switch {
		case acc.Status == domain.StatusSealed:
			return false, &UseDenial{Reason: "accessory", AccID: id, Detail: "附件封存: " + id}
		case acc.Status == domain.StatusSuspended:
			return false, &UseDenial{Reason: "accessory", AccID: id, Detail: "附件停用: " + id}
		case acc.overdue(date):
			return false, &UseDenial{Reason: "accessory", AccID: id,
				Detail: fmt.Sprintf("附件超期（到期日 %d，当日 %d）: %s", acc.Expiry, date, id)}
		}
	}
	return true, nil
}

// checkDevice 校验设备参数（存在性、报废、类别），供使用登记与判定共用。
func (s *System) checkDevice(deviceID string) (*Object, error) {
	dev, err := s.get(deviceID)
	if err != nil {
		return nil, err
	}
	if !dev.Cat.IsDevice() {
		return nil, domain.NewError(domain.ErrInvalidParam, "对象不是设备: "+deviceID)
	}
	return dev, nil
}

// RegisterUse 在 date 当日登记设备使用。按当日判定可使用性；
// 不可用时拒绝（不改变任何状态与时钟），并给出第一个不满足的原因。
// 被接受时推进时钟。
func (s *System) RegisterUse(deviceID string, date int) (bool, *UseDenial, error) {
	if deviceID == "" {
		return false, nil, domain.NewError(domain.ErrInvalidParam, "编号为空")
	}
	if date < 0 {
		return false, nil, domain.NewError(domain.ErrInvalidParam, fmt.Sprintf("日期非法: %d", date))
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClock(date); err != nil {
		return false, nil, err
	}
	dev, err := s.checkDevice(deviceID)
	if err != nil {
		return false, nil, err
	}
	ok, denial := usableLocked(dev, s, date)
	if !ok {
		return false, denial, nil
	}
	s.advanceClock(date)
	return true, nil, nil
}

// Usable 是只读查询：判定设备在 date 当日是否可使用。
// 不检查日期回退，也不推进时钟。
func (s *System) Usable(deviceID string, date int) (bool, *UseDenial, error) {
	if deviceID == "" {
		return false, nil, domain.NewError(domain.ErrInvalidParam, "编号为空")
	}
	if date < 0 {
		return false, nil, domain.NewError(domain.ErrInvalidParam, fmt.Sprintf("日期非法: %d", date))
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	dev, err := s.checkDevice(deviceID)
	if err != nil {
		return false, nil, err
	}
	ok, denial := usableLocked(dev, s, date)
	return ok, denial, nil
}
