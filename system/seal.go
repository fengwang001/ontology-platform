package system

import (
	"fmt"

	"ontology/domain"
	"ontology/index"
)

// Seal 封存对象，暂停计时。要求对象当前在用（未封存、未停用）且未超期。
func (s *System) Seal(id string, date int) error {
	if id == "" {
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
	obj, err := s.get(id)
	if err != nil {
		return err
	}
	if obj.Status != domain.StatusInService {
		return domain.NewError(domain.ErrStateNotAllowed,
			fmt.Sprintf("仅在用对象可封存，当前状态: %s", obj.Status))
	}
	if obj.overdue(date) {
		return domain.NewError(domain.ErrConditionUnmet,
			fmt.Sprintf("对象已超期（到期日 %d，当日 %d），不得封存", obj.Expiry, date))
	}
	s.idx.Remove(obj.Cat, index.Entry{Expiry: obj.Expiry, ID: obj.ID})
	obj.Status = domain.StatusSealed
	obj.SealAnchor = date
	s.advanceClock(date)
	return nil
}

// Unseal 启封对象。到期日顺延 启封日-锚点日 天（锚点为封存当日或封存期内
// 最近一次检验日，因此封存期检验重算过的到期日不会被重复顺延）。
// 顺延后若自启封日起剩余有效天数（含启封当日与到期当日）少于该类别的
// 启封最小保障天数，则拒绝启封，对象保持封存。
func (s *System) Unseal(id string, date int) error {
	if id == "" {
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
	obj, err := s.get(id)
	if err != nil {
		return err
	}
	if obj.Status != domain.StatusSealed {
		return domain.NewError(domain.ErrStateNotAllowed,
			fmt.Sprintf("仅封存对象可启封，当前状态: %s", obj.Status))
	}
	cfg := s.configs[obj.Cat]
	newExpiry := obj.Expiry + (date - obj.SealAnchor)
	remaining := newExpiry - date + 1
	if remaining < cfg.MinUnsealDays {
		return domain.NewError(domain.ErrConditionUnmet,
			fmt.Sprintf("启封保障天数不足：顺延后剩余有效 %d 天，少于最小保障 %d 天", remaining, cfg.MinUnsealDays))
	}
	obj.Expiry = newExpiry
	obj.Status = domain.StatusInService
	obj.SealAnchor = 0
	s.idx.Upsert(obj.Cat, index.Entry{Expiry: obj.Expiry, ID: obj.ID})
	s.advanceClock(date)
	return nil
}
