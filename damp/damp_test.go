package damp_test

import (
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"

	"ontology/damp"
)

func mustNew(t *testing.T, delta, num, den, ps, pr, pmax, tmax int64) *damp.Dampener {
	t.Helper()
	d, err := damp.New(delta, num, den, ps, pr, pmax, tmax)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}

func wantEvents(t *testing.T, got []damp.Event, want ...damp.Event) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events=%+v, want %+v", got, want)
	}
}

func wantPenalty(t *testing.T, d *damp.Dampener, peer, prefix, at, wantP int64, wantSup bool) {
	t.Helper()
	p, sup, err := d.Penalty(peer, prefix, at)
	if err != nil {
		t.Fatalf("Penalty(%d,%d,%d): %v", peer, prefix, at, err)
	}
	if p != wantP || sup != wantSup {
		t.Fatalf("Penalty(%d,%d,%d)=(%d,%v), want (%d,%v)", peer, prefix, at, p, sup, wantP, wantSup)
	}
}

func ann(t *testing.T, d *damp.Dampener, peer, prefix int64, attr uint64, now int64) []damp.Event {
	t.Helper()
	evs, err := d.Announce(peer, prefix, attr, now)
	if err != nil {
		t.Fatalf("Announce(%d,%d,%d,%d): %v", peer, prefix, attr, now, err)
	}
	return evs
}

func wd(t *testing.T, d *damp.Dampener, peer, prefix, now int64) []damp.Event {
	t.Helper()
	evs, err := d.Withdraw(peer, prefix, now)
	if err != nil {
		t.Fatalf("Withdraw(%d,%d,%d): %v", peer, prefix, now, err)
	}
	return evs
}

func pd(t *testing.T, d *damp.Dampener, peer, now int64) []damp.Event {
	t.Helper()
	evs, err := d.PeerDown(peer, now)
	if err != nil {
		t.Fatalf("PeerDown(%d,%d): %v", peer, now, err)
	}
	return evs
}

func tick(t *testing.T, d *damp.Dampener, now int64) []damp.Event {
	t.Helper()
	evs, err := d.Tick(now)
	if err != nil {
		t.Fatalf("Tick(%d): %v", now, err)
	}
	return evs
}

func TestNewValidation(t *testing.T) {
	cases := []struct {
		name                                string
		delta, num, den, ps, pr, pmax, tmax int64
	}{
		{"delta=0", 0, 1, 2, 100, 10, 1000, 100},
		{"delta>1e6", 1_000_001, 1, 2, 100, 10, 1000, 100},
		{"num=0", 10, 0, 2, 100, 10, 1000, 100},
		{"num=den", 10, 2, 2, 100, 10, 1000, 100},
		{"den>1000", 10, 1, 1001, 100, 10, 1000, 100},
		{"pr=0", 10, 1, 2, 100, 0, 1000, 100},
		{"pr=ps", 10, 1, 2, 100, 100, 1000, 100},
		{"ps>pmax", 10, 1, 2, 200, 100, 150, 100},
		{"pmax>1e9", 10, 1, 2, 100, 10, 1_000_000_001, 100},
		{"tmax=0", 10, 1, 2, 100, 10, 1000, 0},
		{"tmax>1e9", 10, 1, 2, 100, 10, 1000, 1_000_000_001},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := damp.New(c.delta, c.num, c.den, c.ps, c.pr, c.pmax, c.tmax)
			if !errors.Is(err, damp.ErrInvalidParam) {
				t.Fatalf("err=%v, want ErrInvalidParam", err)
			}
		})
	}
	if _, err := damp.New(1, 1, 2, 2, 1, 2, 1); err != nil {
		t.Fatalf("valid boundary params rejected: %v", err)
	}
}

