package staffingtest

// MOfferView 是通知的可比较视图。
type MOfferView struct {
	ID            int64
	Candidate     string
	Position      string
	Salary        int
	Deadline      int
	IssuedAt      int
	Status        string
	RespondedAt   int
	EntryDate     int
	OnboardedAt   int
	LeftAt        int
	CanceledAt    int
	UsedException bool
}

// MPositionView 是岗位可比较视图。
type MPositionView struct {
	BandLow   int
	BandHigh  int
	Headcount int
	Frozen    bool
}

// MExceptionView 是例外额度可比较视图。
type MExceptionView struct {
	Position  string
	Quarter   int
	Total     int
	Remaining int
}

// StateView 是朴素模型的完整可比较状态。
type StateView struct {
	Now        int
	Positions  map[string]MPositionView
	Candidates map[string]bool
	Offers     map[int64]MOfferView
	Exceptions map[string]MExceptionView
	Occupied   map[string]int
	Onboarded  map[string]int
	Pending    map[string]int
}

// View 导出当前状态（线性重算占用）。
func (m *Model) View() StateView {
	v := StateView{
		Now:        m.now,
		Positions:  map[string]MPositionView{},
		Candidates: map[string]bool{},
		Offers:     map[int64]MOfferView{},
		Exceptions: map[string]MExceptionView{},
		Occupied:   map[string]int{},
		Onboarded:  map[string]int{},
		Pending:    map[string]int{},
	}
	for id, p := range m.positions {
		v.Positions[id] = MPositionView{
			BandLow: p.bandLow, BandHigh: p.bandHigh,
			Headcount: p.headcount, Frozen: p.frozen,
		}
	}
	for c := range m.candidates {
		v.Candidates[c] = true
	}
	for id, o := range m.offers {
		v.Offers[id] = MOfferView{
			ID: o.id, Candidate: o.candidate, Position: o.position,
			Salary: o.salary, Deadline: o.deadline, IssuedAt: o.issuedAt,
			Status: string(o.status), RespondedAt: o.respondedAt,
			EntryDate: o.entryDate, OnboardedAt: o.onboardedAt,
			LeftAt: o.leftAt, CanceledAt: o.canceledAt,
			UsedException: o.usedException,
		}
		switch {
		case o.status == MOnboarded && o.leftAt < 0:
			v.Onboarded[o.position]++
		case o.status == MPending || o.status == MAccepted:
			v.Pending[o.position]++
		}
	}
	for id, e := range m.exceptions {
		v.Exceptions[id] = MExceptionView{
			Position: e.position, Quarter: e.quarter,
			Total: e.total, Remaining: e.remaining,
		}
	}
	for p := range m.positions {
		v.Onboarded[p] += 0
		v.Pending[p] += 0
		v.Occupied[p] = v.Onboarded[p] + v.Pending[p]
	}
	return v
}
