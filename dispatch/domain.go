package dispatch

// Location 是地理位置/商家区域的不透明标识。点到点耗时完全由外部源给出，
// 本系统不对其结构或三角不等式做任何假设。
type Location = string

// OrderID / RiderID 为业务标识。
type OrderID = string
type RiderID = string

// Order 描述一笔订单：取货点、送达点、商家出餐就绪时刻与承诺送达时刻。
type Order struct {
	ID      OrderID
	Pickup  Location
	Dropoff Location
	ReadyAt int64 // 商家预计出餐就绪（秒，全局单调时钟语义）
	Promise int64 // 承诺送达时刻
}

// StopKind 区分取货/送达停靠。
type StopKind uint8

const (
	StopPickup StopKind = iota + 1
	StopDropoff
)

// Stop 是骑手未完成序列中的一个停靠。
type Stop struct {
	OrderID OrderID
	Kind    StopKind
	At      Location
	// ReadyAt 仅取货停靠使用：到达早于该时刻必须等待。
	ReadyAt int64
	// Dwell 为构造参数规定的固定停留时长，计入离开时刻。
	Dwell int64
}

// Rider 描述骑手当前快照。
type Rider struct {
	ID         RiderID
	Online     bool
	Region     Location // 当前服务区域（以取货点所属商家区域匹配）
	Capacity   int
	Pos        Location // 当前位置
	DepartedAt int64    // 当前出发时刻（首个停靠的推定起点）
	Pending    []Stop   // 尚未完成的停靠序列
}

// OrderStatus 为订单生命周期状态。
type OrderStatus uint8

const (
	OrderNew OrderStatus = iota
	OrderAssigned
	OrderPicked
	OrderDelivered
	OrderCancelled
)

func (s OrderStatus) String() string {
	switch s {
	case OrderNew:
		return "new"
	case OrderAssigned:
		return "assigned"
	case OrderPicked:
		return "picked"
	case OrderDelivered:
		return "delivered"
	case OrderCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// TravelTimeSource 为外部注入的点到点耗时源：只可查询。
// ok=false 表示两点间不存在可用耗时，任何经过该边的插入均不可行。
type TravelTimeSource interface {
	Travel(from, to Location) (seconds int64, ok bool)
}

// TravelTimeFunc 便于用函数实现耗时源。
type TravelTimeFunc func(from, to Location) (int64, bool)

func (f TravelTimeFunc) Travel(from, to Location) (int64, bool) { return f(from, to) }

// TravelMap 是最常见的显式矩阵型耗时源。
type TravelMap map[Location]map[Location]int64

func (m TravelMap) Travel(from, to Location) (int64, bool) {
	if row, ok := m[from]; ok {
		if d, ok := row[to]; ok {
			return d, true
		}
	}
	return 0, false
}
