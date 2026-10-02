package seating

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

// naiveRegistrar 是题目规格的朴素直译实现，用于对照：
// 每次 Hold 都枚举全部排与全部连续段，按规则两遍选座。
type naiveRegistrar struct {
	rows, width int
	ttl         int64
	holds       []naiveHold
	maxNow      int64
	seen        bool
}

type naiveHold struct {
	row, start, size int
	expiry           int64
	status           holdStatus
}

func newNaive(rows, width int, ttl int64) *naiveRegistrar {
	return &naiveRegistrar{rows: rows, width: width, ttl: ttl}
}

// seatFree 逐个座位判定：无已确认订单、无活跃保留占用。
func (n *naiveRegistrar) seatFree(row, seat int, now int64) bool {
	for _, h := range n.holds {
		if h.row != row || seat < h.start || seat >= h.start+h.size {
			continue
		}
		if h.status == holdConfirmed || (h.status == holdActive && now < h.expiry) {
			return false
		}
	}
	return true
}

// runLen 从 seat 起沿 dir 方向数连续空闲座个数，即紧邻的最大空闲连续区长度。
func (n *naiveRegistrar) runLen(row, seat, dir int, now int64) int {
	length := 0
	for s := seat; s >= 1 && s <= n.width && n.seatFree(row, s, now); s += dir {
		length++
	}
	return length
}

type naiveCandidate struct {
	row, start, dev int
	orphan          bool
}

func (n *naiveRegistrar) checkClock(now int64) error {
	if n.seen && now < n.maxNow {
		return ErrClockRollback
	}
	return nil
}

// hold 返回选座结果与判定依据（用于日志）。
func (n *naiveRegistrar) hold(k int, now int64) (HoldResult, string, error) {
	if err := n.checkClock(now); err != nil {
		return HoldResult{}, "时钟回退", err
	}
	if k < 1 || k > 8 {
		return HoldResult{}, "人数非法", ErrInvalidGroupSize
	}

	var all []naiveCandidate
	for row := 1; row <= n.rows; row++ {
		for s := 1; s+k-1 <= n.width; s++ {
			free := true
			for seat := s; seat < s+k; seat++ {
				if !n.seatFree(row, seat, now) {
					free = false
					break
				}
			}
			if !free {
				continue
			}
			left := n.runLen(row, s-1, -1, now)
			right := n.runLen(row, s+k, 1, now)
			dev := 2*s + k - 1 - (n.width + 1)
			if dev < 0 {
				dev = -dev
			}
			all = append(all, naiveCandidate{
				row: row, start: s, dev: dev,
				orphan: left == 1 || right == 1,
			})
		}
	}

	var pool []naiveCandidate
	pass := 1
	for _, c := range all {
		if !c.orphan {
			pool = append(pool, c)
		}
	}
	if len(pool) == 0 {
		pool = all
		pass = 2
	}
	if len(pool) == 0 {
		return HoldResult{}, "无座", ErrNoSeats
	}

	best := pool[0]
	for _, c := range pool[1:] {
		if c.row < best.row ||
			(c.row == best.row && c.dev < best.dev) ||
			(c.row == best.row && c.dev == best.dev && c.start < best.start) {
			best = c
		}
	}

	n.maxNow = now
	n.seen = true
	n.holds = append(n.holds, naiveHold{
		row: best.row, start: best.start, size: k,
		expiry: now + n.ttl, status: holdActive,
	})
	why := fmt.Sprintf("第%d遍: 候选%d个 池%d个 选中(排%d 座%d 偏离%d)",
		pass, len(all), len(pool), best.row, best.start, best.dev)
	return HoldResult{ID: len(n.holds), Row: best.row, Start: best.start}, why, nil
}

func (n *naiveRegistrar) settle(id int, now int64, target holdStatus) error {
	if err := n.checkClock(now); err != nil {
		return err
	}
	if id < 1 || id > len(n.holds) {
		return ErrHoldNotFound
	}
	h := &n.holds[id-1]
	switch {
	case h.status == holdConfirmed:
		return ErrHoldConfirmed
	case h.status == holdReleased:
		return ErrHoldReleased
	case now >= h.expiry:
		return ErrHoldExpired
	}
	n.maxNow = now
	n.seen = true
	h.status = target
	return nil
}