// 题面主例：Δ=10, 3/4, Ps=2000, Pr=800, Pmax=6000, Tmax=200。
// 验证 last 相位对齐（reuseAt=65 而非 66）。
func TestWorkedExample(t *testing.T) {
	d := mustNew(t, 10, 3, 4, 2000, 800, 6000, 200)

	wantEvents(t, ann(t, d, 1, 1, 7, 0))
	wantEvents(t, wd(t, d, 1, 1, 5))
	wantPenalty(t, d, 1, 1, 5, 1000, false)

	wantEvents(t, ann(t, d, 1, 1, 7, 12))
	wantEvents(t, wd(t, d, 1, 1, 16))
	wantPenalty(t, d, 1, 1, 16, 1750, false)

	wantEvents(t, ann(t, d, 1, 1, 7, 24))
	wantEvents(t, wd(t, d, 1, 1, 26))
	wantPenalty(t, d, 1, 1, 26, 2312, true)

	wantEvents(t, ann(t, d, 1, 1, 7, 30))
	wantPenalty(t, d, 1, 1, 30, 2312, true) // 在通告中但被抑制

	wantEvents(t, tick(t, d, 64))
	// last=25 相位对齐：25+4·10=65；若误置 last=now=26 会得 66。
	wantEvents(t, tick(t, d, 65), damp.Event{Peer: 1, Prefix: 1, At: 65, Usable: true})
	wantPenalty(t, d, 1, 1, 65, 731, false)
}

// Tmax=30 变体：reuseAt=min(56,65)=56，因 Tmax 解除后 p 仍高于 Pr。
func TestWorkedExampleTmax(t *testing.T) {
	d := mustNew(t, 10, 3, 4, 2000, 800, 6000, 30)

	ann(t, d, 1, 1, 7, 0)
	wd(t, d, 1, 1, 5)
	ann(t, d, 1, 1, 7, 12)
	wd(t, d, 1, 1, 16)
	ann(t, d, 1, 1, 7, 24)
	wd(t, d, 1, 1, 26)
	wantPenalty(t, d, 1, 1, 26, 2312, true)

	wantEvents(t, tick(t, d, 55))
	wantEvents(t, tick(t, d, 56), damp.Event{Peer: 1, Prefix: 1, At: 56, Usable: false})
	// p=975 仍大于 Pr=800，但不再抑制。
	wantPenalty(t, d, 1, 1, 56, 975, false)

	// 再受罚才重新判定：1975<Ps 不抑制。
	ann(t, d, 1, 1, 7, 57)
	wd(t, d, 1, 1, 58)
	wantPenalty(t, d, 1, 1, 58, 1975, false)

	// 2975>=Ps 重新抑制，取新的 supSince=60。
	ann(t, d, 1, 1, 7, 59)
	wd(t, d, 1, 1, 60)
	wantPenalty(t, d, 1, 1, 60, 2975, true)
}

// 恰在 reuseAt 到来的加罚：先解除（报事件），再加罚，再按新值判定。
func TestPenaltyAtReuseAt(t *testing.T) {
	d := mustNew(t, 10, 3, 4, 2000, 800, 6000, 200)

	ann(t, d, 1, 1, 7, 0)
	wd(t, d, 1, 1, 5)
	ann(t, d, 1, 1, 7, 12)
	wd(t, d, 1, 1, 16)
	ann(t, d, 1, 1, 7, 24)
	wd(t, d, 1, 1, 26)
	ann(t, d, 1, 1, 7, 30)

	// t=65 的 Withdraw：先在 65 解除（Usable 真），结算 p=731、last=65，
	// 加罚得 1731<Ps，不再抑制，路由已撤销。
	wantEvents(t, wd(t, d, 1, 1, 65), damp.Event{Peer: 1, Prefix: 1, At: 65, Usable: true})
	wantPenalty(t, d, 1, 1, 65, 1731, false)

	if _, err := d.Withdraw(1, 1, 66); !errors.Is(err, damp.ErrRouteNotFound) {
		t.Fatalf("err=%v, want ErrRouteNotFound", err)
	}
}

