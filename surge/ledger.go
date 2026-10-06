package surge

// rider 是骑手在供需账本中的状态。
type rider struct {
	id        string
	online    bool
	area      string // 在线时所在区域；离线时为空
	enteredAt int64  // 最近一次进入当前所在区域的时刻（含上线/移动）
	held      int    // 当前持单数（已派未完成的单数）
}

// available 报告骑手在其当前区域是否计入可用运力：
// 在线且持单未达上限。
func (r *rider) available(maxHeld int) bool {
	return r.online && r.held < maxHeld
}

// orderState 为订单生命周期状态。
type orderState int

const (
	orderPending    orderState = iota // 已创建，待派
	orderDispatched                   // 已派出，进行中
	orderCompleted
	orderCancelled
)

// order 是订单及其锁定结算信息。
type order struct {
	id         string
	area       string
	createdAt  int64
	state      orderState
	lockedTier int // 创建时锁定的档位
	riderID    string
	// eligibleAtDispatch 在派单时刻快照“创建时是否已在本区在线”，
	// 使完成结算为纯 O(1) 查表，不依赖骑手后续移动。
	eligibleAtDispatch bool
	settled            bool
}

// area 是区域供需账本，计数均为增量维护。
type area struct {
	id           string
	available    int // 在线且持单未满的骑手数
	pending      int // 待派订单数
	currentTier  int
	downConfirms int
	lastEval     int64
	hasEval      bool
}

// ledger 持有全部区域、骑手、订单与补贴账目。
type ledger struct {
	areas   map[string]*area
	riders  map[string]*rider
	orders  map[string]*order
	entries map[string][]*SubsidyEntry // riderID -> 账目条目
}

func newLedger() *ledger {
	return &ledger{
		areas:   map[string]*area{},
		riders:  map[string]*rider{},
		orders:  map[string]*order{},
		entries: map[string][]*SubsidyEntry{},
	}
}

func (l *ledger) addArea(id string) { l.areas[id] = &area{id: id} }

func (l *ledger) addRider(id string) { l.riders[id] = &rider{id: id} }
