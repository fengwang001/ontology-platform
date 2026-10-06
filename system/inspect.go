package system

import (
	"fmt"

	"ontology/calendar"
	"ontology/domain"
	"ontology/index"
)

// Inspect 对对象执行一次检验。
//
// 规则：
//   - 合格：检验日期落在 [原到期日-提前窗口, 原到期日] 内（含两端）时，
//     新到期日以原到期日为基准加一个检验周期；否则（过早或已超期）
//     以检验日期为基准加一个检验周期。
//   - 有条件合格：附带整改限期天数 rectDays，新到期日取按合格规则算得的
//     日期与 检验日+rectDays 两者中较早者。
//   - 不合格：对象立即停用，到期日不变；封存中的对象检验不合格亦转为停用
//     （封存终止）。复检合格后恢复在用。
//
// rectDays 仅在有条件合格时允许 >0。
func (s *System) Inspect(id string, date int, result domain.InspectResult, rectDays int) error {
	if id == "" {
		return domain.NewError(domain.ErrInvalidParam, "编号为空")
	}
	if date < 0 {
		return domain.NewError(domain.ErrInvalidParam, fmt.Sprintf("日期非法: %d", date))
	}
	switch result {
	case domain.ResultPass, domain.ResultFail:
		if rectDays != 0 {
			return domain.NewError(domain.ErrInvalidParam, "非有条件合格时整改限期天数须为 0")
		}
	case domain.ResultConditional:
		if rectDays <= 0 {
			return domain.NewError(domain.ErrInvalidParam, "有条件合格须附带正的整改限期天数")
		}
	default:
		return domain.NewError(domain.ErrInvalidParam, fmt.Sprintf("未知检验结论: %d", result))
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
	cfg := s.configs[obj.Cat]

	switch result {
	case domain.ResultFail:
		// 立即停用，到期日不变；若原本在用则移出索引。
		if obj.Status == domain.StatusInService {
			s.idx.Remove(obj.Cat, index.Entry{Expiry: obj.Expiry, ID: obj.ID})
		}
		obj.Status = domain.StatusSuspended
		obj.SealAnchor = 0
	default: // 合格或有条件合格
		newExpiry := passExpiry(obj.Expiry, date, cfg)
		if result == domain.ResultConditional {
			if alt := date + rectDays; alt < newExpiry {
				newExpiry = alt
			}
		}
		wasInIndex := obj.Status == domain.StatusInService
		if wasInIndex {
			s.idx.Remove(obj.Cat, index.Entry{Expiry: obj.Expiry, ID: obj.ID})
		}
		obj.Expiry = newExpiry
		switch obj.Status {
		case domain.StatusSuspended:
			obj.Status = domain.StatusInService // 复检合格，恢复使用
		case domain.StatusSealed:
			obj.SealAnchor = date // 封存期检验：锚点移至检验日
		}
		if obj.Status == domain.StatusInService {
			s.idx.Upsert(obj.Cat, index.Entry{Expiry: obj.Expiry, ID: obj.ID})
		}
	}
	s.advanceClock(date)
	return nil
}

// passExpiry 按合格规则计算新到期日。
func passExpiry(oldExpiry, date int, cfg domain.Config) int {
	if date >= oldExpiry-cfg.EarlyWindowDays && date <= oldExpiry {
		return calendar.AddMonths(oldExpiry, cfg.PeriodMonths)
	}
	return calendar.AddMonths(date, cfg.PeriodMonths)
}