func (n *naiveRegistrar) confirm(id int, now int64) error {
	return n.settle(id, now, holdConfirmed)
}

func (n *naiveRegistrar) release(id int, now int64) error {
	return n.settle(id, now, holdReleased)
}

func (n *naiveRegistrar) seats(now int64) [][]SeatState {
	out := make([][]SeatState, n.rows)
	for row := 1; row <= n.rows; row++ {
		out[row-1] = make([]SeatState, n.width)
		for seat := 1; seat <= n.width; seat++ {
			if n.seatFree(row, seat, now) {
				continue
			}
			out[row-1][seat-1] = SeatHeld
			for _, h := range n.holds {
				if h.status == holdConfirmed && h.row == row &&
					seat >= h.start && seat < h.start+h.size {
					out[row-1][seat-1] = SeatConfirmed
				}
			}
		}
	}
	return out
}

// TestRandomSequencesAgainstNaive 用 2000 组随机操作序列
// 将 Registrar 与朴素实现逐步对照，日志打印输入、输出与判定依据。
func TestRandomSequencesAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(1064))

	for seq := 0; seq < 2000; seq++ {
		rows := 1 + rng.Intn(6)
		width := 1 + rng.Intn(10)
		ttl := int64(1 + rng.Intn(20))

		reg, err := New(rows, width, ttl)
		if err != nil {
			t.Fatalf("seq=%d New 失败: %v", seq, err)
		}
		naive := newNaive(rows, width, ttl)

		ops := 10 + rng.Intn(30)
		now := int64(0)
		t.Logf("seq=%d 输入: R=%d W=%d T=%d ops=%d", seq, rows, width, ttl, ops)

		for op := 0; op < ops; op++ {
			// 时刻通常单调前进，偶尔回退以触发时钟回退。
			if rng.Intn(10) == 0 && now > 0 {
				now -= int64(rng.Intn(int(now) + 1))
			} else {
				now += int64(rng.Intn(4))
			}

			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4: // Hold
				k := 1 + rng.Intn(9) // 含非法 k=9
				gotRes, gotErr := reg.Hold(k, now)
				wantRes, why, wantErr := naive.hold(k, now)
				t.Logf("seq=%d op=%d 输入 Hold(k=%d, now=%d) 输出 got=(%+v, %v) want=(%+v, %v) 依据: %s",
					seq, op, k, now, gotRes, gotErr, wantRes, wantErr, why)
				if !errors.Is(gotErr, wantErr) {
					t.Fatalf("seq=%d op=%d Hold 错误不一致: got %v, want %v", seq, op, gotErr, wantErr)
				}
				if gotErr == nil && gotRes != wantRes {
					t.Fatalf("seq=%d op=%d Hold 结果不一致: got %+v, want %+v", seq, op, gotRes, wantRes)
				}
			case 5, 6, 7: // Confirm
				id := 1 + rng.Intn(len(naive.holds)+2)
				gotErr := reg.Confirm(id, now)
				wantErr := naive.confirm(id, now)
				t.Logf("seq=%d op=%d 输入 Confirm(id=%d, now=%d) 输出 got=%v want=%v",
					seq, op, id, now, gotErr, wantErr)
				if !errors.Is(gotErr, wantErr) {
					t.Fatalf("seq=%d op=%d Confirm 错误不一致: got %v, want %v", seq, op, gotErr, wantErr)
				}
			case 8: // Release
				id := 1 + rng.Intn(len(naive.holds)+2)
				gotErr := reg.Release(id, now)
				wantErr := naive.release(id, now)
				t.Logf("seq=%d op=%d 输入 Release(id=%d, now=%d) 输出 got=%v want=%v",
					seq, op, id, now, gotErr, wantErr)
				if !errors.Is(gotErr, wantErr) {
					t.Fatalf("seq=%d op=%d Release 错误不一致: got %v, want %v", seq, op, gotErr, wantErr)
				}
			default: // Seats
				got := reg.Seats(now)
				want := naive.seats(now)
				t.Logf("seq=%d op=%d 输入 Seats(now=%d) 输出一致=%v", seq, op, now, reflect.DeepEqual(got, want))
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("seq=%d op=%d Seats 不一致:\ngot  %v\nwant %v", seq, op, got, want)
				}
			}

			// 每个操作后再对照一次当前视角的座位图。
			if got, want := reg.Seats(now), naive.seats(now); !reflect.DeepEqual(got, want) {
				t.Fatalf("seq=%d op=%d 后 Seats(%d) 不一致:\ngot  %v\nwant %v", seq, op, now, got, want)
			}
		}
	}
}

