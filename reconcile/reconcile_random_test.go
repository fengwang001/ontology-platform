package reconcile

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// 朴素模拟：按规则逐事件写成的对照实现，不追求效率，
// 每批都显式校验、显式排序、显式预检水位、逐事件判定并记录判定依据。

type naivePair struct {
	wm         int64
	hasWm      bool
	anchor     int64
	hasAnchor  bool
	attributed bool
	e          int64
	hasE       bool
}

type naiveUser struct {
	u    int64
	hasU bool
}

type naiveReconciler struct {
	v, c, a, tmin, lk, tu int64
	pairs                 map[[2]string]*naivePair
	users                 map[string]*naiveUser
	stats                 Stats
	logf                  func(format string, args ...any)
}

func newNaive(v, c, a, tmin, lk, tu int64, logf func(string, ...any)) *naiveReconciler {
	return &naiveReconciler{
		v: v, c: c, a: a, tmin: tmin, lk: lk, tu: tu,
		pairs: make(map[[2]string]*naivePair),
		users: make(map[string]*naiveUser),
		logf:  logf,
	}
}

func (n *naiveReconciler) pairOf(user, ad string) *naivePair {
	k := [2]string{user, ad}
	p := n.pairs[k]
	if p == nil {
		p = &naivePair{}
		n.pairs[k] = p
	}
	return p
}

// submit 返回与排序后事件一一对应的结论；拒绝时返回错误。
func (n *naiveReconciler) submit(events []Event) ([]Outcome, error) {
	// 1. 参数校验。
	for _, ev := range events {
		if ev.Kind != Impression && ev.Kind != Click {
			return nil, ErrInvalidParam
		}
		if ev.User == "" || ev.Ad == "" {
			return nil, ErrInvalidParam
		}
		if ev.T < 0 || ev.T > 1_000_000_000_000_000 {
			return nil, ErrInvalidParam
		}
		if ev.Kind == Impression && (ev.V < 0 || ev.V > 1_000_000_000) {
			return nil, ErrInvalidParam
		}
	}
	// 2. 稳定排序：t 升序，同刻曝光先于点击，再按输入次序。
	idx := make([]int, len(events))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool {
		a, b := events[idx[i]], events[idx[j]]
		if a.T != b.T {
			return a.T < b.T
		}
		return a.Kind < b.Kind
	})
	// 3. 乱序预检（不修改状态）。
	for _, i := range idx {
		ev := events[i]
		if p, ok := n.pairs[[2]string{ev.User, ev.Ad}]; ok && p.hasWm && ev.T < p.wm {
			n.logf("  拒绝: (%s,%s) t=%d < 水位 %d -> 乱序", ev.User, ev.Ad, ev.T, p.wm)
			return nil, ErrOutOfOrder
		}
	}
	// 4. 逐事件处理。
	outcomes := make([]Outcome, len(events))
	for pos, i := range idx {
		ev := events[i]
		o, why := n.step(ev)
		outcomes[pos] = o
		n.stats.add(o)
		n.logf("  事件 %-12s -> %s（%s）", formatEvent(ev), o, why)
	}
	return outcomes, nil
}

func formatEvent(ev Event) string {
	if ev.Kind == Impression {
		return fmt.Sprintf("曝光(%s,%s,t=%d,v=%d)", ev.User, ev.Ad, ev.T, ev.V)
	}
	return fmt.Sprintf("点击(%s,%s,t=%d)", ev.User, ev.Ad, ev.T)
}

func (n *naiveReconciler) step(ev Event) (Outcome, string) {
	p := n.pairOf(ev.User, ev.Ad)
	if !p.hasWm || ev.T > p.wm {
		p.wm = ev.T
		p.hasWm = true
	}
	if ev.Kind == Impression {
		return n.impression(p, ev)
	}
	return n.click(p, ev)
}