// PeerDown 不加罚：惩罚与抑制状态原样保留。
func TestPeerDownNoPenalty(t *testing.T) {
	d := mustNew(t, 10, 3, 4, 2000, 800, 6000, 200)

	ann(t, d, 1, 1, 7, 0)
	wd(t, d, 1, 1, 5)
	ann(t, d, 1, 1, 7, 12)
	wd(t, d, 1, 1, 16)
	ann(t, d, 1, 1, 7, 24)

	// t=26 的 PeerDown：不加罚，p 结算为 1312，不抑制。
	wantEvents(t, pd(t, d, 1, 26))
	wantPenalty(t, d, 1, 1, 26, 1312, false)

	if _, err := d.PeerDown(1, 27); !errors.Is(err, damp.ErrPeerNotFound) {
		t.Fatalf("err=%v, want ErrPeerNotFound", err)
	}
	if _, err := d.Withdraw(1, 1, 28); !errors.Is(err, damp.ErrRouteNotFound) {
		t.Fatalf("err=%v, want ErrRouteNotFound", err)
	}
}

// PeerDown 撤销被抑制的通告路由：抑制状态保留，解除时 Usable 为假。
func TestPeerDownKeepsSuppression(t *testing.T) {
	d := mustNew(t, 10, 3, 4, 2000, 800, 6000, 200)

	ann(t, d, 1, 1, 7, 0)
	wd(t, d, 1, 1, 5)
	ann(t, d, 1, 1, 7, 12)
	wd(t, d, 1, 1, 16)
	ann(t, d, 1, 1, 7, 24)
	wd(t, d, 1, 1, 26)
	ann(t, d, 1, 1, 7, 30)

	wantEvents(t, pd(t, d, 1, 31))
	wantPenalty(t, d, 1, 1, 31, 2312, true) // 仍被抑制
	wantEvents(t, tick(t, d, 65), damp.Event{Peer: 1, Prefix: 1, At: 65, Usable: false})
}

// 阈值边界：p 恰等 Ps 抑制、小 1 不抑制；衰减后恰等 Pr 不解除、小 1 解除。
func TestThresholds(t *testing.T) {
	t.Run("p=Ps 抑制", func(t *testing.T) {
		d := mustNew(t, 1000, 3, 4, 2000, 800, 6000, 200)
		ann(t, d, 1, 1, 1, 0)
		wd(t, d, 1, 1, 1)
		ann(t, d, 1, 1, 1, 2)
		wd(t, d, 1, 1, 3) // 1000+1000=2000=Ps
		wantPenalty(t, d, 1, 1, 3, 2000, true)
	})
	t.Run("p=Ps-1 不抑制", func(t *testing.T) {
		d := mustNew(t, 1000, 3, 4, 2001, 800, 6000, 200)
		ann(t, d, 1, 1, 1, 0)
		wd(t, d, 1, 1, 1)
		ann(t, d, 1, 1, 1, 2)
		wd(t, d, 1, 1, 3) // 2000<Ps=2001
		wantPenalty(t, d, 1, 1, 3, 2000, false)
	})
	t.Run("衰减后恰等 Pr 不解除", func(t *testing.T) {
		d := mustNew(t, 10, 1, 2, 1600, 800, 1600, 100000)
		ann(t, d, 1, 1, 1, 0)
		wd(t, d, 1, 1, 1)
		ann(t, d, 1, 1, 1, 2)
		wd(t, d, 1, 1, 3) // 2000 截断到 Pmax=1600=Ps
		// k=0 故 last 保持 1；1600->800(恰等 Pr 不解除)->400，j=2，reuseAt=1+20=21。
		wantEvents(t, tick(t, d, 20))
		wantEvents(t, tick(t, d, 21), damp.Event{Peer: 1, Prefix: 1, At: 21, Usable: false})
		wantPenalty(t, d, 1, 1, 21, 400, false)
	})
	t.Run("小 1 解除", func(t *testing.T) {
		d := mustNew(t, 10, 1, 2, 1599, 800, 1599, 100000)
		ann(t, d, 1, 1, 1, 0)
		wd(t, d, 1, 1, 1)
		ann(t, d, 1, 1, 1, 2)
		wd(t, d, 1, 1, 3) // 截断到 1599
		// last=1；1599->799<800，j=1，reuseAt=1+10=11。
		wantEvents(t, tick(t, d, 10))
		wantEvents(t, tick(t, d, 11), damp.Event{Peer: 1, Prefix: 1, At: 11, Usable: false})
		wantPenalty(t, d, 1, 1, 11, 799, false)
	})
}

