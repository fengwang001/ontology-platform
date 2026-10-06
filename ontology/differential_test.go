package ffm

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"testing"
)

// 随机差分：同一条随机操作序列分别喂给生产实现与独立朴素模型，
// 逐步比对错误类别、返回结果与查询快照；任何分歧立即打印完整判定日志后失败。

type diffLogger struct{ lines []string }

type testTrack struct {
	now     int64
	segIDs  []string
	recIDs  []string
	nextSeq int
	opened  bool
}

func (l *diffLogger) logf(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *diffLogger) dump(t *testing.T) {
	t.Helper()
	for _, s := range l.lines {
		t.Log(s)
	}
}

func diffConfigs(r *rand.Rand) Config {
	return Config{
		RetroWindow:      int64(r.Intn(60)),
		MinMiles:         int64(r.Intn(300)),
		InactiveDuration: int64(20 + r.Intn(120)),
		CancelFee:        int64(r.Intn(50)),
		PeriodLength:     int64(10 + r.Intn(80)),
		Thresholds:       [3]int64{500, 1500, 3000},
		Bonuses:          [4]int64{0, 10, 25, 60},
	}
}

func samePost(a, b *PostResult) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameRedeem(a, b *RedeemResult) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameCancel(a, b *CancelResult) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func TestRandomDifferential(t *testing.T) {
	seed := timeSeed()
	if v := os.Getenv("DIFF_SEED"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			seed = n
		}
	}
	t.Logf("DIFF_SEED=%d（可用该环境变量复现）", seed)

	const accounts = 3
	const opsPerAccount = 400
	for iter := 0; iter < 40; iter++ {
		r := rand.New(rand.NewSource(seed + int64(iter)))
		cfg := diffConfigs(r)
		sys, err := NewSystem(cfg)
		if err != nil {
			t.Fatalf("sys cfg: %v", err)
		}
		nav, err := NewNaive(cfg)
		if err != nil {
			t.Fatalf("naive cfg: %v", err)
		}
		lg := &diffLogger{}
		lg.logf("iter=%d cfg=%+v", iter, cfg)

		// 每个账户维护单调局部时钟，保证生成序列对两侧都是合法输入。
		tracks := make([]*testTrack, accounts)
		for i := range tracks {
			tracks[i] = &testTrack{now: int64(r.Intn(5))}
		}

		for step := 0; step < accounts*opsPerAccount; step++ {
			ai := r.Intn(accounts)
			tr := tracks[ai]
			acct := fmt.Sprintf("acct%d", ai)
			// 时钟只前进，偶尔跨多个周期以触发空周期降级。
			tr.now += int64(r.Intn(40))

			var codeS, codeN ErrCode
			var desc string
			if !tr.opened {
				if r.Intn(4) != 0 {
					desc = fmt.Sprintf("OpenAccount(%s,t=%d)", acct, tr.now)
					errS := sys.OpenAccount(acct, tr.now)
					errN := nav.OpenAccount(acct, tr.now)
					codeS, codeN = codeOf(errS), codeOf(errN)
					if errS == nil {
						tr.opened = true
					}
				} else {
					desc = fmt.Sprintf("Post-before-open(%s,t=%d)", acct, tr.now)
					_, eS := sys.Post(acct, tr.now, Segment{ID: "x", Distance: 1, Rate: 0, FlightTime: tr.now})
					_, eN := nav.Post(acct, tr.now, Segment{ID: "x", Distance: 1, Rate: 0, FlightTime: tr.now})
					codeS, codeN = codeOf(eS), codeOf(eN)
				}
			} else {
				codeS, codeN, desc = applyRandomOp(t, r, sys, nav, acct, tr)
			}
			lg.logf("step=%d %s => sys=%d naive=%d", step, desc, codeS, codeN)
			if codeS != codeN {
				lg.dump(t)
				t.Fatalf("iter=%d step=%d 错误类别分歧 sys=%d naive=%d (%s)", iter, step, codeS, codeN, desc)
			}
			// 每次操作后双方查询结论必须一致。
			qNow := tr.now
			snapS, eS := sys.Query(acct, qNow)
			snapN, eN := nav.Query(acct, qNow)
			if codeOf(eS) != codeOf(eN) || snapS != snapN {
				lg.logf("QUERY 分歧 at t=%d: sys=%+v(%v) naive=%+v(%v)", qNow, snapS, eS, snapN, eN)
				lg.dump(t)
				t.Fatalf("iter=%d step=%d 查询分歧", iter, step)
			}
		}
	}
}

