package system

import (
	"fmt"

	"ontology/domain"
)

// Attach 将附件挂接到设备；附件已挂在其他设备时视为转移。
// 要求附件当前未超期、未封存、未停用。
func (s *System) Attach(deviceID, accID string, date int) error {
	if deviceID == "" || accID == "" {
		return domain.NewError(domain.ErrInvalidParam, "编号为空")
	}
	if deviceID == accID {
		return domain.NewError(domain.ErrInvalidParam, "设备与附件编号相同")
	}
	if date < 0 {
		return domain.NewError(domain.ErrInvalidParam, fmt.Sprintf("日期非法: %d", date))
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClock(date); err != nil {
		return err
	}
	dev, err := s.get(deviceID)
	if err != nil {
		return err
	}
	acc, err := s.get(accID)
	if err != nil {
		return err
	}
	if !dev.Cat.IsDevice() {
		return domain.NewError(domain.ErrInvalidParam, "挂接目标不是设备: "+deviceID)
	}
	if !acc.Cat.IsAccessory() {
		return domain.NewError(domain.ErrInvalidParam, "被挂接对象不是附件: "+accID)
	}
	if acc.HostID == deviceID {
		return domain.NewError(domain.ErrStateNotAllowed, "附件已挂接在该设备上: "+accID)
	}
	switch {
	case acc.Status == domain.StatusSealed:
		return domain.NewError(domain.ErrConditionUnmet, "附件处于封存状态，不得挂接: "+accID)
	case acc.Status == domain.StatusSuspended:
		return domain.NewError(domain.ErrConditionUnmet, "附件处于停用状态，不得挂接: "+accID)
	case acc.overdue(date):
		return domain.NewError(domain.ErrConditionUnmet,
			fmt.Sprintf("附件已超期（到期日 %d，当日 %d），不得挂接: %s", acc.Expiry, date, accID))
	}
	if acc.HostID != "" {
		delete(s.objects[acc.HostID].Attach, accID) // 从旧设备转移
	}
	acc.HostID = deviceID
	dev.Attach[accID] = true
	s.advanceClock(date)
	return nil
}

// Detach 将附件从其设备上摘除。附件须当前挂接在某台设备上。
func (s *System) Detach(accID string, date int) error {
	if accID == "" {
		return domain.NewError(domain.ErrInvalidParam, "编号为空")
	}
	if date < 0 {
		return domain.NewError(domain.ErrInvalidParam, fmt.Sprintf("日期非法: %d", date))
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClock(date); err != nil {
		return err
	}
	acc, err := s.get(accID)
	if err != nil {
		return err
	}
	if !acc.Cat.IsAccessory() {
		return domain.NewError(domain.ErrInvalidParam, "对象不是附件: "+accID)
	}
	if acc.HostID == "" {
		return domain.NewError(domain.ErrStateNotAllowed, "附件未挂接在任何设备上: "+accID)
	}
	delete(s.objects[acc.HostID].Attach, accID)
	acc.HostID = ""
	s.advanceClock(date)
	return nil
}