// 抑制中再受罚：按新的 p 与 last 重算 reuseAt，supSince 不变。
func TestRepenaltyWhileSuppressed(t *testing.T) {
	d := mustNew(t, 10, 1, 2, 1000, 100, 5000, 45)

	ann(t, d, 1, 1, 1, 0)
	wd(t, d, 1, 1, 1) // p=1000=Ps，supSince=1，j=4，reuseAt=min(46,41)=41
	ann(t, d, 1, 1, 1, 2)
	// t=13 attr 变化：结算 k=1 得 p=500、last=11，加罚 500 得 1000；
	// 重算 reuseAt=min(1+45, 11+40)=46（supSince 仍为 1，否则得 51）。
	ann(t, d, 1, 1, 2, 13)
	wantPenalty(t, d, 1, 1, 13, 1000, true)

	wantEvents(t, tick(t, d, 41)) // 原 reuseAt 已被重算覆盖
	wantEvents(t, tick(t, d, 45))
	wantEvents(t, tick(t, d, 46), damp.Event{Peer: 1, Prefix: 1, At: 46, Usable: true})
	wantPenalty(t, d, 1, 1, 46, 125, false)
}

// 重复通告不加罚；attr 变化加罚 500。
func TestDuplicateAnnounce(t *testing.T) {
	d := mustNew(t, 10, 3, 4, 2000, 800, 6000, 200)

	ann(t, d, 1, 1, 5, 0)
	ann(t, d, 1, 1, 5, 10) // 重复通告
	wantPenalty(t, d, 1, 1, 10, 0, false)

	ann(t, d, 1, 1, 6, 20) // attr 变化加罚 500
	wantPenalty(t, d, 1, 1, 20, 500, false)

	ann(t, d, 1, 1, 6, 30)                  // 再次重复通告
	wantPenalty(t, d, 1, 1, 30, 375, false) // 500 衰减一步
}

// 逐步取整与一次取整的差异：p=13 两步逐步得 6，一次乘 9/16 取整得 7。
func TestStepwiseRounding(t *testing.T) {
	d := mustNew(t, 10, 3, 4, 13, 1, 13, 200)
	ann(t, d, 1, 1, 1, 0)
	wd(t, d, 1, 1, 1)                    // p=min(1000,13)=13，last=1
	wantPenalty(t, d, 1, 1, 21, 6, true) // k=2：13->9->6
}

// 被拒绝的操作：不改状态、不落地解除、不推进时钟；错误可用 errors.Is 区分，
// 优先级为参数非法 > 时钟回退 > 状态类。
func TestRejectedOpsDoNotLand(t *testing.T) {
	d := mustNew(t, 10, 3, 4, 2000, 800, 6000, 200)

	ann(t, d, 1, 1, 7, 0)
	wd(t, d, 1, 1, 5)
	ann(t, d, 1, 1, 7, 12)
	wd(t, d, 1, 1, 16)
	ann(t, d, 1, 1, 7, 24)
	wd(t, d, 1, 1, 26) // 抑制，reuseAt=65
	wantEvents(t, tick(t, d, 30))

	// 状态类拒绝：不落地 65 的解除、不推进时钟。
	if _, err := d.Withdraw(1, 999, 65); !errors.Is(err, damp.ErrRouteNotFound) {
		t.Fatalf("err=%v, want ErrRouteNotFound", err)
	}
	ann(t, d, 1, 1, 7, 40) // 时钟仍在 30，40 被接受

	if _, err := d.Tick(39); !errors.Is(err, damp.ErrClockBack) {
		t.Fatalf("err=%v, want ErrClockBack", err)
	}
	if _, err := d.Announce(0, 1, 7, 40); !errors.Is(err, damp.ErrInvalidParam) {
		t.Fatalf("err=%v, want ErrInvalidParam", err)
	}
	// 参数非法优先于时钟回退。
	if _, err := d.Announce(0, 1, 7, 10); !errors.Is(err, damp.ErrInvalidParam) {
		t.Fatalf("err=%v, want ErrInvalidParam (param > clock)", err)
	}
	// 时钟回退优先于状态类。
	if _, err := d.Withdraw(1, 999, 10); !errors.Is(err, damp.ErrClockBack) {
		t.Fatalf("err=%v, want ErrClockBack (clock > state)", err)
	}

	wantEvents(t, tick(t, d, 64))
	// 65 的解除未被此前的拒绝操作落地，此刻才触发。
	wantEvents(t, tick(t, d, 65), damp.Event{Peer: 1, Prefix: 1, At: 65, Usable: true})
}

