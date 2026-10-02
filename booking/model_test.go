package booking

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// 朴素模拟：严格按规则逐步写成的独立参照实现，用于对照被测实现。

type mRes struct {
	id, owner, slot, size, tier, seq int64
}

type model struct {
	C, W, Omax, R int64
	lastSettled   int64
	window        [][2]int64
	seq           int64
	k             map[int64]int64
	res           map[int64]*mRes
	slotRes       map[int64][]*mRes
	slotBooked    map[int64]int64
}

func newModel(C, W, Omax, R int64) *model {
	return &model{
		C: C, W: W, Omax: Omax, R: R,
		lastSettled: -1,
		k:           make(map[int64]int64),
		res:         make(map[int64]*mRes),
		slotRes:     make(map[int64][]*mRes),
		slotBooked:  make(map[int64]int64),
	}
}

func (m *model) oversell() int64 {
	var sr, sa int64
	for _, e := range m.window {
		sr += e[0]
		sa += e[1]
	}
	if sr == 0 {
		return 0
	}
	o := (sr - sa) * 10000 / sr
	if o > m.Omax {
		o = m.Omax
	}
	return o
}

func (m *model) limit() int64 { return m.C * (10000 + m.oversell()) / 10000 }

// book 返回拒绝原因代码（"" 表示成功）。
func (m *model) book(id, owner, slot, size, tier int64) string {
	if id < 0 || id > 1_000_000 || owner < 0 || owner > 1_000_000 ||
		slot < 0 || slot > 1_000_000 || size < 1 || size > 1_000_000 ||
		tier < 0 || tier > 2 {
		return "arg"
	}
	if _, ok := m.res[id]; ok {
		return "dup"
	}
	if slot <= m.lastSettled {
		return "settled"
	}
	if m.slotBooked[slot]+size > m.limit() {
		return "limit"
	}
	r := &mRes{id, owner, slot, size, tier, m.seq}
	m.res[id] = r
	m.slotRes[slot] = append(m.slotRes[slot], r)
	m.slotBooked[slot] += size
	m.seq++
	return ""
}

// settle 返回拒绝原因代码与结果。
func (m *model) settle(slot int64, arrivals []Arrival) (string, *SettleResult) {
	if slot < 0 || slot > 1_000_000 {
		return "arg", nil
	}
	seen := make(map[int64]bool)
	amt := make(map[int64]int64)
	for _, a := range arrivals {
		r, ok := m.res[a.ID]
		if !ok || r.slot != slot {
			return "arg", nil
		}
		if seen[a.ID] {
			return "arg", nil
		}
		if a.A < 0 || a.A > r.size {
			return "arg", nil
		}
		seen[a.ID] = true
		amt[a.ID] = a.A
	}
	if slot <= m.lastSettled {
		return "rollback", nil
	}
	res := &SettleResult{Slot: slot, Booked: m.slotBooked[slot]}
	for _, r := range m.slotRes[slot] {
		res.Arrived += amt[r.id]
	}
	m.window = append(m.window, [2]int64{res.Booked, res.Arrived})
	if len(m.window) > int(m.W) {
		m.window = m.window[1:]
	}
	excess := res.Arrived - m.C
	evictedOf := make(map[int64]int64)
	compOf := make(map[int64]int64)
	if excess > 0 {
		var cands []*mRes
		for _, r := range m.slotRes[slot] {
			if amt[r.id] > 0 {
				cands = append(cands, r)
			}
		}
		sort.Slice(cands, func(i, j int) bool {
			if cands[i].tier != cands[j].tier {
				return cands[i].tier > cands[j].tier
			}
			return cands[i].seq > cands[j].seq
		})
		bumped := make(map[int64]bool)
		for _, r := range cands {
			if excess == 0 {
				break
			}
			t := amt[r.id]
			if t > excess {
				t = excess
			}
			evictedOf[r.id] = t
			excess -= t
			k := m.k[r.owner]
			if k > 3 {
				k = 3
			}
			compOf[r.id] = t * m.R * (1 + k)
			res.TotalComp += compOf[r.id]
			bumped[r.owner] = true
		}
		for o := range bumped {
			m.k[o]++
		}
	}
	m.lastSettled = slot
	for _, r := range m.slotRes[slot] {
		a := amt[r.id]
		res.Evictions = append(res.Evictions, Eviction{
			ID: r.id, Arrived: a, Served: a - evictedOf[r.id],
			Evicted: evictedOf[r.id], Comp: compOf[r.id],
		})
	}
	return "", res
}

func bookErrCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrInvalidArgument):
		return "arg"
	case errors.Is(err, ErrDuplicateID):
		return "dup"
	case errors.Is(err, ErrSlotSettled):
		return "settled"
	case errors.Is(err, ErrOverLimit):
		return "limit"
	}
	return "unknown"
}

func settleErrCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrInvalidArgument):
		return "arg"
	case errors.Is(err, ErrSlotRollback):
		return "rollback"
	}
	return "unknown"
}

