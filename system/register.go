package system

import (
	"fmt"

	"ontology/calendar"
	"ontology/domain"
	"ontology/index"
)

// Register 登记新对象，附带首次检验合格日期；
// 到期日 = 首次检验合格日期 + 一个检验周期（日历月）。
func (s *System) Register(id string, cat domain.Category, firstPassDate int) error {
	if id == "" {
		return domain.NewError(domain.ErrInvalidParam, "编号为空")
	}
	if !cat.Valid() {
		return domain.NewError(domain.ErrInvalidParam, fmt.Sprintf("未知类别: %q", cat))
	}
	if firstPassDate < 0 {
		return domain.NewError(domain.ErrInvalidParam, fmt.Sprintf("日期非法: %d", firstPassDate))
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClock(firstPassDate); err != nil {
		return err
	}
	if _, ok := s.objects[id]; ok {
		return domain.NewError(domain.ErrInvalidParam, "编号已存在: "+id)
	}
	cfg := s.configs[cat]
	obj := &Object{
		ID:     id,
		Cat:    cat,
		Status: domain.StatusInService,
		Expiry: calendar.AddMonths(firstPassDate, cfg.PeriodMonths),
	}
	if cat.IsDevice() {
		obj.Attach = make(map[string]bool)
	}
	s.objects[id] = obj
	s.idx.Upsert(cat, index.Entry{Expiry: obj.Expiry, ID: obj.ID})
	s.advanceClock(firstPassDate)
	return nil
}