// Penalty 只读：不推进时钟、不落地解除。
func TestPenaltyReadOnly(t *testing.T) {
	d := mustNew(t, 10, 3, 4, 2000, 800, 6000, 200)

	ann(t, d, 1, 1, 7, 0)
	wd(t, d, 1, 1, 5)
	ann(t, d, 1, 1, 7, 12)
	wd(t, d, 1, 1, 16)
	ann(t, d, 1, 1, 7, 24)
	wd(t, d, 1, 1, 26) // 抑制，reuseAt=65
	wantEvents(t, tick(t, d, 30))

	// t=100 只读结算：k=7，p=308；t>=reuseAt 时抑制状态按时间判定为假。
	wantPenalty(t, d, 1, 1, 100, 308, false)
	// 时钟未被 Penalty 推进：now=40 仍被接受。
	ann(t, d, 1, 1, 7, 40)
	// 解除未被 Penalty 落地：Tick(65) 仍报事件。
	wantEvents(t, tick(t, d, 65), damp.Event{Peer: 1, Prefix: 1, At: 65, Usable: true})

	// 无记录路由返回 0 与未抑制。
	wantPenalty(t, d, 2, 2, 65, 0, false)
	// t 小于已接受的最大 now 被拒绝。
	if _, _, err := d.Penalty(1, 1, 10); !errors.Is(err, damp.ErrClockBack) {
		t.Fatalf("err=%v, want ErrClockBack", err)
	}
}

// 相同操作序列重放得到相同的事件清单。
func TestReplayDeterminism(t *testing.T) {
	type op struct {
		kind         int
		peer, prefix int64
		attr         uint64
		now          int64
	}
	seq := []op{
		{0, 1, 1, 7, 0}, {0, 2, 1, 9, 0}, {1, 1, 1, 0, 5}, {0, 1, 1, 8, 12},
		{1, 1, 1, 0, 16}, {0, 1, 1, 7, 24}, {1, 1, 1, 0, 26}, {3, 0, 0, 0, 30},
		{0, 1, 1, 7, 30}, {2, 2, 0, 0, 31}, {3, 0, 0, 0, 64}, {3, 0, 0, 0, 65},
		{1, 1, 1, 0, 65}, {3, 0, 0, 0, 100},
	}
	run := func() [][]damp.Event {
		d := mustNew(t, 10, 3, 4, 2000, 800, 6000, 200)
		var out [][]damp.Event
		for _, o := range seq {
			var evs []damp.Event
			switch o.kind {
			case 0:
				evs = ann(t, d, o.peer, o.prefix, o.attr, o.now)
			case 1:
				evs = wd(t, d, o.peer, o.prefix, o.now)
			case 2:
				evs = pd(t, d, o.peer, o.now)
			case 3:
				evs = tick(t, d, o.now)
			}
			out = append(out, evs)
		}
		return out
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay mismatch:\nfirst=%v\nsecond=%v", first, second)
	}
}

// 并发调用等价于某个串行顺序（配合 -race）；校验不变量 0<=p<=Pmax。
func TestConcurrency(t *testing.T) {
	d := mustNew(t, 10, 3, 4, 2000, 800, 6000, 1000)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			peer := int64(g%3 + 1)
			for i := 0; i < 200; i++ {
				now := int64(g*1000 + i)
				switch i % 4 {
				case 0:
					d.Announce(peer, int64(i%5+1), uint64(i), now)
				case 1:
					d.Withdraw(peer, int64(i%5+1), now)
				case 2:
					d.Tick(now)
				case 3:
					d.Penalty(peer, int64(i%5+1), now)
				}
			}
		}(g)
	}
	wg.Wait()
	for peer := int64(1); peer <= 3; peer++ {
		for prefix := int64(1); prefix <= 5; prefix++ {
			p, _, err := d.Penalty(peer, prefix, 1_000_000_000_000)
			if err != nil {
				t.Fatalf("Penalty(%d,%d): %v", peer, prefix, err)
			}
			if p < 0 || p > 6000 {
				t.Fatalf("Penalty(%d,%d)=%d out of [0,Pmax]", peer, prefix, p)
			}
		}
	}
}