func (n *naiveReconciler) impression(p *naivePair, ev Event) (Outcome, string) {
	if ev.V < n.v {
		return Invisible, fmt.Sprintf("v=%d < V=%d", ev.V, n.v)
	}
	if p.hasAnchor && ev.T-p.anchor < n.c {
		return Cooldown, fmt.Sprintf("t-锚点=%d < C=%d", ev.T-p.anchor, n.c)
	}
	if p.hasE && ev.T < p.e {
		return Cooldown, fmt.Sprintf("t=%d < e=%d", ev.T, p.e)
	}
	p.anchor = ev.T
	p.hasAnchor = true
	p.attributed = false
	return Counted, "通过可见性与冷却检查，锚点移至本曝光"
}

func (n *naiveReconciler) click(p *naivePair, ev Event) (Outcome, string) {
	if !p.hasAnchor {
		return NoImpression, "该对无锚点"
	}
	g := ev.T - p.anchor
	if g > n.a {
		return Expired, fmt.Sprintf("g=%d > A=%d", g, n.a)
	}
	if g < n.tmin {
		return TooFast, fmt.Sprintf("g=%d < Tmin=%d", g, n.tmin)
	}
	if p.attributed {
		return Duplicate, "锚点已被归因"
	}
	if u := n.users[ev.User]; u != nil && u.hasU && ev.T-u.u < n.tu {
		return CrossTalk, fmt.Sprintf("t-u=%d < Tu=%d", ev.T-u.u, n.tu)
	}
	p.attributed = true
	p.e = ev.T + n.lk
	p.hasE = true
	u := n.users[ev.User]
	if u == nil {
		u = &naiveUser{}
		n.users[ev.User] = u
	}
	if !u.hasU || ev.T > u.u {
		u.u = ev.T
	}
	u.hasU = true
	return Attributed, fmt.Sprintf("g=%d 有效，e=%d，u=%d", g, p.e, u.u)
}

// randParams 生成偏向小值的随机参数，提高边界碰撞概率。
func randParams(rng *rand.Rand) [6]int64 {
	pick := func() int64 {
		switch rng.Intn(10) {
		case 0, 1:
			return 0
		case 2, 3, 4:
			return int64(rng.Intn(5))
		case 5, 6, 7:
			return int64(rng.Intn(30))
		default:
			return int64(rng.Intn(200))
		}
	}
	return [6]int64{pick(), pick(), pick(), pick(), pick(), pick()}
}

// TestRandomAgainstNaive 2000 组随机批序列与朴素模拟对照，
// 日志打印输入、输出与判定依据。
func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261004))
	users := []string{"u0", "u1", "u2"}
	ads := []string{"a0", "a1", "a2"}

	for seq := 0; seq < 2000; seq++ {
		p := randParams(rng)
		r, err := New(p[0], p[1], p[2], p[3], p[4], p[5])
		if err != nil {
			t.Fatalf("seq %d: New 报错: %v", seq, err)
		}
		logf := func(format string, args ...any) {
			t.Logf("seq %d: "+format, append([]any{seq}, args...)...)
		}
		n := newNaive(p[0], p[1], p[2], p[3], p[4], p[5], logf)
		logf("参数 V=%d C=%d A=%d Tmin=%d Lk=%d Tu=%d", p[0], p[1], p[2], p[3], p[4], p[5])

		var cursor int64
		batches := 1 + rng.Intn(6)
		for b := 0; b < batches; b++ {
			size := rng.Intn(13)
			events := make([]Event, 0, size)
			// 5% 的批故意让 t 低于水位，触发整批拒绝。
			rewind := rng.Intn(20) == 0 && cursor > 100
			base := cursor
			if rewind {
				base = cursor - 100
			}
			for i := 0; i < size; i++ {
				user := users[rng.Intn(len(users))]
				ad := ads[rng.Intn(len(ads))]
				ts := base + int64(rng.Intn(20))
				if rng.Intn(2) == 0 {
					events = append(events, imp(user, ad, ts, int64(rng.Intn(250))))
				} else {
					events = append(events, clk(user, ad, ts))
				}
			}
			// 2% 的批掺入非法事件。
			invalid := rng.Intn(50) == 0 && len(events) > 0
			if invalid {
				events[rng.Intn(len(events))] = imp("", "a", base, 0)
			}

			logf("批 %d 输入 %v", b, events)
			got, gotErr := r.Submit(events)
			want, wantErr := n.submit(events)

			if (gotErr == nil) != (wantErr == nil) {
				t.Fatalf("seq %d 批 %d: 实现 err=%v, 模拟 err=%v", seq, b, gotErr, wantErr)
			}
			if gotErr != nil {
				if !errors.Is(gotErr, wantErr) {
					t.Fatalf("seq %d 批 %d: 实现 err=%v, 模拟 err=%v", seq, b, gotErr, wantErr)
				}
			} else if !reflect.DeepEqual(got, want) {
				t.Fatalf("seq %d 批 %d: 实现=%v, 模拟=%v", seq, b, got, want)
			}
			if gotStats, wantStats := r.Stats(), n.stats; gotStats != wantStats {
				t.Fatalf("seq %d 批 %d: 计数 %+v != 模拟 %+v", seq, b, gotStats, wantStats)
			}
			if !rewind {
				cursor = base + 25
			}
		}

		// 不变量：曝光/点击总数恒等式与归因上界。
		s := r.Stats()
		if s.Attributed > s.Counted {
			t.Fatalf("seq %d: 归因数 %d 超过已计数曝光数 %d", seq, s.Attributed, s.Counted)
		}
	}
}

