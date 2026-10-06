package rollout

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// diffTester 在同一事件序列上驱动生产实现与朴素模型，
// 逐步记录输入、输出与判定依据，并比较返回值与全部可观察状态。
type diffTester struct {
	t      *testing.T
	rng    *rand.Rand
	real   *Splitter
	naive  *naiveModel
	cfg    Config
	log    strings.Builder
	stepNo int
}

func (d *diffTester) logf(format string, args ...any) {
	fmt.Fprintf(&d.log, "    %s\n", fmt.Sprintf(format, args...))
}

func (d *diffTester) fail(msg string) {
	d.t.Fatalf("differential mismatch at step %d: %s\n%s", d.stepNo, msg, d.log.String())
}

func (d *diffTester) cmpErr(tag string, got, want error) {
	if (got == nil) != (want == nil) || (got != nil && got.Error() != want.Error()) {
		d.fail(fmt.Sprintf("%s error: real=%v naive=%v", tag, got, want))
	}
}

func (d *diffTester) cmpStates(tag string) {
	rs, ns := d.real.Snapshot(), d.naive.state()
	if rs.Phase != ns.phase {
		d.fail(fmt.Sprintf("%s phase: real=%v naive=%v", tag, rs.Phase, ns.phase))
	}
	if rs.PhaseIndex != ns.idx {
		d.fail(fmt.Sprintf("%s idx: real=%d naive=%d", tag, rs.PhaseIndex, ns.idx))
	}
	if rs.Ratio != ns.ratio {
		d.fail(fmt.Sprintf("%s ratio: real=%d naive=%d", tag, rs.Ratio, ns.ratio))
	}
	if rs.EnteredAtMs != ns.enter {
		d.fail(fmt.Sprintf("%s enter: real=%d naive=%d", tag, rs.EnteredAtMs, ns.enter))
	}
	if rs.FailStreak != ns.fails {
		d.fail(fmt.Sprintf("%s fails: real=%d naive=%d", tag, rs.FailStreak, ns.fails))
	}
	if rs.LastClockMs != ns.lastMs {
		d.fail(fmt.Sprintf("%s clock: real=%d naive=%d", tag, rs.LastClockMs, ns.lastMs))
	}
	if rs.StickyCount != len(ns.sticky) {
		d.fail(fmt.Sprintf("%s sticky count: real=%d naive=%d", tag, rs.StickyCount, len(ns.sticky)))
	}
	// 逐条比较粘性：版本、时刻一致，且 LRU 先后序一致（朴素按 seq 降序）。
	d.real.mu.Lock()
	realOrder := []string{}
	for e := d.real.sticky.ll.Front(); e != nil; e = e.Next() {
		en := e.Value.(*stickyEntry)
		realOrder = append(realOrder, en.id)
		var found bool
		for _, ne := range ns.sticky {
			if ne.id == en.id {
				found = true
				if ne.version != en.version || ne.atMs != en.atMs {
					d.fail(fmt.Sprintf("%s sticky %s: real=(%v@%d) naive=(%v@%d)",
						tag, en.id, en.version, en.atMs, ne.version, ne.atMs))
				}
			}
		}
		if !found {
			d.fail(fmt.Sprintf("%s sticky %s missing in naive", tag, en.id))
		}
	}
	d.real.mu.Unlock()
	if len(realOrder) != len(ns.sticky) {
		d.fail(fmt.Sprintf("%s sticky order length", tag))
	}
	for i := range realOrder {
		if realOrder[i] != ns.sticky[i].id {
			d.fail(fmt.Sprintf("%s sticky LRU order at %d: real=%s naive=%s",
				tag, i, realOrder[i], ns.sticky[i].id))
		}
	}
	// 窗口计数。
	d.real.mu.Lock()
	rw := d.real.win
	d.real.mu.Unlock()
	if rw != ns.win {
		d.fail(fmt.Sprintf("%s window: real=%+v naive=%+v", tag, rw, ns.win))
	}
}

func randomConfig(rng *rand.Rand) Config {
	n := 1 + rng.Intn(4)
	ratios := make([]int, n)
	cur := 0
	for i := range ratios {
		step := 1 + rng.Intn((10000-cur)/(n-i))
		if step <= 0 {
			step = 1
		}
		cur += step
		if cur > 10000 {
			cur = 10000
		}
		ratios[i] = cur
	}
	ratios[n-1] = 10000 // 保证最后阶段全量，便于构造观测
	return Config{
		Ratios:      ratios,
		DwellMs:     int64(rng.Intn(8)),
		MinCanary:   1 + rng.Intn(4),
		ToleranceBP: rng.Intn(3) * 500,
		MaxFailures: 1 + rng.Intn(3),
		StickyMs:    int64(rng.Intn(6)),
		MaxSticky:   rng.Intn(5),
	}
}