// naiveSim 是逐毫秒推进的朴素模拟：每毫秒逐路由施加一步衰减、
// 逐毫秒检查解除时刻，用于对照验证跳跃式实现。
type naiveSim struct {
	delta, num, den, ps, pr, pmax, tmax int64
	clock                               int64
	recs                                map[[2]int64]*naiveRoute
	announced                           map[[2]int64]uint64
	byPeer                              map[int64]map[int64]bool
}

type naiveRoute struct {
	p, last    int64
	hasRec     bool
	suppressed bool
	supSince   int64
	reuseAt    int64
}

func newNaive(delta, num, den, ps, pr, pmax, tmax int64) *naiveSim {
	return &naiveSim{
		delta: delta, num: num, den: den, ps: ps, pr: pr, pmax: pmax, tmax: tmax,
		recs:      make(map[[2]int64]*naiveRoute),
		announced: make(map[[2]int64]uint64),
		byPeer:    make(map[int64]map[int64]bool),
	}
}

func (s *naiveSim) checkClock(now int64) error {
	if now < 0 || now > 1_000_000_000_000 {
		return damp.ErrInvalidParam
	}
	if now < s.clock {
		return damp.ErrClockBack
	}
	return nil
}

// advance 逐毫秒推进到 now：每毫秒对每条记录施加一步衰减（若跨过步边界），
// 并收集恰在该毫秒解除的事件（按 reuseAt、邻居、前缀排序）。
func (s *naiveSim) advance(now int64) []damp.Event {
	var evs []damp.Event
	for t := s.clock + 1; t <= now; t++ {
		for _, r := range s.recs {
			if r.hasRec && t-r.last >= s.delta {
				r.p = r.p * s.num / s.den
				r.last += s.delta
			}
		}
		var due [][2]int64
		for k, r := range s.recs {
			if r.suppressed && r.reuseAt == t {
				due = append(due, k)
			}
		}
		sort.Slice(due, func(a, b int) bool {
			if due[a][0] != due[b][0] {
				return due[a][0] < due[b][0]
			}
			return due[a][1] < due[b][1]
		})
		for _, k := range due {
			s.recs[k].suppressed = false
			_, ann := s.announced[k]
			evs = append(evs, damp.Event{Peer: k[0], Prefix: k[1], At: t, Usable: ann})
		}
	}
	s.clock = now
	return evs
}

func (s *naiveSim) schedule(r *naiveRoute) {
	p, j := r.p, int64(0)
	for {
		p = p * s.num / s.den
		j++
		if p < s.pr {
			break
		}
	}
	at := r.supSince + s.tmax
	if x := r.last + j*s.delta; x < at {
		at = x
	}
	r.reuseAt = at
}

func (s *naiveSim) penalize(key [2]int64, amt, now int64) {
	r := s.recs[key]
	if r == nil {
		r = &naiveRoute{}
		s.recs[key] = r
	}
	if !r.hasRec {
		r.hasRec = true
		r.last = now
	}
	r.p += amt
	if r.p > s.pmax {
		r.p = s.pmax
	}
	if r.suppressed {
		s.schedule(r)
	} else if r.p >= s.ps {
		r.suppressed = true
		r.supSince = now
		s.schedule(r)
	}
}

