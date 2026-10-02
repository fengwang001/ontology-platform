// Package booking 实现带历史缺席率自适应超卖与到场挤出补偿的容量预订簿。
package booking

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 操作拒绝原因。Book 与 Settle 按固定顺序只报告第一个命中的原因。
var (
	ErrInvalidConfig   = errors.New("booking: 配置非法")
	ErrInvalidArgument = errors.New("booking: 参数非法")
	ErrDuplicateID     = errors.New("booking: id 重复")
	ErrSlotSettled     = errors.New("booking: 时段已结算")
	ErrOverLimit       = errors.New("booking: 超出时段预订上限")
	ErrSlotRollback    = errors.New("booking: 时段回退")
)

const (
	maxCoord    = 1_000_000
	maxOversell = 10_000
	maxWindow   = 64
)

// Arrival 描述一次 Settle 中某个预订的到场量。
type Arrival struct {
	ID int64
	A  int64
}

// Eviction 描述一次 Settle 中某个预订的实到服务量与被挤出量。
type Eviction struct {
	ID      int64
	Arrived int64
	Served  int64
	Evicted int64
	Comp    int64
}

// SettleResult 为 Settle 的返回结果。
type SettleResult struct {
	Slot      int64
	Booked    int64
	Arrived   int64
	Evictions []Eviction
	TotalComp int64
}

type reservation struct {
	id      int64
	owner   int64
	slot    int64
	size    int64
	tier    int64
	seq     int64
	arrived int64
	evicted int64
	comp    int64
}

type windowEntry struct {
	booked  int64
	arrived int64
}

// Book 为容量预订簿，所有方法可并发调用，效果等价于某个串行顺序。
type Book struct {
	mu sync.Mutex

	capC    int64
	windowW int
	omax    int64
	compR   int64

	lastSettled int64
	window      []windowEntry
	sumBooked   int64
	sumArrived  int64

	seq      int64
	kByOwner map[int64]int64

	byID     map[int64]*reservation
	bySlot   map[int64][]*reservation
	bookedBy map[int64]int64
}

// New 构造预订簿。任一参数越界时整体拒绝并返回 ErrInvalidConfig。
func New(capC, windowW, omax, compR int64) (*Book, error) {
	if capC < 1 || capC > maxCoord ||
		windowW < 1 || windowW > maxWindow ||
		omax < 0 || omax > maxOversell ||
		compR < 0 || compR > maxCoord {
		return nil, fmt.Errorf("%w: C=%d W=%d Omax=%d R=%d", ErrInvalidConfig, capC, windowW, omax, compR)
	}
	return &Book{
		capC:        capC,
		windowW:     int(windowW),
		omax:        omax,
		compR:       compR,
		lastSettled: -1,
		kByOwner:    make(map[int64]int64),
		byID:        make(map[int64]*reservation),
		bySlot:      make(map[int64][]*reservation),
		bookedBy:    make(map[int64]int64),
	}, nil
}

// currentO 计算调用时刻的超卖系数（万分比）。
// 调用方须持有锁。
func (b *Book) currentO() int64 {
	if b.sumBooked == 0 {
		return 0
	}
	o := (b.sumBooked - b.sumArrived) * 10000 / b.sumBooked
	if o > b.omax {
		o = b.omax
	}
	return o
}

// limit 返回时段在当前超卖系数下的预订上限。
// 调用方须持有锁。
func (b *Book) limit() int64 {
	return b.capC * (10000 + b.currentO()) / 10000
}

func validCoord(v int64) bool { return v >= 0 && v <= maxCoord }

