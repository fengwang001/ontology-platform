package approval

import (
	"errors"
	"strconv"
	"sync"
	"testing"

	"ontology/deadline"
	"ontology/org"
)

func TestErrorsClockAndClosed(t *testing.T) {
	e, o := newTestEngine(t, 10)
	buildChain(t, o, []string{"A", "B"})
	mustLimit(t, o, "B", 100)
	if err := e.Submit("r", "A", 50, 5); err != nil {
		t.Fatal(err)
	}
	if err := e.Submit("r", "A", 50, 4); !errors.Is(err, ErrClock) {
		t.Fatalf("clock=%v want ErrClock", err)
	}
	if err := e.Decide("r", "B", false, 5); err != nil { // Rejected@5
		t.Fatal(err)
	}
	if err := e.Decide("r", "B", true, 6); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed=%v want ErrClosed", err)
	}
	if _, err := e.Status("nope", 5); !errors.Is(err, ErrNotFound) {
		t.Fatalf("status=%v want ErrNotFound", err)
	}
	if err := e.Submit("r", "A", 50, 5); !errors.Is(err, ErrExist) {
		t.Fatalf("dup=%v want ErrExist", err)
	}
	if err := e.Submit("noapp", "A", 999, 5); !errors.Is(err, ErrNoApprover) {
		t.Fatalf("noapp=%v want ErrNoApprover", err)
	}
	if _, err := New(org.New(), 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("T range=%v want ErrInvalid", err)
	}
}

func TestOrgCycles(t *testing.T) {
	o := org.New()
	if err := o.SetManager("A", "A"); !errors.Is(err, org.ErrInvalid) {
		t.Fatalf("self=%v want ErrInvalid", err)
	}
	for _, x := range [][2]string{{"A", "B"}, {"B", "C"}, {"C", "D"}} {
		if err := o.SetManager(x[0], x[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := o.SetManager("D", "A"); !errors.Is(err, org.ErrCycle) {
		t.Fatalf("cycle=%v want ErrCycle", err)
	}
	if err := o.SetManager("D", "B"); !errors.Is(err, org.ErrCycle) {
		t.Fatalf("cycle2=%v want ErrCycle", err)
	}
	if err := o.SetManager("D", "C"); !errors.Is(err, org.ErrCycle) {
		t.Fatalf("self after traverse=%v want ErrCycle", err)
	}
	if err := o.SetLimit("", 1); !errors.Is(err, org.ErrInvalid) {
		t.Fatalf("empty name=%v want ErrInvalid", err)
	}
	if err := o.SetLimit("Z", 1_000_000_000_001); !errors.Is(err, org.ErrInvalid) {
		t.Fatalf("limit range=%v want ErrInvalid", err)
	}
	if err := o.SetManager("", "B"); !errors.Is(err, org.ErrInvalid) {
		t.Fatalf("empty employee=%v want ErrInvalid", err)
	}
}

func TestHeapOrder(t *testing.T) {
	h := deadline.New()
	h.Push(deadline.Item{Req: "a", DueAt: 30})
	h.Push(deadline.Item{Req: "b", DueAt: 10})
	h.Push(deadline.Item{Req: "c", DueAt: 20})
	h.Push(deadline.Item{Req: "b", DueAt: 40}) // 替换而非重复
	if h.Len() != 3 {
		t.Fatalf("len=%d want 3", h.Len())
	}
	h.Delete("c")
	it, _ := h.Pop()
	if it.Req != "a" || it.DueAt != 30 {
		t.Fatalf("pop=%+v want a@30", it)
	}
	it, _ = h.Pop()
	if it.Req != "b" || it.DueAt != 40 {
		t.Fatalf("pop=%+v want b@40", it)
	}
	if _, ok := h.Pop(); ok {
		t.Fatal("heap should be empty")
	}
}

// 堆考察计数：1 个与 10000 个无关待决申请两档；0 次升级时只考察 1 个堆项。
func TestHeapExaminedBound(t *testing.T) {
	for _, n := range []int{1, 10000} {
		e, o := newTestEngine(t, 10)
		buildChain(t, o, []string{"A", "B", "C"})
		mustLimit(t, o, "B", 100)
		mustLimit(t, o, "C", 100)
		if err := e.Submit("r0", "A", 50, 0); err != nil {
			t.Fatal(err)
		}
		for i := 1; i < n; i++ { // 同刻提交，到期点同为 10 > 5
			if err := e.Submit("other"+strconv.Itoa(i), "A", 50, 0); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.Decide("r0", "B", true, 5); err != nil {
			t.Fatal(err)
		}
		if e.examined != 1 {
			t.Fatalf("n=%d examined=%d want 1", n, e.examined)
		}
		t.Logf("待决申请=%d、0 次升级时堆考察数=%d", n, e.examined)
	}
}

func TestConcurrentLimitDecide(t *testing.T) {
	e, o := newTestEngine(t, 100)
	buildChain(t, o, []string{"A", "B", "C"})
	mustLimit(t, o, "B", 100)
	mustLimit(t, o, "C", 100)
	const N = 200
	var wg sync.WaitGroup
	reqs := make([]string, N)
	for i := 0; i < N; i++ {
		req := "q" + strconv.Itoa(i)
		reqs[i] = req
		if err := e.Submit(req, "A", 50, 0); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < N; i++ {
		req := reqs[i]
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = o.SetLimit("B", 0)
			_ = o.SetLimit("B", 100)
		}()
		go func(r string) {
			defer wg.Done()
			switch err := e.Decide(r, "B", true, 50); {
			case err == nil, errors.Is(err, ErrRevoked):
				// 串行交错的两种合法结果
			default:
				t.Errorf("unexpected err %v", err)
			}
		}(req)
	}
	wg.Wait()
}

func mustLimit(t *testing.T, o *org.Org, name string, v int64) {
	t.Helper()
	if err := o.SetLimit(name, v); err != nil {
		t.Fatalf("SetLimit(%s,%d): %v", name, v, err)
	}
}

func buildChain(t *testing.T, o *org.Org, chain []string) {
	t.Helper()
	for i := 0; i+1 < len(chain); i++ {
		if err := o.SetManager(chain[i], chain[i+1]); err != nil {
			t.Fatalf("SetManager(%s,%s): %v", chain[i], chain[i+1], err)
		}
	}
}

func check(t *testing.T, st Status, err error, oc Outcome, who string, ta, finalAt int64) {
	t.Helper()
	if err != nil || st.Outcome != oc || st.Assignee != who ||
		st.AssignedAt != ta || st.FinalAt != finalAt {
		t.Fatalf("status=%+v err=%v; want %s %s ta=%d final=%d",
			st, err, oc, who, ta, finalAt)
	}
}