// applyRandomOp 以同一输入驱动两侧一种操作，比对结果并返回错误码与描述。
func applyRandomOp(t *testing.T, r *rand.Rand, sys *System, nav *NaiveSystem,
	acct string, tr *testTrack,
) (ErrCode, ErrCode, string) {
	t.Helper()
	c := sys.cfg
	kind := r.Intn(100)
	switch {
	case kind < 45: // 入账：多数用窗口内时刻，少量构造超期/重复
		id := fmt.Sprintf("seg%d", tr.nextSeq)
		tr.nextSeq++
		flight := tr.now - int64(r.Intn(int(c.RetroWindow)+10))
		if flight < 0 {
			flight = tr.now
		}
		if r.Intn(8) == 0 && len(tr.segIDs) > 0 {
			id = tr.segIDs[r.Intn(len(tr.segIDs))] // 重复
		} else {
			tr.segIDs = append(tr.segIDs, id)
		}
		rate := int64(r.Intn(301))
		distance := int64(1 + r.Intn(2000))
		if r.Intn(15) == 0 { // 偶尔非法
			rate = int64(301 + r.Intn(10))
		}
		seg := Segment{ID: id, Distance: distance, Rate: rate, FlightTime: flight}
		desc := fmt.Sprintf("Post(%s,t=%d,%+v)", acct, tr.now, seg)
		rs, es := sys.Post(acct, tr.now, seg)
		rn, en := nav.Post(acct, tr.now, seg)
		if !samePost(rs, rn) {
			t.Fatalf("%s 结果分歧 sys=%+v naive=%+v", desc, rs, rn)
		}
		return codeOf(es), codeOf(en), desc
	case kind < 60 && len(tr.segIDs) > 0: // 退票
		id := tr.segIDs[r.Intn(len(tr.segIDs))]
		if r.Intn(5) == 0 {
			id = fmt.Sprintf("ghost-seg-%d", r.Intn(100000)) // 未入账先退票
		}
		desc := fmt.Sprintf("Refund(%s,t=%d,seg=%s)", acct, tr.now, id)
		return codeOf(sys.Refund(acct, id, tr.now)), codeOf(nav.Refund(acct, id, tr.now)), desc
	case kind < 80: // 兑换
		id := fmt.Sprintf("rec%d", tr.nextSeq)
		tr.nextSeq++
		miles := int64(1 + r.Intn(3000))
		if r.Intn(6) == 0 && len(tr.recIDs) > 0 {
			id = tr.recIDs[r.Intn(len(tr.recIDs))] // 重复记录 ID
		} else {
			tr.recIDs = append(tr.recIDs, id)
		}
		desc := fmt.Sprintf("Redeem(%s,t=%d,id=%s,miles=%d)", acct, tr.now, id, miles)
		rs, es := sys.Redeem(acct, id, tr.now, miles)
		rn, en := nav.Redeem(acct, id, tr.now, miles)
		if !sameRedeem(rs, rn) {
			t.Fatalf("%s 结果分歧 sys=%+v naive=%+v", desc, rs, rn)
		}
		return codeOf(es), codeOf(en), desc
	case kind < 92 && len(tr.recIDs) > 0: // 取消
		id := tr.recIDs[r.Intn(len(tr.recIDs))]
		if r.Intn(5) == 0 {
			id = "ghost-rec"
		}
		desc := fmt.Sprintf("Cancel(%s,t=%d,id=%s)", acct, tr.now, id)
		rs, es := sys.CancelRedeem(acct, id, tr.now)
		rn, en := nav.CancelRedeem(acct, id, tr.now)
		if !sameCancel(rs, rn) {
			t.Fatalf("%s 结果分歧 sys=%+v naive=%+v", desc, rs, rn)
		}
		return codeOf(es), codeOf(en), desc
	default: // 解冻
		desc := fmt.Sprintf("Unfreeze(%s,t=%d)", acct, tr.now)
		return codeOf(sys.Unfreeze(acct, tr.now)), codeOf(nav.Unfreeze(acct, tr.now)), desc
	}
}

func timeSeed() int64 {
	// 用纳秒做种子，日志会打印；确定性复现用 DIFF_SEED。
	return nanosecondSeed()
}

func TestConcurrentEquivalence(t *testing.T) {
	// 并发只做弱验证：-race 下多 goroutine 交错操作同一账户不得数据竞争/崩溃；
	// 强等价性（存在等价串行序）由确定性差分测试覆盖。
	cfg := testConfig()
	cfg.InactiveDuration = 1_000_000
	sys, _ := NewSystem(cfg)
	mustOpen(t, sys, "c", 0)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 200; i++ {
				now := int64(g*10000 + i)
				_, _ = sys.Post("c", now, Segment{ID: fmt.Sprintf("g%dk%d", g, i), Distance: int64(1 + r.Intn(100)), Rate: int64(r.Intn(200)), FlightTime: now})
				_, _ = sys.Query("c", now)
			}
		}(g)
	}
	wg.Wait()
}
