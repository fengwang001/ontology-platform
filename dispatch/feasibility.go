package dispatch

// Insertion 描述一次可行插入及其评价量。
type Insertion struct {
	PickupIndex  int // 插入位置：取货停靠在新序列中的下标
	DropoffIndex int // 插入位置：送达停靠在新序列中的下标
	NewArrival   int64
	OldExtra     int64 // 在途订单送达总延后量
	ExtraTime    int64 // 序列总耗时增量
	Sched        Schedule
}

// NoRiderReason 为无可行骑手的三种可程序化区分原因。
type NoRiderReason uint8

const (
	ReasonRegionCapacity NoRiderReason = iota + 1
	ReasonNewOrderPromise
	ReasonExistingViolation
)

func (r NoRiderReason) Error() error {
	switch r {
	case ReasonRegionCapacity:
		return ErrNoRiderRegionCapacity
	case ReasonNewOrderPromise:
		return ErrNewOrderPromise
	case ReasonExistingViolation:
		return ErrExistingViolation
	default:
		return nil
	}
}

// Candidate 是为单个骑手枚举插入位置的结果。
type Candidate struct {
	RiderID RiderID
	Held    int
	Best    Insertion
	HasBest bool
	// NewPromiseOK 标记是否存在“满足新订单承诺且取先于送、边可达”的位置。
	NewPromiseOK bool
}

// BestInsertion 在不掌握在途订单承诺时刻时的便捷入口（测试用，旧单承诺视为无限大）。
func BestInsertion(tt TravelTimeSource, r *Rider, order Order, pickupDwell, dropDwell, maxDetour int64, oldSched Schedule) (Insertion, bool) {
	return BestInsertionWithPromises(tt, r, order, pickupDwell, dropDwell, maxDetour, oldSched, nil)
}

// BestInsertionWithPromises 枚举骑手现有序列上的全部插入位置，返回最优可行插入。
// 同一骑手内偏好：在途总延后最小 -> 新单送达最早 -> 取货更靠前 -> 送达更靠前。
// 复杂度只与 len(r.Pending) 相关（O(n^2) 个位置，每个位置 O(n) 推定）。
func BestInsertionWithPromises(tt TravelTimeSource, r *Rider, order Order, pickupDwell, dropDwell, maxDetour int64, oldSched Schedule, promiseOf func(OrderID) int64) (Insertion, bool) {
	ins, best, _ := enumerate(tt, r, order, pickupDwell, dropDwell, maxDetour, oldSched, promiseOf)
	return ins, best
}

// EvaluateRider 枚举单骑手位置并产出完整分类信号。
func EvaluateRider(tt TravelTimeSource, r *Rider, order Order, pickupDwell, dropDwell, maxDetour int64, promiseOf func(OrderID) int64) Candidate {
	c := Candidate{RiderID: r.ID, Held: countHeld(r.Pending)}
	ins, has, newPromiseOK := enumerate(tt, r, order, pickupDwell, dropDwell, maxDetour, nil, promiseOf)
	c.Best = ins
	c.HasBest = has
	c.NewPromiseOK = newPromiseOK
	return c
}

// enumerate 枚举全部 (pi, di)，pi 为取货插入位置 0..n，di 为送达插入位置 pi+1..n+1。
// 返回最优可行插入、是否存在可行位置、是否存在仅满足新订单承诺的位置。
func enumerate(tt TravelTimeSource, r *Rider, order Order, pickupDwell, dropDwell, maxDetour int64, oldSched Schedule, promiseOf func(OrderID) int64) (Insertion, bool, bool) {
	pickup := Stop{OrderID: order.ID, Kind: StopPickup, At: order.Pickup, ReadyAt: order.ReadyAt, Dwell: pickupDwell}
	dropoff := Stop{OrderID: order.ID, Kind: StopDropoff, At: order.Dropoff, Dwell: dropDwell}
	n := len(r.Pending)

	if oldSched == nil {
		os, ok := EstimateSchedule(tt, r.Pos, r.DepartedAt, r.Pending)
		if !ok {
			return Insertion{}, false, false
		}
		oldSched = os
	}
	oldDur := scheduleDuration(r.DepartedAt, oldSched)

	// 旧序列中每个在途订单送达下标的原推定到达。
	type oldDrop struct {
		idx    int
		arrive int64
	}
	oldDrops := make(map[OrderID]oldDrop, n)
	for i := range r.Pending {
		if r.Pending[i].Kind == StopDropoff {
			oldDrops[r.Pending[i].OrderID] = oldDrop{i, oldSched[i].Arrive}
		}
	}

	var best Insertion
	has := false
	newPromiseOK := false

	for pi := 0; pi <= n; pi++ {
		for di := pi + 1; di <= n+1; di++ {
			cand := make([]Stop, 0, n+2)
			cand = append(cand, r.Pending[:pi]...)
			cand = append(cand, pickup)
			cand = append(cand, r.Pending[pi:di-1]...)
			cand = append(cand, dropoff)
			cand = append(cand, r.Pending[di-1:]...)

			sched, ok := EstimateSchedule(tt, r.Pos, r.DepartedAt, cand)
			if !ok {
				continue
			}
			newDropArrival := sched[di].Arrive
			if newDropArrival <= order.Promise {
				newPromiseOK = true
			} else {
				continue
			}

			feasible := true
			var oldExtra int64
			for oid, od := range oldDrops {
				newIdx := mappedIndex(od.idx, pi, di)
				newArrival := sched[newIdx].Arrive
				if promiseOf != nil && newArrival > promiseOf(oid) {
					feasible = false
					break
				}
				delay := newArrival - od.arrive
				if delay > maxDetour {
					feasible = false
					break
				}
				if delay > 0 {
					oldExtra += delay
				}
			}
			if !feasible {
				continue
			}

			cur := Insertion{
				PickupIndex:  pi,
				DropoffIndex: di,
				NewArrival:   newDropArrival,
				OldExtra:     oldExtra,
				ExtraTime:    scheduleDuration(r.DepartedAt, sched) - oldDur,
				Sched:        sched,
			}
			if !has || betterInsertion(cur, best) {
				best = cur
				has = true
			}
		}
	}
	return best, has, newPromiseOK
}

// mappedIndex 给出旧下标 oldIdx 在“pi 处插取货、di 处插送达”后的新下标。
func mappedIndex(oldIdx, pi, di int) int {
	switch {
	case oldIdx >= di-1:
		return oldIdx + 2
	case oldIdx >= pi:
		return oldIdx + 1
	default:
		return oldIdx
	}
}

// betterInsertion 实现同一骑手内的确定性位置偏好。
func betterInsertion(a, b Insertion) bool {
	if a.OldExtra != b.OldExtra {
		return a.OldExtra < b.OldExtra
	}
	if a.NewArrival != b.NewArrival {
		return a.NewArrival < b.NewArrival
	}
	if a.PickupIndex != b.PickupIndex {
		return a.PickupIndex < b.PickupIndex
	}
	return a.DropoffIndex < b.DropoffIndex
}