// Book 登记预订。拒绝原因按 参数非法 → id 重复 → 时段已结算 → 超出上限
// 的顺序只报告第一个；被拒绝时不改变任何状态也不消耗序号。
func (b *Book) Book(id, owner, slot, size, tier int64) error {
	if !validCoord(id) || !validCoord(owner) || !validCoord(slot) ||
		size < 1 || size > maxCoord || tier < 0 || tier > 2 {
		return fmt.Errorf("%w: id=%d owner=%d slot=%d size=%d tier=%d",
			ErrInvalidArgument, id, owner, slot, size, tier)
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.byID[id]; ok {
		return fmt.Errorf("%w: id=%d", ErrDuplicateID, id)
	}
	if slot <= b.lastSettled {
		return fmt.Errorf("%w: slot=%d lastSettled=%d", ErrSlotSettled, slot, b.lastSettled)
	}
	if b.bookedBy[slot]+size > b.limit() {
		return fmt.Errorf("%w: slot=%d booked=%d size=%d limit=%d",
			ErrOverLimit, slot, b.bookedBy[slot], size, b.limit())
	}
	r := &reservation{id: id, owner: owner, slot: slot, size: size, tier: tier, seq: b.seq}
	b.seq++
	b.byID[id] = r
	b.bySlot[slot] = append(b.bySlot[slot], r)
	b.bookedBy[slot] += size
	return nil
}

// Settle 结算时段 slot。拒绝原因按 参数非法 → 时段回退 的顺序只报告第一个；
// 被拒绝时不改变任何状态。
func (b *Book) Settle(slot int64, arrivals []Arrival) (*SettleResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !validCoord(slot) {
		return nil, fmt.Errorf("%w: slot=%d", ErrInvalidArgument, slot)
	}
	seen := make(map[int64]bool, len(arrivals))
	amounts := make(map[int64]int64, len(arrivals))
	for _, ar := range arrivals {
		r, ok := b.byID[ar.ID]
		if !ok || r.slot != slot {
			return nil, fmt.Errorf("%w: id=%d 不属于时段 %d", ErrInvalidArgument, ar.ID, slot)
		}
		if seen[ar.ID] {
			return nil, fmt.Errorf("%w: id=%d 重复列出", ErrInvalidArgument, ar.ID)
		}
		if ar.A < 0 || ar.A > r.size {
			return nil, fmt.Errorf("%w: id=%d 到场量 %d 越界 [0,%d]", ErrInvalidArgument, ar.ID, ar.A, r.size)
		}
		seen[ar.ID] = true
		amounts[ar.ID] = ar.A
	}
	if slot <= b.lastSettled {
		return nil, fmt.Errorf("%w: slot=%d lastSettled=%d", ErrSlotRollback, slot, b.lastSettled)
	}

	res := &SettleResult{Slot: slot, Booked: b.bookedBy[slot]}
	for _, r := range b.bySlot[slot] {
		r.arrived = amounts[r.id]
		res.Arrived += r.arrived
	}

	// 一：把 (R0, A) 追加进窗口，超过 W 条丢弃最旧。
	b.window = append(b.window, windowEntry{booked: res.Booked, arrived: res.Arrived})
	b.sumBooked += res.Booked
	b.sumArrived += res.Arrived
	if len(b.window) > b.windowW {
		old := b.window[0]
		b.window = b.window[1:]
		b.sumBooked -= old.booked
		b.sumArrived -= old.arrived
	}

	// 二：到场超过容量时按 tier 降序、预订序号降序依次挤出。
	if excess := res.Arrived - b.capC; excess > 0 {
		cands := make([]*reservation, 0, len(b.bySlot[slot]))
		for _, r := range b.bySlot[slot] {
			if r.arrived > 0 {
				cands = append(cands, r)
			}
		}
		sort.Slice(cands, func(i, j int) bool {
			if cands[i].tier != cands[j].tier {
				return cands[i].tier > cands[j].tier
			}
			return cands[i].seq > cands[j].seq
		})
		evictedOwners := make(map[int64]bool)
		for _, r := range cands {
			if excess == 0 {
				break
			}
			t := r.arrived
			if t > excess {
				t = excess
			}
			r.evicted = t
			excess -= t
			k := b.kByOwner[r.owner]
			if k > 3 {
				k = 3
			}
			r.comp = t * b.compR * (1 + k)
			res.TotalComp += r.comp
			evictedOwners[r.owner] = true
		}
		// 三：本次 Settle 中被挤出至少一个单位的每个租户 k 加一（至多加一）。
		for owner := range evictedOwners {
			b.kByOwner[owner]++
		}
	}

	// 四：lastSettled=slot。
	b.lastSettled = slot

	for _, r := range b.bySlot[slot] {
		res.Evictions = append(res.Evictions, Eviction{
			ID:      r.id,
			Arrived: r.arrived,
			Served:  r.arrived - r.evicted,
			Evicted: r.evicted,
			Comp:    r.comp,
		})
	}
	return res, nil
}

// CurrentO 返回当前超卖系数（万分比）。
func (b *Book) CurrentO() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.currentO()
}

// Limit 返回按当前超卖系数计算的时段预订上限。
func (b *Book) Limit() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.limit()
}

// Booked 返回时段 slot 已成功预订的总量。
func (b *Book) Booked(slot int64) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.bookedBy[slot]
}

// K 返回租户 owner 累计被挤次数。
func (b *Book) K(owner int64) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.kByOwner[owner]
}

// LastSettled 返回已结算的最大时段号。
func (b *Book) LastSettled() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastSettled
}

// WindowLen 返回当前窗口内的记录条数。
func (b *Book) WindowLen() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.window)
}

// Seq 返回全局预订序号（已成功 Book 的次数）。
func (b *Book) Seq() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq
}
