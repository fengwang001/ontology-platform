package hold

import (
	"errors"
	"testing"

	"ontology/routing"
)

// 例中路线：n=3，back=[1,2,2]，仅工序 2 检验，R=1。
func newEnv(t *testing.T, y int, nmin int64) (*Manager, *routing.Manager) {
	t.Helper()
	rm := routing.NewManager()
	if err := rm.Define("r3", 3, []int{1, 2, 2}, []bool{false, true, false}, 1); err != nil {
		t.Fatal(err)
	}
	qe := func(op string) bool { return op == "qe" }
	wm, h, err := Config(rm, y, nmin, qe)
	if err != nil {
		t.Fatal(err)
	}
	_ = wm
	return h, rm
}

func TestFirstPassExactThresholds(t *testing.T) {
	// Y=90, Nmin=50。
	h, _ := newEnv(t, 90, 50)
	if err := h.Open("wo", "r3", 100); err != nil {
		t.Fatal(err)
	}
	wm := h.WIP()

	if err := wm.Report("wo", 1, 0, 100, 0, 0); err != nil {
		t.Fatal(err)
	}
	// 样本不足 Nmin：8 良 2 废，80% 也不挂起。
	if err := wm.Report("wo", 2, 0, 8, 2, 0); err != nil {
		t.Fatal(err)
	}
	s, _ := wm.State("wo")
	if s.Held {
		t.Fatal("held below Nmin")
	}
	g, tot, _ := h.FirstPass("wo", 2)
	if g != 8 || tot != 10 {
		t.Fatalf("fp = %d/%d", g, tot)
	}
}

func TestFirstPassExactEqualNotHeld(t *testing.T) {
	h, _ := newEnv(t, 90, 100)
	if err := h.Open("wo", "r3", 100); err != nil {
		t.Fatal(err)
	}
	wm := h.WIP()
	if err := wm.Report("wo", 1, 0, 100, 0, 0); err != nil {
		t.Fatal(err)
	}
	// 90/4/6：fGood=90 fTotal=100，恰等 90% 且样本恰等 Nmin，不挂起。
	if err := wm.Report("wo", 2, 0, 90, 4, 6); err != nil {
		t.Fatal(err)
	}
	s, _ := wm.State("wo")
	if s.Held {
		t.Fatal("held at exact threshold")
	}
	if s.Queue[[2]int{3, 0}] != 90 || s.Scrapped != 4 || s.Queue[[2]int{2, 1}] != 6 {
		t.Fatalf("flow wrong: %+v", s.Queue)
	}
}