// TestRandomAgainstModel 用 2000 组随机操作序列对照朴素模拟，
// 日志打印每步输入、双方输出与判定依据。
func TestRandomAgainstModel(t *testing.T) {
	const sequences = 2000
	for i := 0; i < sequences; i++ {
		rng := rand.New(rand.NewSource(int64(i)))
		C := 1 + rng.Int63n(15)
		W := 1 + rng.Int63n(6)
		Omax := rng.Int63n(10001)
		R := rng.Int63n(101)
		b, err := New(C, W, Omax, R)
		if err != nil {
			t.Fatalf("序列 %d: New 被拒: %v", i, err)
		}
		m := newModel(C, W, Omax, R)
		prefix := fmt.Sprintf("序列 %d (C=%d W=%d Omax=%d R=%d)", i, C, W, Omax, R)
		t.Logf("%s 开始", prefix)

		checkQueries := func(step int64) {
			t.Helper()
			if b.CurrentO() != m.oversell() || b.Limit() != m.limit() ||
				b.LastSettled() != m.lastSettled || b.Seq() != m.seq ||
				b.WindowLen() != len(m.window) {
				t.Fatalf("%s 步骤 %d: 查询不一致 O=%d/%d Limit=%d/%d last=%d/%d seq=%d/%d win=%d/%d",
					prefix, step, b.CurrentO(), m.oversell(), b.Limit(), m.limit(),
					b.LastSettled(), m.lastSettled, b.Seq(), m.seq, b.WindowLen(), len(m.window))
			}
			t.Logf("%s 步骤 %d 判定: 查询一致 O=%d Limit=%d lastSettled=%d seq=%d win=%d",
				prefix, step, b.CurrentO(), b.Limit(), b.LastSettled(), b.Seq(), b.WindowLen())
		}

		var nextID int64
		var ids []int64
		ops := 30 + rng.Int63n(30)
		for step := int64(0); step < ops; step++ {
			if rng.Int63n(100) < 60 {
				// Book
				var id int64
				switch {
				case len(ids) > 0 && rng.Int63n(100) < 15:
					id = ids[rng.Int63n(int64(len(ids)))] // 重复 id
				case rng.Int63n(100) < 4:
					id = -1 - rng.Int63n(3) // 非法 id
				default:
					id = nextID
					nextID++
				}
				owner := rng.Int63n(6)
				slot := m.lastSettled + rng.Int63n(5) - 1
				if rng.Int63n(100) < 3 {
					slot = 1_000_001 // 非法 slot
				}
				size := 1 + rng.Int63n(10)
				if rng.Int63n(100) < 3 {
					size = 0 // 非法 size
				}
				tier := rng.Int63n(3)
				if rng.Int63n(100) < 3 {
					tier = 3 // 非法 tier
				}
				got := bookErrCode(b.Book(id, owner, slot, size, tier))
				want := m.book(id, owner, slot, size, tier)
				t.Logf("%s 步骤 %d 输入 Book(id=%d owner=%d slot=%d size=%d tier=%d) 输出 got=%q want=%q",
					prefix, step, id, owner, slot, size, tier, got, want)
				if got != want {
					t.Fatalf("%s 步骤 %d: Book 判定不一致 got=%q want=%q", prefix, step, got, want)
				}
				if got == "" {
					ids = append(ids, id)
				}
				t.Logf("%s 步骤 %d 判定: Book 结果一致 (%q)", prefix, step, got)
			} else {
				// Settle
				slot := m.lastSettled + 1 + rng.Int63n(4)
				switch {
				case rng.Int63n(100) < 8:
					slot = m.lastSettled // 回退
				case rng.Int63n(100) < 3:
					slot = -5 // 非法
				}
				var arrivals []Arrival
				for _, r := range m.slotRes[slot] {
					if rng.Int63n(100) < 60 {
						a := rng.Int63n(r.size + 1)
						if rng.Int63n(100) < 5 {
							a = r.size + 1 // 到场量越界
						}
						arrivals = append(arrivals, Arrival{r.id, a})
						if rng.Int63n(100) < 4 {
							arrivals = append(arrivals, Arrival{r.id, 0}) // id 重复
						}
					}
				}
				if rng.Int63n(100) < 4 {
					arrivals = append(arrivals, Arrival{999_999, 1}) // 未知 id
				}
				gotRes, gotErr := b.Settle(slot, arrivals)
				wantCode, wantRes := m.settle(slot, arrivals)
				gotCode := settleErrCode(gotErr)
				t.Logf("%s 步骤 %d 输入 Settle(slot=%d arrivals=%v) 输出 got=(%q,%v) want=(%q,%v)",
					prefix, step, slot, arrivals, gotCode, gotRes, wantCode, wantRes)
				if gotCode != wantCode {
					t.Fatalf("%s 步骤 %d: Settle 判定不一致 got=%q want=%q", prefix, step, gotCode, wantCode)
				}
				if gotCode == "" && !equalSettleResult(gotRes, wantRes) {
					t.Fatalf("%s 步骤 %d: Settle 结果不一致 got=%+v want=%+v", prefix, step, gotRes, wantRes)
				}
				t.Logf("%s 步骤 %d 判定: Settle 结果一致 (%q)", prefix, step, gotCode)
			}
			checkQueries(step)
		}
		for owner, wantK := range m.k {
			if got := b.K(owner); got != wantK {
				t.Fatalf("%s: K(%d)=%d, 期望 %d", prefix, owner, got, wantK)
			}
		}
		t.Logf("%s 结束: 全部判定一致", prefix)
	}
}

func equalSettleResult(a, b *SettleResult) bool {
	if a.Slot != b.Slot || a.Booked != b.Booked || a.Arrived != b.Arrived ||
		a.TotalComp != b.TotalComp || len(a.Evictions) != len(b.Evictions) {
		return false
	}
	for i := range a.Evictions {
		if a.Evictions[i] != b.Evictions[i] {
			return false
		}
	}
	return true
}
