package staffingtest

import "ontology/staffing"

// RunOp 在真实服务上执行一步，返回与朴素模型同构的 Result。
func RunOp(s *staffing.Service, op Op) Result {
	switch op.Kind {
	case "AddPosition":
		return wrapErr(s.AddPosition(op.Now, staffing.PositionSpec{
			ID: op.Pos, BandLow: op.BandLow, BandHigh: op.BandHigh, Headcount: op.Total,
		}), nil)
	case "AddCandidate":
		return wrapErr(s.AddCandidate(op.Now, op.Cand), nil)
	case "AddException":
		return wrapErr(s.AddException(op.Now, op.ExcID, op.Pos, op.Quarter, op.Total), nil)
	case "SetFrozen":
		return wrapErr(s.SetFrozen(op.Now, op.Pos, op.Frozen), nil)
	case "AdjustHeadcount":
		return wrapErr(s.AdjustHeadcount(op.Now, op.Pos, op.Total), nil)
	case "Issue":
		id, err := s.IssueOffer(op.Now, op.Cand, op.Pos, op.Salary, op.Deadline)
		return wrapErr(err, []int64{id})
	case "IssueBatch":
		items := make([]staffing.BatchItem, len(op.Batch))
		for i, b := range op.Batch {
			items[i] = staffing.BatchItem{
				CandidateID: b.Cand, PositionID: op.Pos,
				Salary: b.Salary, Deadline: b.Deadline,
			}
		}
		ids, err := s.IssueBatch(op.Now, op.Pos, items)
		return wrapErr(err, ids)
	case "Respond":
		return wrapErr(s.Respond(op.Now, op.OfferID, op.Accept, op.Entry), nil)
	case "Onboard":
		return wrapErr(s.Onboard(op.Now, op.OfferID), nil)
	case "Withdraw":
		return wrapErr(s.Withdraw(op.Now, op.OfferID), nil)
	case "Cancel":
		return wrapErr(s.Cancel(op.Now, op.OfferID), nil)
	case "Leave":
		return wrapErr(s.Leave(op.Now, op.Cand), nil)
	case "GetOffer":
		_, err := s.GetOffer(op.Now, op.OfferID)
		return wrapErr(err, nil)
	case "Occupancy":
		_, _, _, err := s.Occupancy(op.Now, op.Pos)
		return wrapErr(err, nil)
	case "Snapshot":
		_, err := s.Snapshot(op.Now)
		return wrapErr(err, nil)
	default:
		panic("unknown kind " + op.Kind)
	}
}

func wrapErr(err error, ids []int64) Result {
	if err == nil {
		return Result{OK: true, Code: 0, Index: -1, IDs: ids}
	}
	e := err.(*staffing.Error)
	return Result{OK: false, Code: int(e.Code), Index: e.Index}
}

// ServiceView 把真实服务快照转成与朴素模型一致的 StateView。
func ServiceView(snap staffing.Snapshot) StateView {
	v := StateView{
		Now:        snap.Now,
		Positions:  map[string]MPositionView{},
		Candidates: map[string]bool{},
		Offers:     map[int64]MOfferView{},
		Exceptions: map[string]MExceptionView{},
		Occupied:   map[string]int{},
		Onboarded:  map[string]int{},
		Pending:    map[string]int{},
	}
	for id, p := range snap.Positions {
		v.Positions[id] = MPositionView{
			BandLow: p.BandLow, BandHigh: p.BandHigh,
			Headcount: p.Headcount, Frozen: p.Frozen,
		}
	}
	for c := range snap.Candidates {
		v.Candidates[c] = true
	}
	for id, o := range snap.Offers {
		v.Offers[id] = MOfferView{
			ID: o.ID, Candidate: o.CandidateID, Position: o.PositionID,
			Salary: o.Salary, Deadline: o.Deadline, IssuedAt: o.IssuedAt,
			Status: o.Status.String(), RespondedAt: o.RespondedAt,
			EntryDate: o.EntryDate, OnboardedAt: o.OnboardedAt,
			LeftAt: o.LeftAt, CanceledAt: o.CanceledAt,
			UsedException: o.UsedException,
		}
	}
	for id, e := range snap.Exceptions {
		v.Exceptions[id] = MExceptionView{
			Position: e.PositionID, Quarter: e.Quarter,
			Total: e.Total, Remaining: e.Remaining,
		}
	}
	for p := range snap.Positions {
		v.Onboarded[p] = snap.Onboarded[p]
		v.Pending[p] = snap.Pending[p]
		v.Occupied[p] = snap.Occupied[p]
	}
	return v
}