func (s *naiveSim) announce(peer, prefix int64, attr uint64, now int64) ([]damp.Event, error) {
	if peer < 1 || peer > 1_000_000 || prefix < 1 || prefix > 1_000_000_000 {
		return nil, damp.ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	key := [2]int64{peer, prefix}
	evs := s.advance(now)
	old, isAnn := s.announced[key]
	switch {
	case !isAnn:
		s.announced[key] = attr
		set := s.byPeer[peer]
		if set == nil {
			set = make(map[int64]bool)
			s.byPeer[peer] = set
		}
		set[prefix] = true
	case old != attr:
		s.announced[key] = attr
		s.penalize(key, 500, now)
	}
	return evs, nil
}

func (s *naiveSim) withdraw(peer, prefix int64, now int64) ([]damp.Event, error) {
	if peer < 1 || peer > 1_000_000 || prefix < 1 || prefix > 1_000_000_000 {
		return nil, damp.ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	key := [2]int64{peer, prefix}
	if _, ok := s.announced[key]; !ok {
		return nil, damp.ErrRouteNotFound
	}
	evs := s.advance(now)
	delete(s.announced, key)
	delete(s.byPeer[peer], prefix)
	if len(s.byPeer[peer]) == 0 {
		delete(s.byPeer, peer)
	}
	s.penalize(key, 1000, now)
	return evs, nil
}

func (s *naiveSim) peerDown(peer int64, now int64) ([]damp.Event, error) {
	if peer < 1 || peer > 1_000_000 {
		return nil, damp.ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	set := s.byPeer[peer]
	if len(set) == 0 {
		return nil, damp.ErrPeerNotFound
	}
	evs := s.advance(now)
	for prefix := range set {
		delete(s.announced, [2]int64{peer, prefix})
	}
	delete(s.byPeer, peer)
	return evs, nil
}

func (s *naiveSim) tick(now int64) ([]damp.Event, error) {
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	return s.advance(now), nil
}

func (s *naiveSim) penalty(peer, prefix int64, t int64) (int64, bool, error) {
	if peer < 1 || peer > 1_000_000 || prefix < 1 || prefix > 1_000_000_000 {
		return 0, false, damp.ErrInvalidParam
	}
	if err := s.checkClock(t); err != nil {
		return 0, false, err
	}
	r := s.recs[[2]int64{peer, prefix}]
	if r == nil || !r.hasRec {
		return 0, false, nil
	}
	p := r.p
	for k := (t - r.last) / s.delta; k > 0 && p > 0; k-- {
		p = p * s.num / s.den
	}
	return p, r.suppressed && t < r.reuseAt, nil
}

func errClass(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, damp.ErrInvalidParam):
		return damp.ErrInvalidParam
	case errors.Is(err, damp.ErrClockBack):
		return damp.ErrClockBack
	case errors.Is(err, damp.ErrRouteNotFound):
		return damp.ErrRouteNotFound
	case errors.Is(err, damp.ErrPeerNotFound):
		return damp.ErrPeerNotFound
	}
	return err
}

func eventsEqual(a, b []damp.Event) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// 1500 组随机操作序列：跳跃式实现与逐毫秒朴素模拟对照，
// 同时用第二实例重放验证事件清单确定可复现。
func TestRandomVsNaive(t *testing.T) {
	const sequences = 1500
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)))
		delta := 1 + rng.Int63n(20)
		den := 2 + rng.Int63n(7)
		num := 1 + rng.Int63n(den-1)
		pr := 1 + rng.Int63n(200)
		ps := pr + 1 + rng.Int63n(500)
		pmax := ps + rng.Int63n(2000)
		tmax := 1 + rng.Int63n(120)

		d1, err := damp.New(delta, num, den, ps, pr, pmax, tmax)
		if err != nil {
			t.Fatalf("seq=%d New: %v", seq, err)
		}
		d2, err := damp.New(delta, num, den, ps, pr, pmax, tmax)
		if err != nil {
			t.Fatalf("seq=%d New: %v", seq, err)
		}
		sim := newNaive(delta, num, den, ps, pr, pmax, tmax)

		now := int64(0)
		ops := 20 + rng.Intn(30)
		t.Logf("seq=%d 输入: Δ=%d num/den=%d/%d Ps=%d Pr=%d Pmax=%d Tmax=%d ops=%d",
			seq, delta, num, den, ps, pr, pmax, tmax, ops)

		for i := 0; i < ops; i++ {
			now += rng.Int63n(25)
			peer := 1 + rng.Int63n(3)
			prefix := 1 + rng.Int63n(4)
			attr := uint64(rng.Int63n(3))
			kind := rng.Intn(10)

			var ev1, ev2, evN []damp.Event
			var err1, err2, errN error
			var opDesc string
			switch {
			case kind <= 3:
				opDesc = "Announce"
				ev1, err1 = d1.Announce(peer, prefix, attr, now)
				ev2, err2 = d2.Announce(peer, prefix, attr, now)
				evN, errN = sim.announce(peer, prefix, attr, now)
			case kind <= 6:
				opDesc = "Withdraw"
				ev1, err1 = d1.Withdraw(peer, prefix, now)
				ev2, err2 = d2.Withdraw(peer, prefix, now)
				evN, errN = sim.withdraw(peer, prefix, now)
			case kind == 7:
				opDesc = "PeerDown"
				ev1, err1 = d1.PeerDown(peer, now)
				ev2, err2 = d2.PeerDown(peer, now)
				evN, errN = sim.peerDown(peer, now)
			case kind == 8:
				opDesc = "Tick"
				ev1, err1 = d1.Tick(now)
				ev2, err2 = d2.Tick(now)
				evN, errN = sim.tick(now)
			default:
				opDesc = "Penalty"
				tq := now + rng.Int63n(30)
				p1, s1, e1 := d1.Penalty(peer, prefix, tq)
				p2, s2, e2 := d2.Penalty(peer, prefix, tq)
				pN, sN, eN := sim.penalty(peer, prefix, tq)
				t.Logf("seq=%d op=%d %s(%d,%d,t=%d) 输出: p=%d sup=%v err=%v; 判定依据: 朴素模拟 p=%d sup=%v err=%v",
					seq, i, opDesc, peer, prefix, tq, p1, s1, errClass(e1), pN, sN, errClass(eN))
				if errClass(e1) != errClass(eN) || p1 != pN || s1 != sN {
					t.Fatalf("seq=%d op=%d Penalty(%d,%d,%d)=(%d,%v,%v), 朴素模拟=(%d,%v,%v)",
						seq, i, peer, prefix, tq, p1, s1, e1, pN, sN, eN)
				}
				if p1 != p2 || s1 != s2 || errClass(e1) != errClass(e2) {
					t.Fatalf("seq=%d op=%d 重放 Penalty 不一致", seq, i)
				}
				continue
			}

			t.Logf("seq=%d op=%d %s(peer=%d,prefix=%d,attr=%d,now=%d) 输出: events=%v err=%v; 判定依据: 朴素模拟 events=%v err=%v",
				seq, i, opDesc, peer, prefix, attr, now, ev1, errClass(err1), evN, errClass(errN))

			if errClass(err1) != errClass(errN) {
				t.Fatalf("seq=%d op=%d %s err=%v, 朴素模拟=%v", seq, i, opDesc, err1, errN)
			}
			if !eventsEqual(ev1, evN) {
				t.Fatalf("seq=%d op=%d %s events=%v, 朴素模拟=%v", seq, i, opDesc, ev1, evN)
			}
			if !eventsEqual(ev1, ev2) || errClass(err1) != errClass(err2) {
				t.Fatalf("seq=%d op=%d %s 重放不一致: %v/%v vs %v/%v", seq, i, opDesc, ev1, err1, ev2, err2)
			}

			// 每操作后全量对照所有路由在当前时刻的惩罚与抑制状态。
			for p := int64(1); p <= 3; p++ {
				for x := int64(1); x <= 4; x++ {
					p1, s1, e1 := d1.Penalty(p, x, now)
					pN, sN, eN := sim.penalty(p, x, now)
					if errClass(e1) != errClass(eN) || p1 != pN || s1 != sN {
						t.Fatalf("seq=%d op=%d 后 Penalty(%d,%d,%d)=(%d,%v,%v), 朴素模拟=(%d,%v,%v)",
							seq, i, p, x, now, p1, s1, e1, pN, sN, eN)
					}
				}
			}
		}
	}
}