// TestConcurrent 并发 Submit 与 Stats：不同 user 的批互不影响，
// 结果等价于各 user 批序列各自串行执行。
func TestConcurrent(t *testing.T) {
	const workers = 8
	const batchesPerWorker = 50

	rng := rand.New(rand.NewSource(7))
	allBatches := make([][][]Event, workers)
	for w := 0; w < workers; w++ {
		user := fmt.Sprintf("wu%d", w)
		var cursor int64
		for b := 0; b < batchesPerWorker; b++ {
			size := 1 + rng.Intn(8)
			var events []Event
			for i := 0; i < size; i++ {
				ts := cursor + int64(rng.Intn(20))
				ad := fmt.Sprintf("a%d", rng.Intn(3))
				if rng.Intn(2) == 0 {
					events = append(events, imp(user, ad, ts, int64(rng.Intn(120))))
				} else {
					events = append(events, clk(user, ad, ts))
				}
			}
			allBatches[w] = append(allBatches[w], events)
			cursor += 25
		}
	}

	params := [6]int64{10, 5, 100, 2, 30, 40}
	r, err := New(params[0], params[1], params[2], params[3], params[4], params[5])
	if err != nil {
		t.Fatalf("New 报错: %v", err)
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for _, b := range allBatches[w] {
				if _, err := r.Submit(b); err != nil {
					t.Errorf("Submit 报错: %v", err)
					return
				}
				_ = r.Stats()
			}
		}(w)
	}
	wg.Wait()

	// 各 user 的批序列串行重放，计数之和应等于并发执行的总计数。
	var want Stats
	for w := 0; w < workers; w++ {
		rw, err := New(params[0], params[1], params[2], params[3], params[4], params[5])
		if err != nil {
			t.Fatalf("New 报错: %v", err)
		}
		for _, b := range allBatches[w] {
			if _, err := rw.Submit(b); err != nil {
				t.Fatalf("串行重放报错: %v", err)
			}
		}
		s := rw.Stats()
		want.Counted += s.Counted
		want.Cooldown += s.Cooldown
		want.Invisible += s.Invisible
		want.Attributed += s.Attributed
		want.Duplicate += s.Duplicate
		want.Expired += s.Expired
		want.TooFast += s.TooFast
		want.NoImpression += s.NoImpression
		want.CrossTalk += s.CrossTalk
	}
	if got := r.Stats(); got != want {
		t.Fatalf("并发计数 %+v != 串行重放 %+v", got, want)
	}
}
