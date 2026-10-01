package ontology

import (
	"fmt"
	"math/big"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

func reasonOf(err error) Reason {
	if err == nil {
		return ""
	}
	return err.(*Error).Reason
}

// e 恰为 T*F/1000 时 q1 恰为 N；差一毫秒则为向下取整值。
func TestTimeQuotaBoundary(t *testing.T) {
	s, err := NewScheduler(1000, 1<<20, 500)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Begin(0, 0, 10); err != nil {
		t.Fatal(err)
	}
	q, _, err := s.Tick(499, 0)
	if err != nil || q != 9 {
		t.Fatalf("e=499: q=%d err=%v, want floor=9", q, err)
	}
	q, _, err = s.Tick(500, 0)
	if err != nil || q != 10 {
		t.Fatalf("e=500: q=%d err=%v, want 10", q, err)
	}
}

// 日志进度领先取 q2；时间进度领先取 q1。
func TestMaxOfTwoProgresses(t *testing.T) {
	s, _ := NewScheduler(1000, 1000, 500)
	if err := s.Begin(0, 0, 100); err != nil {
		t.Fatal(err)
	}
	if q, _, err := s.Tick(250, 0); err != nil || q != 50 {
		t.Fatalf("time-ahead: q=%d err=%v, want 50", q, err)
	}
	if q, _, err := s.Tick(250, 400); err != nil || q != 80 {
		t.Fatalf("wal-ahead: q=%d err=%v, want 80", q, err)
	}
}

// 两条进度各自封顶于 N。
func TestQuotaCappedAtN(t *testing.T) {
	s, _ := NewScheduler(10, 10, 1)
	if err := s.Begin(0, 0, 7); err != nil {
		t.Fatal(err)
	}
	q, need, err := s.Tick(1_000_000, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if q != 7 || need != 7 {
		t.Fatalf("q=%d need=%d, want 7/7", q, need)
	}
}

// N=0 立即完成，不进入进行态，完成计数加一。
func TestZeroDirtyCompletesImmediately(t *testing.T) {
	s, _ := NewScheduler(1000, 1000, 500)
	if err := s.Begin(10, 20, 0); err != nil {
		t.Fatal(err)
	}
	st := s.Status()
	if st.Active || st.N != 0 || st.Written != 0 || st.Completed != 1 {
		t.Fatalf("status=%+v, want idle completed=1", st)
	}
	if _, _, err := s.Tick(10, 20); reasonOf(err) != ReasonNoCheckpoint {
		t.Fatalf("tick after N=0: %v, want no_checkpoint", err)
	}
	if err := s.Begin(10, 20, 1); err != nil {
		t.Fatalf("immediate next begin: %v", err)
	}
}

// N=e=2^40、T=F=1，中间乘积约 2^80*1000，不溢出且配额封顶为 N。
func TestHugeValuesNoOverflow(t *testing.T) {
	const huge int64 = 1 << 40
	s, err := NewScheduler(1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Begin(0, 0, huge); err != nil {
		t.Fatal(err)
	}
	q, need, err := s.Tick(huge, huge)
	if err != nil {
		t.Fatal(err)
	}
	if q != huge || need != huge {
		t.Fatalf("q=%d need=%d, want %d", q, need, huge)
	}
}

// Wrote 空闲、非正、超量均被可区分拒绝。
func TestWroteValidation(t *testing.T) {
	s, _ := NewScheduler(1000, 1000, 500)
	if err := s.Wrote(1); reasonOf(err) != ReasonNoCheckpoint {
		t.Fatalf("wrote while idle: %v", err)
	}
	if err := s.Begin(0, 0, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.Wrote(6); reasonOf(err) != ReasonKExceedsRemaining {
		t.Fatalf("wrote 6: %v, want k_exceeds_remaining", err)
	}
	if err := s.Wrote(0); reasonOf(err) != ReasonKNotPositive {
		t.Fatalf("wrote 0: %v, want k_not_positive", err)
	}
	if err := s.Wrote(-3); reasonOf(err) != ReasonKNotPositive {
		t.Fatalf("wrote -3: %v, want k_not_positive", err)
	}
	if err := s.Wrote(5); err != nil {
		t.Fatal(err)
	}
	if err := s.Wrote(1); reasonOf(err) != ReasonNoCheckpoint {
		t.Fatalf("wrote after done: %v, want no_checkpoint", err)
	}
	if st := s.Status(); st.Active || st.Completed != 1 {
		t.Fatalf("status=%+v", st)
	}
}

// now 与 walPos 各自倒退被拒；被拒操作不改变任何状态（含最近时刻）。
func TestRegressionRejectedWithoutStateChange(t *testing.T) {
	s, _ := NewScheduler(1000, 1000, 500)
	if err := s.Begin(100, 50, 10); err != nil {
		t.Fatal(err)
	}
	// e=500 -> q1=10（目标点）；u=50 -> q2=1，取最大 10。
	if q, _, err := s.Tick(600, 100); err != nil || q != 10 {
		t.Fatalf("tick: q=%d err=%v, want 10", q, err)
	}
	if err := s.Wrote(2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Tick(599, 100); reasonOf(err) != ReasonNowRegression {
		t.Fatalf("now regression: %v", err)
	}
	if _, _, err := s.Tick(600, 99); reasonOf(err) != ReasonWalPosRegression {
		t.Fatalf("wal regression: %v", err)
	}
	q, need, err := s.Tick(600, 100)
	if err != nil || q != 10 || need != 8 {
		t.Fatalf("tick after rejected regressions: q=%d need=%d err=%v", q, need, err)
	}
	if err := s.Wrote(8); err != nil {
		t.Fatal(err)
	}
	if err := s.Begin(599, 100, 1); reasonOf(err) != ReasonNowRegression {
		t.Fatalf("begin now regression across checkpoints: %v", err)
	}
	if err := s.Begin(600, 99, 1); reasonOf(err) != ReasonWalPosRegression {
		t.Fatalf("begin wal regression across checkpoints: %v", err)
	}
	if err := s.Begin(-1, 99, 1); reasonOf(err) != ReasonNegativeNow {
		t.Fatalf("begin negative now: %v", err)
	}
	if err := s.Begin(600, -1, 1); reasonOf(err) != ReasonNegativeWalPos {
		t.Fatalf("begin negative wal: %v", err)
	}
	if err := s.Begin(600, 100, -1); reasonOf(err) != ReasonNegativeDirty {
		t.Fatalf("begin negative dirty: %v", err)
	}
	if st := s.Status(); st.Active || st.Completed != 1 {
		t.Fatalf("status=%+v, want idle completed=1", st)
	}
	if err := s.Begin(600, 100, 1); err != nil {
		t.Fatalf("valid begin rejected: %v", err)
	}
}

func TestNewSchedulerValidation(t *testing.T) {
	cases := []struct {
		t, w, f int64
		want    Reason
	}{
		{0, 1, 1, ReasonTNotPositive},
		{-1, 1, 1, ReasonTNotPositive},
		{1, 0, 1, ReasonWNotPositive},
		{1, -1, 1, ReasonWNotPositive},
		{1, 1, 0, ReasonFOutOfRange},
		{1, 1, 1000, ReasonFOutOfRange},
		{1, 1, -5, ReasonFOutOfRange},
	}
	for _, c := range cases {
		if _, err := NewScheduler(c.t, c.w, c.f); reasonOf(err) != c.want {
			t.Fatalf("New(%d,%d,%d)=%v, want %s", c.t, c.w, c.f, err, c.want)
		}
	}
}

// ---- big.Rat 朴素参考模型（严格照抄题目公式）----

type refModel struct {
	t, w, f                     int64
	active                      bool
	startNow, startWal          int64
	n, written                  int64
	lastNow, lastWal, completed int64
}

// ratQuota 用 big.Rat 朴素计算 min(n, floor(n*p*1000/(budget*f)))。
func ratQuota(n, p, budget, f int64) int64 {
	r := new(big.Rat).SetFrac(
		new(big.Int).Mul(new(big.Int).Mul(big.NewInt(n), big.NewInt(p)), big.NewInt(1000)),
		new(big.Int).Mul(big.NewInt(budget), big.NewInt(f)),
	)
	floored := new(big.Int).Quo(r.Num(), r.Denom())
	if floored.Cmp(big.NewInt(n)) >= 0 {
		return n
	}
	return floored.Int64()
}

func (m *refModel) begin(now, walPos, dirty int64) error {
	switch {
	case now < 0:
		return &Error{Op: "Begin", Reason: ReasonNegativeNow}
	case walPos < 0:
		return &Error{Op: "Begin", Reason: ReasonNegativeWalPos}
	case dirty < 0:
		return &Error{Op: "Begin", Reason: ReasonNegativeDirty}
	case m.active:
		return &Error{Op: "Begin", Reason: ReasonCheckpointActive}
	case now < m.lastNow:
		return &Error{Op: "Begin", Reason: ReasonNowRegression}
	case walPos < m.lastWal:
		return &Error{Op: "Begin", Reason: ReasonWalPosRegression}
	}
	m.lastNow, m.lastWal = now, walPos
	m.active = true
	m.startNow, m.startWal, m.n, m.written = now, walPos, dirty, 0
	if dirty == 0 {
		m.active = false
		m.n = 0
		m.completed++
	}
	return nil
}

func (m *refModel) tick(now, walPos int64) (q, need, q1, q2 int64, err error) {
	switch {
	case now < 0:
		return 0, 0, 0, 0, &Error{Op: "Tick", Reason: ReasonNegativeNow}
	case walPos < 0:
		return 0, 0, 0, 0, &Error{Op: "Tick", Reason: ReasonNegativeWalPos}
	case !m.active:
		return 0, 0, 0, 0, &Error{Op: "Tick", Reason: ReasonNoCheckpoint}
	case now < m.lastNow:
		return 0, 0, 0, 0, &Error{Op: "Tick", Reason: ReasonNowRegression}
	case walPos < m.lastWal:
		return 0, 0, 0, 0, &Error{Op: "Tick", Reason: ReasonWalPosRegression}
	}
	m.lastNow, m.lastWal = now, walPos
	e, u := now-m.startNow, walPos-m.startWal
	q1 = ratQuota(m.n, e, m.t, m.f)
	q2 = ratQuota(m.n, u, m.w, m.f)
	q = q1
	if q2 > q1 {
		q = q2
	}
	need = q - m.written
	if need < 0 {
		need = 0
	}
	return q, need, q1, q2, nil
}

func (m *refModel) wrote(k int64) error {
	switch {
	case !m.active:
		return &Error{Op: "Wrote", Reason: ReasonNoCheckpoint}
	case k <= 0:
		return &Error{Op: "Wrote", Reason: ReasonKNotPositive}
	case k > m.n-m.written:
		return &Error{Op: "Wrote", Reason: ReasonKExceedsRemaining}
	}
	m.written += k
	if m.written == m.n {
		m.active = false
		m.n, m.written = 0, 0
		m.completed++
	}
	return nil
}

func (m *refModel) status() StatusInfo {
	return StatusInfo{Active: m.active, N: m.n, Written: m.written, Completed: m.completed}
}

// TestRandomDifferential：2000 组随机操作序列，调度器与 big.Rat 朴素模型逐步对拍，
// 日志记录每条输入、输出与判定依据。
func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	var log strings.Builder
	const seq = 2000

	for iter := 0; iter < seq; iter++ {
		// 四分之一序列采用“大数量级”画像：T、W、F 极小，N/e/u 达 2^40，
		// 使 n*e*1000 的中间乘积远超 int64，对拍精确整数路径。
		hugeProfile := rng.Intn(4) == 0
		var tv, wv, fv, maxN, delta int64
		if hugeProfile {
			tv = int64(1 + rng.Intn(5))
			wv = int64(1 + rng.Intn(5))
			fv = int64(1 + rng.Intn(3))
			maxN = 1 << 40
			delta = 1 << 41
		} else {
			tv = int64(1 + rng.Intn(1000))
			wv = int64(1 + rng.Intn(1000))
			fv = int64(1 + rng.Intn(999))
			maxN = 1 << 20
			delta = 0
		}
		sut, err := NewScheduler(tv, wv, fv)
		if err != nil {
			t.Fatalf("iter %d: unexpected ctor error: %v", iter, err)
		}
		ref := &refModel{t: tv, w: wv, f: fv}
		fmt.Fprintf(&log, "iter %d: New T=%d W=%d F=%d -> ok\n", iter, tv, wv, fv)

		var now, wal int64
		steps := 5 + rng.Intn(46)
		for step := 0; step < steps; step++ {
			switch rng.Intn(4) {
			case 0: // Begin
				dirty := rng.Int63n(maxN + 1)
				if rng.Intn(10) == 0 { // 注入非法输入
					switch rng.Intn(3) {
					case 0:
						now = -1 - rng.Int63n(5)
					case 1:
						wal = -1 - rng.Int63n(5)
					case 2:
						dirty = -1 - rng.Int63n(5)
					}
				}
				got := sut.Begin(now, wal, dirty)
				want := ref.begin(now, wal, dirty)
				fmt.Fprintf(&log, "iter %d step %d: Begin(now=%d wal=%d dirty=%d) -> %s\n",
					iter, step, now, wal, dirty, verdict(got, want))
				if reasonOf(got) != reasonOf(want) {
					t.Fatalf("iter %d step %d Begin(%d,%d,%d): got=%v want=%v\nlog:\n%s",
						iter, step, now, wal, dirty, got, want, log.String())
				}
				// 非法值只用于本次注入；恢复为最后一个合法单调值。
				if now < 0 {
					now = 0
				}
				if wal < 0 {
					wal = 0
				}
			case 1: // Tick
				if !ref.active {
					break
				}
				var e, u int64
				if hugeProfile {
					e = rng.Int63n(delta)
					u = rng.Int63n(delta)
				} else {
					e = int64(rng.Intn(int(tv) * 2))
					u = int64(rng.Intn(int(wv) * 2))
				}
				tn := ref.startNow + e
				tw := ref.startWal + u
				// 偶尔注入倒退或负值。
				if rng.Intn(8) == 0 {
					tn = ref.lastNow - 1 - int64(rng.Intn(3))
				}
				if rng.Intn(8) == 0 {
					tw = ref.lastWal - 1 - int64(rng.Intn(3))
				}
				gq, gn, gerr := sut.Tick(tn, tw)
				wq, wn, rq1, rq2, rerr := ref.tick(tn, tw)
				basis := "time"
				if rq2 > rq1 {
					basis = "wal"
				}
				fmt.Fprintf(&log, "iter %d step %d: Tick(now=%d wal=%d) -> q=%d need=%d err=%s | e=%d u=%d q1=%d q2=%d max=%s\n",
					iter, step, tn, tw, gq, gn, verdict(gerr, rerr), e, u, rq1, rq2, basis)
				if reasonOf(gerr) != reasonOf(rerr) || gq != wq || gn != wn {
					t.Fatalf("iter %d step %d Tick(%d,%d): got=(%d,%d,%v) want=(%d,%d,%v)\nlog:\n%s",
						iter, step, tn, tw, gq, gn, gerr, wq, wn, rerr, log.String())
				}
				if gerr == nil {
					now, wal = tn, tw
				}
			case 2: // Wrote
				var k int64
				if ref.active {
					remaining := ref.n - ref.written
					switch rng.Intn(4) {
					case 0:
						k = remaining + 1 + int64(rng.Intn(3)) // 超量
					case 1:
						k = -int64(1 + rng.Intn(3)) // 非正
					case 2:
						k = 0
					default:
						k = 1 + rng.Int63n(remaining+1) // 合法（含恰好写完）
						if k > remaining {
							k = remaining
						}
					}
				} else {
					k = 1
				}
				got := sut.Wrote(k)
				want := ref.wrote(k)
				fmt.Fprintf(&log, "iter %d step %d: Wrote(k=%d) -> %s\n",
					iter, step, k, verdict(got, want))
				if reasonOf(got) != reasonOf(want) {
					t.Fatalf("iter %d step %d Wrote(%d): got=%v want=%v\nlog:\n%s",
						iter, step, k, got, want, log.String())
				}
			case 3: // Status
				gst := sut.Status()
				wst := ref.status()
				fmt.Fprintf(&log, "iter %d step %d: Status() -> active=%t n=%d written=%d completed=%d\n",
					iter, step, gst.Active, gst.N, gst.Written, gst.Completed)
				if gst != wst {
					t.Fatalf("iter %d step %d Status: got=%+v want=%+v\nlog:\n%s",
						iter, step, gst, wst, log.String())
				}
			}
		}
	}
	t.Logf("differential log (%d sequences):\n%s", seq, log.String())
}

func verdict(got, want error) string {
	if reasonOf(got) == reasonOf(want) {
		if got == nil {
			return "ok"
		}
		return "rejected:" + string(reasonOf(got))
	}
	return fmt.Sprintf("MISMATCH(got=%v want=%v)", got, want)
}

// 并发冒烟：多 goroutine 混合调用必须串行等价、无竞态、不 panic。
func TestConcurrentSmoke(t *testing.T) {
	s, err := NewScheduler(1000, 1000, 500)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Begin(0, 0, 1_000_000); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_, _, _ = s.Tick(int64(g*200+j), int64(g*200+j))
				_ = s.Status()
				_ = s.Wrote(0) // 必然被拒，验证并发下状态不被破坏
			}
		}(i)
	}
	wg.Wait()
	st := s.Status()
	if !st.Active || st.N != 1_000_000 || st.Written != 0 {
		t.Fatalf("status=%+v", st)
	}
}
