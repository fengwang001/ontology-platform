package leasechain

import "sort"

// generateBillsLocked 为截至 now 的全部已到应付日账期登记；账期随租约
// 实际期限（终止时 End 收缩）生成，登记后重复结算不会重复生成。
func (s *Service) generateBillsLocked(now int) {
	for _, l := range s.leases {
		for _, b := range installments(l.Start, l.End, s.cfg.PayDay, l.Rent) {
			if b.due > now {
				continue
			}
			k := billKey{leaseID: l.ID, due: b.due}
			if _, ok := s.bills[k]; ok {
				continue
			}
			b.leaseID = l.ID
			s.bills[k] = &b
		}
	}
}

// createArrearsLocked 为逾期恰达 G 天（now >= due+G）且尚未足额清偿的账期
// 生成欠费；一笔账期至多一笔欠费，按 (due, leaseID) 确定次序。
func (s *Service) createArrearsLocked(now int) {
	type ref struct {
		leaseID int64
		due     int
		amount  int64
	}
	var pending []ref
	for k, b := range s.bills {
		if !isOverdue(k.due, now, s.cfg.G) {
			continue
		}
		if s.arrearByBillLocked(k.leaseID, k.due) != nil {
			continue
		}
		pending = append(pending, ref{k.leaseID, k.due, b.amount})
	}
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].due != pending[j].due {
			return pending[i].due < pending[j].due
		}
		return pending[i].leaseID < pending[j].leaseID
	})
	for _, p := range pending {
		s.nextID++
		s.arrears[s.nextID] = &Arrear{
			ID:      s.nextID,
			LeaseID: p.leaseID,
			Due:     p.due,
			Amount:  p.amount,
		}
	}
}

func (s *Service) arrearByBillLocked(leaseID int64, due int) *Arrear {
	for _, a := range s.arrears {
		if a.LeaseID == leaseID && a.Due == due {
			return a
		}
	}
	return nil
}

// expireLeasesLocked 按 (End, ID) 固定次序终止到期租约；级联中提升的租约
// 若也已到期，会在后续迭代中再次处理，与逐笔到期重放等价。
func (s *Service) expireLeasesLocked(now int) {
	for {
		var cand *Lease
		for _, l := range s.leases {
			if l.Terminated || l.End > now {
				continue
			}
			if cand == nil || l.End < cand.End || (l.End == cand.End && l.ID < cand.ID) {
				cand = l
			}
		}
		if cand == nil {
			return
		}
		s.cascadeLocked(cand, cand.End)
	}
}

// cascadeLocked 终止 l 并逐级处理其下级：已独立承认者提升到父级的父级
// （根为 0），未承认者一并终止并继续向下级联。
func (s *Service) cascadeLocked(l *Lease, termDate int) {
	l.Terminated = true
	l.End = termDate
	child := s.childLocked(l.ID)
	l.ChildID = 0
	if child == nil {
		return
	}
	if child.Recognized {
		newParent := l.ParentID
		child.ParentID = newParent
		if newParent != 0 {
			if gp := s.leases[newParent]; gp != nil {
				gp.ChildID = child.ID
			}
		}
		return
	}
	s.cascadeLocked(child, termDate)
}

// childLocked 找到父租约当前有效下级：优先用缓存的 ChildID 指针 O(1)，
// 并以 ParentID 权威字段兜底，避免缓存与链不一致时误判。
func (s *Service) childLocked(parentID int64) *Lease {
	p := s.leases[parentID]
	if p != nil && p.ChildID != 0 {
		c := s.leases[p.ChildID]
		if c != nil && !c.Terminated && c.ParentID == parentID {
			return c
		}
	}
	for _, c := range s.leases {
		if !c.Terminated && c.ParentID == parentID {
			return c
		}
	}
	return nil
}

// ancestorsLocked 返回 l 逐级向上的租约链（含 l 自身）。
func (s *Service) ancestorsLocked(l *Lease) []*Lease {
	var out []*Lease
	for cur := l; cur != nil; {
		out = append(out, cur)
		if cur.ParentID == 0 {
			break
		}
		cur = s.leases[cur.ParentID]
	}
	return out
}

// depthLocked 返回 l 的深度（直接租约为 0），沿父指针逐级行走。
func (s *Service) depthLocked(l *Lease) int {
	d := 0
	for cur := l; cur.ParentID != 0; d++ {
		cur = s.leases[cur.ParentID]
		if cur == nil {
			break
		}
	}
	return d
}
