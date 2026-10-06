package fulfillment

// Stage 订单生命周期阶段，事件必须按此次序各发生一次。
type Stage int

const (
	StageAccepted Stage = iota
	StageDispatched
	StageMealReady
	StagePickedUp
	StageDelivered
)

func (s Stage) String() string {
	switch s {
	case StageAccepted:
		return "accepted"
	case StageDispatched:
		return "dispatched"
	case StageMealReady:
		return "meal-ready"
	case StagePickedUp:
		return "picked-up"
	case StageDelivered:
		return "delivered"
	}
	return "unknown"
}

// order 订单状态。params 为接受时刻冻结的参数快照。
type order struct {
	id           string
	params       Params
	stage        Stage
	acceptTs     int64
	dispatchTs   int64
	mealReadyTs  int64
	pickupTs     int64
	deliverTs    int64
	cancelled    bool
	addrChanged  bool
	addrChangeTs int64
	origPromise  int64 // 原始承诺时刻 = 接受时刻 + 承诺时长，不含任何延展
	extension    int64 // 已生效延展之和，已按上限截断
	settled      bool
	verdict      Verdict
}

// promise 延展后的承诺送达时刻。
func (o *order) promise() int64 { return o.origPromise + o.extension }

// extend 累加延展并按累计上限截断。delta 非负时增量式截断与求和后截断等价。
func (o *order) extend(delta int64) {
	o.extension += delta
	if o.extension > o.params.ExtensionCap {
		o.extension = o.params.ExtensionCap
	}
}

// delay 延误时长 = 送达时刻 - 延展后承诺时刻，不为正则无延误。
func (o *order) delay() int64 { return o.deliverTs - o.promise() }
