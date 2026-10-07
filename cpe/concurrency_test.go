package cpe

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// 并发等价：多 goroutine 并发调用，结果须等价于某个串行顺序。
// 验证方式：审计日志记录了锁内实际串行顺序与每步结果，
// 按该顺序在全新服务上重放，逐步结果与最终状态必须完全一致。
func TestConcurrentEquivalence(t *testing.T) {
	cfg := testCfg()
	s := newTestService(t, cfg)
	holders := []string{"h0", "h1", "h2", "h3"}
	for i, h := range holders {
		mustHolder(t, s, h, 0, i)
	}
	orgs := []string{"o0", "o1", "o2"}
	var nowGen int64 = 10
	const workers = 8
	const opsPerWorker = 250
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w)*1000 + 7))
			var myRecs []string
			for i := 0; i < opsPerWorker; i++ {
				// 多个 goroutine 可能取到相同 now，后到的会因时钟回退被拒。
				now := int(atomic.AddInt64(&nowGen, 1)) / 2
				h := holders[rng.Intn(len(holders))]
				switch rng.Intn(10) {
				case 0, 1:
					if len(myRecs) > 0 {
						rec := myRecs[rng.Intn(len(myRecs))]
						_ = s.CorrectCredit(h, rec, orgs[rng.Intn(len(orgs))], 1+rng.Intn(5), now)
						continue
					}
					fallthrough
				case 2:
					if len(myRecs) > 0 {
						rec := myRecs[rng.Intn(len(myRecs))]
						_ = s.RevokeCredit(h, rec, orgs[rng.Intn(len(orgs))], now)
						continue
					}
					fallthrough
				case 3:
					_, _ = s.GetAccounting(h)
					_, _ = s.GetAccountingAt(h, rng.Intn(now+1))
				default:
					earned := now - rng.Intn(5)
					if earned < 0 {
						earned = 0
					}
					id, err := s.RegisterCredit(h, Category(rng.Intn(3)), 1+rng.Intn(5), earned,
						orgs[rng.Intn(len(orgs))], now)
					if err == nil {
						myRecs = append(myRecs, id)
					}
				}
			}
		}(w)
	}
	wg.Wait()
	log := s.AuditLog()
	for i, ev := range log {
		if ev.Seq != i {
			t.Fatalf("audit log seq gap at %d: got %d", i, ev.Seq)
		}
	}
	t.Logf("total ops logged: %d", len(log))
	// 按审计日志的串行顺序重放，逐步比对结果。
	replayed := newTestService(t, cfg)
	for _, ev := range log {
		switch ev.Op {
		case "RegisterHolder":
			err := replayed.RegisterHolder(ev.HolderID, ev.IssueDate, ev.Now)
			if ErrKindOf(err) != ev.Err {
				t.Fatalf("seq %d %s: err %v, want %s", ev.Seq, ev.Op, err, ev.Err)
			}
		case "RegisterCredit":
			id, err := replayed.RegisterCredit(ev.HolderID, ev.Category, ev.Credits, ev.EarnedDate, ev.Org, ev.Now)
			if ErrKindOf(err) != ev.Err || id != ev.ResultRecordID {
				t.Fatalf("seq %d %s: got (%q,%v), want (%q,%s)", ev.Seq, ev.Op, id, err, ev.ResultRecordID, ev.Err)
			}
		case "CorrectCredit":
			err := replayed.CorrectCredit(ev.HolderID, ev.RecordID, ev.Org, ev.NewCredits, ev.Now)
			if ErrKindOf(err) != ev.Err {
				t.Fatalf("seq %d %s: err %v, want %s", ev.Seq, ev.Op, err, ev.Err)
			}
		case "RevokeCredit":
			err := replayed.RevokeCredit(ev.HolderID, ev.RecordID, ev.Org, ev.Now)
			if ErrKindOf(err) != ev.Err {
				t.Fatalf("seq %d %s: err %v, want %s", ev.Seq, ev.Op, err, ev.Err)
			}
		}
	}
	// 最终状态一致。
	for _, h := range holders {
		v1, err1 := s.GetAccounting(h)
		v2, err2 := replayed.GetAccounting(h)
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("holder %s: err mismatch %v vs %v", h, err1, err2)
		}
		if err1 == nil && !viewsEqual(v1, v2) {
			t.Fatalf("holder %s final view mismatch:\nconcurrent:\n%s\nreplayed:\n%s", h, v1, v2)
		}
	}
}

// 相同操作序列重放得到完全相同的结果（确定性）。
func TestDeterministicReplay(t *testing.T) {
	cfg := testCfg()
	run := func() []View {
		s := newTestService(t, cfg)
		mustHolder(t, s, "h", 0, 0)
		mustCredit(t, s, "h", Mandatory, 5, 0, "o", 0)
		mustCredit(t, s, "h", Elective, 9, 1, "o", 1)
		mustCredit(t, s, "h", Online, 7, 2, "o", 2)
		rec := mustCredit(t, s, "h", Mandatory, 2, 3, "o", 3)
		if err := s.CorrectCredit("h", rec, "o", 4, 4); err != nil {
			t.Fatal(err)
		}
		tickTo(t, s, 10)
		mustCredit(t, s, "h", Online, 3, 10, "o", 10)
		v1 := viewOf(t, s, "h")
		v2, err := s.GetAccountingAt("h", 5)
		if err != nil {
			t.Fatal(err)
		}
		return []View{v1, v2}
	}
	a, b := run(), run()
	for i := range a {
		if !viewsEqual(a[i], b[i]) {
			t.Fatalf("replay divergence at view %d:\n%s\n%s", i, a[i], b[i])
		}
	}
}
