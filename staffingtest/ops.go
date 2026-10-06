package staffingtest

import "fmt"

// Op 是一步操作；Kind 决定使用哪些字段。
type Op struct {
	Kind     string
	Now      int
	Pos      string
	Cand     string
	Salary   int
	Deadline int
	Entry    int
	OfferID  int64
	Frozen   bool
	Accept   bool
	Total    int
	BandLow  int
	BandHigh int
	Quarter  int
	ExcID    string
	Batch    []BatchMember
}

// BatchMember 是批量发放的一项。
type BatchMember struct {
	Cand     string
	Salary   int
	Deadline int
}

// 错误码数值与 staffing.Code 对齐：1..10。
const (
	CInvalidParam  = 1
	CClockRollback = 2
	CNotFound      = 3
	CInvalidState  = 4
	CFrozen        = 5
	CFull          = 6
	CBand          = 7
	CPending       = 8
	CCooldown      = 9
	CExpired       = 10
)

func okResult() Result                { return Result{OK: true, Code: 0, Index: -1} }
func singleErr(code int) Result       { return Result{OK: false, Code: code, Index: -1} }
func batchErr(code, index int) Result { return Result{OK: false, Code: code, Index: index} }

func (m *Model) clockOK(now int) bool { return !(m.clockSet && now < m.now) }

// Apply 执行一步操作。
func (m *Model) Apply(op Op) Result {
	switch op.Kind {
	case "AddPosition":
		return m.addPosition(op)
	case "AddCandidate":
		return m.addCandidate(op)
	case "AddException":
		return m.addException(op)
	case "SetFrozen":
		return m.setFrozen(op)
	case "AdjustHeadcount":
		return m.adjust(op)
	case "Issue":
		return m.issue(op)
	case "IssueBatch":
		return m.issueBatch(op)
	case "Respond":
		return m.respond(op)
	case "Onboard":
		return m.onboard(op)
	case "Withdraw":
		return m.withdraw(op)
	case "Cancel":
		return m.cancel(op)
	case "Leave":
		return m.leave(op)
	case "GetOffer", "Occupancy":
		return m.touch(op)
	case "Snapshot":
		return okResult()
	default:
		panic(fmt.Sprintf("unknown op kind %q", op.Kind))
	}
}

func (m *Model) addPosition(op Op) Result {
	if op.Pos == "" || op.Total < 0 || op.BandLow > op.BandHigh || op.Now < 0 {
		return singleErr(CInvalidParam)
	}
	if !m.clockOK(op.Now) {
		return singleErr(CClockRollback)
	}
	if _, exists := m.positions[op.Pos]; exists {
		return singleErr(CInvalidState)
	}
	m.positions[op.Pos] = &mPosition{
		id: op.Pos, bandLow: op.BandLow, bandHigh: op.BandHigh, headcount: op.Total,
	}
	m.now, m.clockSet = op.Now, true
	return okResult()
}

func (m *Model) addCandidate(op Op) Result {
	if op.Cand == "" || op.Now < 0 {
		return singleErr(CInvalidParam)
	}
	if !m.clockOK(op.Now) {
		return singleErr(CClockRollback)
	}
	if m.candidates[op.Cand] {
		return singleErr(CInvalidState)
	}
	m.candidates[op.Cand] = true
	m.now, m.clockSet = op.Now, true
	return okResult()
}

func (m *Model) addException(op Op) Result {
	if op.ExcID == "" || op.Pos == "" || op.Quarter < 0 || op.Total < 0 || op.Now < 0 {
		return singleErr(CInvalidParam)
	}
	if !m.clockOK(op.Now) {
		return singleErr(CClockRollback)
	}
	if _, ok := m.positions[op.Pos]; !ok {
		return singleErr(CNotFound)
	}
	if _, ok := m.exceptions[op.ExcID]; ok {
		return singleErr(CInvalidState)
	}
	for _, e := range m.exceptions {
		if e.position == op.Pos && e.quarter == op.Quarter {
			return singleErr(CInvalidState)
		}
	}
	m.exceptions[op.ExcID] = &mException{
		id: op.ExcID, position: op.Pos, quarter: op.Quarter,
		total: op.Total, remaining: op.Total,
	}
	m.now, m.clockSet = op.Now, true
	return okResult()
}

func (m *Model) setFrozen(op Op) Result {
	if op.Pos == "" || op.Now < 0 {
		return singleErr(CInvalidParam)
	}
	if !m.clockOK(op.Now) {
		return singleErr(CClockRollback)
	}
	p, ok := m.positions[op.Pos]
	if !ok {
		return singleErr(CNotFound)
	}
	p.frozen = op.Frozen
	m.now, m.clockSet = op.Now, true
	return okResult()
}