func TestDifferentialRandomSequences(t *testing.T) {
	const sequences = 1200
	const eventsPerSequence = 250
	totalLogged := 0
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 42))
		cfg := randomConfig(rng)
		real, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		d := &diffTester{
			t:     t,
			rng:   rng,
			real:  real,
			naive: newNaive(cfg),
			cfg:   cfg,
		}
		fmt.Fprintf(&d.log, "sequence %d cfg=%+v\n", seq, cfg)

		now := int64(0)
		for ev := 0; ev < eventsPerSequence; ev++ {
			d.stepNo++
			// 时间：小概率回退或相等，否则前进。
			switch rng.Intn(10) {
			case 0:
				now -= int64(1 + rng.Intn(5))
			case 1, 2:
				// 保持不变（允许相等）
			default:
				now += int64(rng.Intn(4))
			}
			if now < 0 {
				now = 0
			}
			id := fmt.Sprintf("id-%d", rng.Intn(8))

			switch rng.Intn(7) {
			case 0:
				e1, e2 := real2err(d.real.Start(now)), d.naive.start(now)
				fmt.Fprintf(&d.log, "[%d] start(t=%d) -> real=%v naive=%v\n", ev, now, e1, e2)
				d.cmpErr("start", e1, e2)
			case 1:
				d.real.Reset()
				d.naive.reset()
				// reset 后时间基线清空，后续从当前 now 重新开始合法。
				fmt.Fprintf(&d.log, "[%d] reset()\n", ev)
			case 2:
				e1, e2 := real2err(d.real.Downgrade(now)), d.naive.downgrade(now)
				fmt.Fprintf(&d.log, "[%d] downgrade(t=%d) -> real=%v naive=%v\n", ev, now, e1, e2)
				d.cmpErr("downgrade", e1, e2)
			case 3:
				var v Version
				if rng.Intn(2) == 0 {
					v = VersionCanary
				} else {
					v = VersionStable
				}
				success := rng.Intn(100) >= 40
				e1, e2 := d.real.Observe(v, success, now), d.naive.observe(v, success, now)
				fmt.Fprintf(&d.log, "[%d] observe(v=%s,ok=%v,t=%d) -> real=%v naive=%v\n",
					ev, v, success, now, e1, e2)
				d.cmpErr("observe", e1, e2)
			case 4:
				// 小概率传入非法版本，触发参数非法分支。
				v := Version(rng.Intn(3))
				e1, e2 := d.real.Observe(v, true, now), d.naive.observe(v, true, now)
				fmt.Fprintf(&d.log, "[%d] observeRaw(v=%d,t=%d) -> real=%v naive=%v\n",
					ev, v, now, e1, e2)
				d.cmpErr("observeRaw", e1, e2)
			case 5:
				r1, e1 := d.real.Evaluate(now)
				r2, e2 := d.naive.evaluate(now)
				fmt.Fprintf(&d.log, "[%d] evaluate(t=%d) -> real=(%s,%v) naive=(%s,%v)\n",
					ev, now, r1, e1, r2, e2)
				d.cmpErr("evaluate", e1, e2)
				if r1 != r2 {
					d.fail(fmt.Sprintf("evaluate result: real=%s naive=%s", r1, r2))
				}
			default:
				// 独立位置对照也必须一致。
				if d.real.Position(id) != naivePosition(id) {
					d.fail("position diverged")
				}
				r1, e1 := d.real.Route(id, now)
				r2, e2 := d.naive.route(id, now)
				fmt.Fprintf(&d.log, "[%d] route(id=%s,t=%d,pos=%d) -> real=(%s/%s,%v) naive=(%s/%s,%v)\n",
					ev, id, now, naivePosition(id), r1.Version, r1.Source, e1,
					r2.Version, r2.Source, e2)
				d.cmpErr("route", e1, e2)
				if r1 != r2 {
					d.fail(fmt.Sprintf("route result: real=%+v naive=%+v", r1, r2))
				}
			}
			d.cmpStates("post")
		}
		// 每个序列都打印日志（-v 可见），默认打印前 3 个序列到测试输出。
		if seq < 3 {
			t.Log("\n" + d.log.String())
		}
		totalLogged += d.log.Len()
	}
	t.Logf("differential: %d sequences x %d events, log bytes=%d",
		sequences, eventsPerSequence, totalLogged)
}

func real2err(err error) error { return err }
