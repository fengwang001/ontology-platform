package system

import (
	"fmt"

	"ontology/domain"
	"ontology/index"
)

// Scrap 报废对象。报废为终态，之后该对象不再接受任何操作。
// 设备报废时其挂接附件自动脱离；附件报废时自动从设备摘除。
func (s *System) Scrap(id string, date int) error {
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
	if obj.Status == domain.StatusInService {
		s.idx.Remove(obj.Cat, index.Entry{Expiry: obj.Expiry, ID: obj.ID})
	}
	if obj.Cat.IsDevice() {
		for accID := range obj.Attach {
			s.objects[accID].HostID = ""
		}
		obj.Attach = make(map[string]bool)
	} else if obj.HostID != "" {
		delete(s.objects[obj.HostID].Attach, obj.ID)
		obj.HostID = ""
	}
	obj.Status = domain.StatusScrapped
	obj.SealAnchor = 0
	s.advanceClock(date)
	return nil
}