func TestTriggerReportStillPosted(t *testing.T) {
	h, _ := newEnv(t, 90, 100)
	if err := h.Open("wo", "r3", 100); err != nil {
		t.Fatal(err)
	}
	wm := h.WIP()
	if err := wm.Report("wo", 1, 0, 100, 0, 0); err != nil {
		t.Fatal(err)
	}
	// 89/5/6：8900 < 9000，挂起，但本次报工照常落账。
	if err := wm.Report("wo", 2, 0, 89, 5, 6); err != nil {
		t.Fatalf("trigger report rejected: %v", err)
	}
	s, _ := wm.State("wo")
	if !s.Held {
		t.Fatal("should be held")
	}
	if s.Queue[[2]int{3, 0}] != 89 || s.Scrapped != 5 || s.Queue[[2]int{2, 1}] != 6 {
		t.Fatalf("trigger report not fully posted: %+v scrap=%d", s.Queue, s.Scrapped)
	}
	// 挂起期间一切变更被拒。
	if err := wm.Report("wo", 3, 0, 1, 0, 0); !errors.Is(err, routing.ErrState) {
		t.Fatalf("report while held: %v", err)
	}
	if err := wm.Split("wo", "wo2", 3, 0, 1); !errors.Is(err, routing.ErrState) {
		t.Fatalf("split while held: %v", err)
	}
	if _, err := wm.Close("wo"); !errors.Is(err, routing.ErrState) {
		t.Fatalf("close while held: %v", err)
	}

	// 非 QE 不能恢复。
	if err := h.Resume("wo", "ops"); !errors.Is(err, routing.ErrForbidden) {
		t.Fatalf("resume by non-QE: %v", err)
	}
	// 未挂起不能 Resume。
	if err := h.Resume("ghost2", "qe"); !errors.Is(err, routing.ErrNotFound) {
		t.Fatalf("resume missing: %v", err)
	}

	// QE 恢复，统计清零。
	if err := h.Resume("wo", "qe"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	s, _ = wm.State("wo")
	if s.Held {
		t.Fatal("still held after resume")
	}
	g, tot, _ := h.FirstPass("wo", 2)
	if g != 0 || tot != 0 {
		t.Fatalf("fp not reset: %d/%d", g, tot)
	}
	// 未挂起再 Resume -> 状态不符。
	if err := h.Resume("wo", "qe"); !errors.Is(err, routing.ErrState) {
		t.Fatalf("resume not-held: %v", err)
	}
}

func TestReworkLayerNotCounted(t *testing.T) {
	// Nmin=10：k=0 层只报 8 良 2 废（80%，样本不足）。
	h, _ := newEnv(t, 90, 10)
	if err := h.Open("wo", "r3", 20); err != nil {
		t.Fatal(err)
	}
	wm := h.WIP()
	// 20 件到检验工序：10 良品，10 返工。
	if err := wm.Report("wo", 1, 0, 0, 0, 20); err != nil {
		t.Fatal(err)
	}
	// 返工件回流到 (1,1)，先送回检验工序的 k=1 层。
	if err := wm.Report("wo", 1, 1, 20, 0, 0); err != nil {
		t.Fatal(err)
	}
	// 等等——上面 20 件全部返工，queue[1][1]=20；k=0 层检验尚无报工。
	// k=1 层在检验工序报 10 良 10 废：良率 50% 但不计首过，不挂起。
	if err := wm.Report("wo", 2, 1, 10, 10, 0); err != nil {
		t.Fatal(err)
	}
	s, _ := wm.State("wo")
	if s.Held {
		t.Fatal("k>0 report must not hold")
	}
	g, tot, _ := h.FirstPass("wo", 2)
	if g != 0 || tot != 0 {
		t.Fatalf("k>0 counted into first pass: %d/%d", g, tot)
	}
}

func TestConfigValidation(t *testing.T) {
	rm := routing.NewManager()
	if _, _, err := Config(rm, 0, 10, func(string) bool { return true }); !errors.Is(err, routing.ErrInvalid) {
		t.Fatalf("Y=0: %v", err)
	}
	if _, _, err := Config(rm, 101, 10, func(string) bool { return true }); !errors.Is(err, routing.ErrInvalid) {
		t.Fatalf("Y=101: %v", err)
	}
	if _, _, err := Config(rm, 50, 0, func(string) bool { return true }); !errors.Is(err, routing.ErrInvalid) {
		t.Fatalf("Nmin=0: %v", err)
	}
	if _, _, err := Config(rm, 50, 10, nil); !errors.Is(err, routing.ErrInvalid) {
		t.Fatalf("nil isQE: %v", err)
	}
}

func TestResumeRejectionOrder(t *testing.T) {
	h, _ := newEnv(t, 90, 10)
	// 参数非法先于无权限。
	if err := h.Resume("", ""); !errors.Is(err, routing.ErrInvalid) {
		t.Fatalf("empty args: %v", err)
	}
	// 无权限先于不存在。
	if err := h.Resume("ghost", "ops"); !errors.Is(err, routing.ErrForbidden) {
		t.Fatalf("forbidden vs missing: %v", err)
	}
}

func TestSplitResetsFirstPass(t *testing.T) {
	h, _ := newEnv(t, 90, 10)
	if err := h.Open("wo", "r3", 20); err != nil {
		t.Fatal(err)
	}
	wm := h.WIP()
	if err := wm.Report("wo", 1, 0, 20, 0, 0); err != nil {
		t.Fatal(err)
	}
	// 检验工序 k=0 报 8 良 2 返工（80%，Nmin=10 满足则会挂起，故先让样本不足：
	// 这里直接报 10 件触发 80% 挂起，再 QE 恢复后拆单）。
	if err := wm.Report("wo", 2, 0, 8, 0, 2); err != nil {
		t.Fatal(err)
	}
	if err := h.Resume("wo", "qe"); err != nil {
		t.Fatal(err)
	}
	if err := wm.Split("wo", "wo2", 3, 0, 8); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := h.FirstPass("wo2", 2); !ok {
		t.Fatal("new wo has no hold state")
	}
	g, tot, _ := h.FirstPass("wo2", 2)
	if g != 0 || tot != 0 {
		t.Fatalf("child fp not zero: %d/%d", g, tot)
	}
	s, _ := wm.State("wo2")
	if s.Held {
		t.Fatal("child born held")
	}
}
