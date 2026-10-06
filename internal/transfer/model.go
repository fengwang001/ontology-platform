package transfer

import "sync"

type Status int

const (
	StatusCreated Status = iota
	StatusShipped
	StatusClosed
	StatusCancelled
)

type Line struct {
	Item string
	Qty  int64
}

// LineState 是某行对外可见的状态快照。
type LineState struct {
	Item      string
	Requested int64 // 创建时申请数量
	Issued    int64 // 发出量（发出前为 0）
	Received  int64 // 累计收货量
	Shortage  int64 // 关闭时登记的在途短缺（事后找回会减少）
	Overage   int64 // 关闭时登记的超收盈余
}

type Order struct {
	mu        sync.Mutex
	ID        string
	Source    string
	Dest      string
	Status    Status
	ShipTime  int64
	lines     []LineState
	itemIndex map[string]int
}

func (s Status) String() string {
	switch s {
	case StatusCreated:
		return "created"
	case StatusShipped:
		return "shipped"
	case StatusClosed:
		return "closed"
	case StatusCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

func newOrder(id, source, dest string, lines []Line) *Order {
	o := &Order{
		ID:        id,
		Source:    source,
		Dest:      dest,
		Status:    StatusCreated,
		itemIndex: make(map[string]int, len(lines)),
	}
	for i, ln := range lines {
		o.lines = append(o.lines, LineState{Item: ln.Item, Requested: ln.Qty})
		o.itemIndex[ln.Item] = i
	}
	return o
}

func (o *Order) line(item string) (int, bool) {
	i, ok := o.itemIndex[item]
	return i, ok
}

// tolerance 返回行 i 的超收容忍额：floor(issued * permille / 1000)。
func (o *Order) tolerance(i int, permille int64) int64 {
	return o.lines[i].Issued * permille / 1000
}

// canReceive 判断再收 qty 是否超过 发出量+容忍额。
func (o *Order) canReceive(i, qty, permille int64) bool {
	idx := int(i)
	ln := &o.lines[idx]
	return ln.Received+qty <= ln.Issued+o.tolerance(idx, permille)
}

// canClose 判断在时刻 at 是否允许关闭。
func (o *Order) canClose(at, waitSeconds int64) (bool, string) {
	allFulfilled := true
	for i := range o.lines {
		if o.lines[i].Received < o.lines[i].Issued {
			allFulfilled = false
			break
		}
	}
	if allFulfilled {
		return true, "all lines fulfilled"
	}
	if at >= o.ShipTime+waitSeconds {
		return true, "wait duration elapsed"
	}
	return false, "close time not reached"
}

// registerDiff 关闭时逐行登记短缺与盈余。
func (o *Order) registerDiff() {
	for i := range o.lines {
		ln := &o.lines[i]
		switch {
		case ln.Received < ln.Issued:
			ln.Shortage = ln.Issued - ln.Received
		case ln.Received > ln.Issued:
			ln.Overage = ln.Received - ln.Issued
		}
	}
}

func (o *Order) snapshotLines() []LineState {
	out := make([]LineState, len(o.lines))
	copy(out, o.lines)
	return out
}
