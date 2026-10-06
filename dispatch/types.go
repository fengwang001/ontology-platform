// Package dispatch 实现即时配送平台的骑手顺路合并派单系统。
package dispatch

import "errors"

// Point 是地理位置标识，行程耗时完全由外部 TravelTimeSource 决定。
type Point string

// TravelTimeSource 提供点到点耗时（秒），不保证满足三角不等式。
type TravelTimeSource interface {
	TravelTime(from, to Point) int64
}

// TravelTimeFunc 把函数适配为 TravelTimeSource。
type TravelTimeFunc func(from, to Point) int64

func (f TravelTimeFunc) TravelTime(from, to Point) int64 { return f(from, to) }

// StopKind 区分取货与送达停靠。
type StopKind int

const (
	StopPickup StopKind = iota
	StopDeliver
)

// Order 是一笔配送订单。
type Order struct {
	ID          string
	Region      string // 取货点所在商家区域
	Pickup      Point
	Drop        Point
	ReadyAt     int64 // 商家预计出餐就绪时刻
	PromiseAt   int64 // 承诺送达时刻
	PickupDwell int64 // 取货停靠停留时长（秒）
	DropDwell   int64 // 送达停靠停留时长（秒）
}

// Rider 是一名骑手及其当前状态快照。
type Rider struct {
	ID       string
	Region   string // 当前服务区域
	Pos      Point  // 当前位置
	DepartAt int64  // 当前出发时刻
	Capacity int    // 载量上限（已取货未送达 + 已接未取货）
}

// Config 是系统级构造参数。
type Config struct {
	MaxDetour int64 // 单次绕路上限：一笔在途订单因一次插入允许的最大送达延后（秒）
}

// Assignment 是一次成功派单的结果。
type Assignment struct {
	RiderID    string
	PickupPos  int   // 取货停靠在插入后序列中的下标
	DeliverPos int   // 送达停靠在插入后序列中的下标
	DropEta    int64 // 新订单送达推定到达时刻
	Increment  int64 // 插入后序列总耗时增量
}

// StopView 是停靠序列的只读视图，带推定时刻。
type StopView struct {
	OrderID string
	Kind    StopKind
	Eta     int64 // 推定到达时刻
	Leave   int64 // 推定离开时刻
}

// 拒绝次序：参数非法 -> 时钟回退 -> 对象不存在或状态不符 -> 无可行骑手。
var (
	ErrInvalidParam          = errors.New("dispatch: invalid parameter")
	ErrClockRegression       = errors.New("dispatch: clock regression")
	ErrRiderNotFound         = errors.New("dispatch: rider not found")
	ErrRiderExists           = errors.New("dispatch: rider already exists")
	ErrRiderOffline          = errors.New("dispatch: rider offline")
	ErrOrderNotFound         = errors.New("dispatch: order not found")
	ErrOrderExists           = errors.New("dispatch: order already exists")
	ErrOrderAlreadyAssigned  = errors.New("dispatch: order already assigned")
	ErrOrderNotAssignable    = errors.New("dispatch: order not assignable")
	ErrOrderAlreadyPickedUp  = errors.New("dispatch: order already picked up")
	ErrOrderAlreadyCancelled = errors.New("dispatch: order already cancelled")
	ErrStopOutOfOrder        = errors.New("dispatch: stop completion out of order")
	// 无可行骑手的三种细分原因，按此次序只报一种。
	ErrNoRiderCapacity   = errors.New("dispatch: all in-region riders at capacity")
	ErrNoPromisePosition = errors.New("dispatch: no insertion meets the new order promise")
	ErrDetourLimit       = errors.New("dispatch: every feasible-promise insertion breaks an in-transit promise or detour limit")
)