// TestConcurrentInvariants 并发调用下验证：
// 保留号严格连续、同一座位不被两个活跃保留或确认订单同时占用、
// 空闲+保留+已确认座位数恒等于 R*W。
func TestConcurrentInvariants(t *testing.T) {
	const rows, width = 4, 10
	reg := mustNew(t, rows, width, 5)

	var wg sync.WaitGroup
	var now atomic.Int64
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 500; i++ {
				n := now.Add(1) - 1
				switch rng.Intn(4) {
				case 0, 1:
					res, err := reg.Hold(1+rng.Intn(8), n)
					if err == nil && rng.Intn(2) == 0 {
						reg.Confirm(res.ID, now.Add(1)-1)
					}
				case 2:
					reg.Release(1+rng.Intn(50), n)
				default:
					reg.Seats(n)
				}
			}
		}(int64(g))
	}
	wg.Wait()

	// 保留号严格连续无空洞。
	for i, h := range reg.holds {
		if h == nil {
			t.Fatalf("保留号 %d 出现空洞", i+1)
		}
	}

	// 任一时刻同一座位不被两个活跃保留或确认订单同时占用：
	// 占座区间为 [created, end)，已确认 end 为 +∞，已释放不占座。
	for i, a := range reg.holds {
		if a.status == holdReleased {
			continue
		}
		endA := a.expiry
		if a.status == holdConfirmed {
			endA = int64(1) << 62
		}
		for j := i + 1; j < len(reg.holds); j++ {
			b := reg.holds[j]
			if b.status == holdReleased || b.row != a.row {
				continue
			}
			seatsOverlap := a.start < b.start+b.size && b.start < a.start+a.size
			if seatsOverlap && b.created < endA {
				t.Fatalf("保留号 %d 与 %d 在同一时刻占用同一座位", i+1, j+1)
			}
		}
	}

	// 空闲+保留+已确认座位数恒等于 R*W。
	final := now.Load()
	counts := map[SeatState]int{}
	for _, row := range reg.Seats(final) {
		for _, s := range row {
			counts[s]++
		}
	}
	total := counts[SeatFree] + counts[SeatHeld] + counts[SeatConfirmed]
	if total != rows*width {
		t.Fatalf("座位数之和 = %d, 期望 %d", total, rows*width)
	}
}

// TestReplayDeterminism 相同的操作序列重放得到完全相同的选座结果。
func TestReplayDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	type op struct {
		kind int
		k    int
		id   int
		now  int64
	}
	var ops []op
	now := int64(0)
	for i := 0; i < 200; i++ {
		now += int64(rng.Intn(3))
		ops = append(ops, op{kind: rng.Intn(3), k: 1 + rng.Intn(8), id: 1 + rng.Intn(60), now: now})
	}

	run := func() []HoldResult {
		reg := mustNew(t, 3, 12, 6)
		var results []HoldResult
		for _, o := range ops {
			switch o.kind {
			case 0:
				res, _ := reg.Hold(o.k, o.now)
				results = append(results, res)
			case 1:
				reg.Confirm(o.id, o.now)
			default:
				reg.Release(o.id, o.now)
			}
		}
		return results
	}

	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("重放结果不一致:\n第一次 %v\n第二次 %v", first, second)
	}
}
