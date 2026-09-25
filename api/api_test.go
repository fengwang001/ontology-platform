package api_test

import (
	"sync"
	"testing"
	"time"

	"ontology/api"
)

func newSvc(t *testing.T) *api.Service {
	t.Helper()
	s, err := api.New("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var five = []struct {
	ts   int64
	want string
}{
	{1768451400, "2026-01-14"}, {1784089800, "2026-07-15"}, {1772944200, "2026-03-07"},
	{1793507400, "2026-11-01"}, {1773030600, "2026-03-09"},
}

// TestFiveEvents 钉住第三节推导出的五个 Bucket 值。
func TestFiveEvents(t *testing.T) {
	s := newSvc(t)
	for _, c := range five {
		if d, err := s.Bucket(c.ts); err != nil || d != c.want {
			t.Fatalf("Bucket(%d)=%s,%v want %s", c.ts, d, err, c.want)
		}
	}
}

// TestMatchesReferenceAndMonotonic 不变量 1、2：全年逐小时对拍 stdlib 参照且单调。
func TestMatchesReferenceAndMonotonic(t *testing.T) {
	s := newSvc(t)
	loc, _ := time.LoadLocation("America/New_York")
	prev := ""
	for ts := int64(1767225600); ts < 1767225600+366*86400; ts += 3600 {
		d, err := s.Bucket(ts)
		if err != nil {
			t.Fatal(err)
		}
		if ref := time.Unix(ts, 0).In(loc).Format("2006-01-02"); d != ref {
			t.Fatalf("ts=%d: %s != ref %s", ts, d, ref)
		}
		if prev != "" && d < prev {
			t.Fatalf("ts=%d: not monotonic: %s < %s", ts, d, prev)
		}
		prev = d
	}
}

// TestMidnightBoundary 不变量 3：2026 全年每日本地午夜，含 23h/25h 天。
func TestMidnightBoundary(t *testing.T) {
	s := newSvc(t)
	loc, _ := time.LoadLocation("America/New_York")
	for day := time.Date(2026, 1, 1, 0, 0, 0, 0, loc); day.Year() == 2026; day = day.AddDate(0, 0, 1) {
		m := day.Unix()
		if d, _ := s.Bucket(m); d != day.Format("2006-01-02") {
			t.Fatalf("Bucket(midnight %v)=%s", day, d)
		}
		if d, _ := s.Bucket(m - 1); d != day.AddDate(0, 0, -1).Format("2006-01-02") {
			t.Fatalf("Bucket(midnight-1 %v)=%s", day, d)
		}
	}
}

// TestDistinctErrorsAndNoTrace 不变量 4：三类哨兵错误互不相同，被拒后状态不变。
func TestDistinctErrorsAndNoTrace(t *testing.T) {
	s := newSvc(t)
	_, eZ := api.New("Not/AZone")
	_, eT := s.Bucket(-1)
	eK := s.Feed([]api.Event{{EventTime: 1768451400, Key: ""}})
	if eZ != api.ErrBadZone || eT != api.ErrNegativeTS || eK != api.ErrEmptyKey {
		t.Fatalf("sentinels: %v %v %v", eZ, eT, eK)
	}
	if eZ == eT || eT == eK || eZ == eK {
		t.Fatal("sentinel errors must be distinct")
	}
	if err := s.Feed([]api.Event{{EventTime: 1768451400, Key: "ok"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Feed([]api.Event{{EventTime: -1, Key: "x"}, {EventTime: 1768451400, Key: "y"}}); err != api.ErrNegativeTS {
		t.Fatal("batch with bad ts must fail")
	}
	if got := s.Count("2026-01-14"); got != 1 {
		t.Fatalf("state changed after rejection: %d", got)
	}
}

// TestConcurrentReads 不变量：并发 Bucket/Count 结果逐字段相同，穿插并发 Feed。
func TestConcurrentReads(t *testing.T) {
	s := newSvc(t)
	evs := make([]api.Event, 0, len(five))
	for _, c := range five {
		evs = append(evs, api.Event{EventTime: c.ts, Key: "k"})
	}
	if err := s.Feed(evs); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan string, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				if d, _ := s.Bucket(five[i%5].ts); d != five[i%5].want {
					errs <- "bucket mismatch"
				}
				if s.Count("2026-01-14") != 1 {
					errs <- "count mismatch"
				}
				if g == 0 && i == 500 {
					_ = s.Feed([]api.Event{{EventTime: 1773030600, Key: "late"}})
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	if got := s.Count("2026-03-09"); got != 2 {
		t.Fatalf("concurrent Feed lost: %d", got)
	}
}

// TestSelfCheck 自检方法本身必须通过。
func TestSelfCheck(t *testing.T) {
	if err := newSvc(t).SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
