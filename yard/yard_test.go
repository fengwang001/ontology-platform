package yard

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	"ontology/appt"
)

func exCfg() appt.Config { return appt.Config{S: 20, E: 30, L: 15, Wmax: 60, K: 2} }

func eqAssign(a, b []Assignment) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i].Truck, b[i].Truck) || !bytes.Equal(a[i].Dock, b[i].Dock) {
			return false
		}
	}
	return true
}

func mkAssign(p ...[2]string) []Assignment {
	out := make([]Assignment, len(p))
	for i := range p {
		out[i] = Assignment{Truck: []byte(p[i][0]), Dock: []byte(p[i][1])}
	}
	return out
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// setupExample 复现题面：先建预约（now=0），再加入月台并用 P1/P2/P3 占满。
func setupExample(t *testing.T) *Yard {
	t.Helper()
	y := New(exCfg())
	bookingsExample(t, y)
	if _, err := y.AddDock([]byte("R1"), Reefer, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := y.AddDock([]byte("R2"), Reefer, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := y.AddDock([]byte("D1"), Dry, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := y.CheckIn([]byte("P2"), Reefer, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := y.CheckIn([]byte("P3"), Reefer, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := y.CheckIn([]byte("P1"), Dry, 1); err != nil {
		t.Fatal(err)
	}
	return y
}

func bookingsExample(t *testing.T, y *Yard) {
	t.Helper()
	must(t, y.Book([]byte("T1"), Dry, 100, 0))
	must(t, y.Book([]byte("T2"), Reefer, 120, 0))
	must(t, y.Book([]byte("T3"), Dry, 100, 0))
}

func TestSpecExample1(t *testing.T) {
	y := setupExample(t)
	if _, err := y.CheckIn([]byte("T3"), Dry, 80); err != nil {
		t.Fatal(err)
	}
	if _, err := y.CheckIn([]byte("W1"), Dry, 85); err != nil {
		t.Fatal(err)
	}
	if _, err := y.CheckIn([]byte("T1"), Dry, 116); err != nil { // 迟到降候补，签到仍成功
		t.Fatalf("late check-in still succeeds, got %v", err)
	}
	got, err := y.CheckIn([]byte("T2"), Reefer, 118)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("no free dock yet: %v", got)
	}
	got, err = y.Depart([]byte("P2"), 120)
	if err != nil {
		t.Fatal(err)
	}
	if !eqAssign(got, mkAssign([2]string{"T2", "R1"})) {
		t.Fatalf("want [(T2,R1)], got %v", got)
	}
}

func TestSpecExample2_PromotionAt145(t *testing.T) {
	y := setupExample(t)
	y.CheckIn([]byte("T3"), Dry, 80)
	y.CheckIn([]byte("W1"), Dry, 85)
	y.CheckIn([]byte("T1"), Dry, 116)
	y.CheckIn([]byte("T2"), Reefer, 118)
	y.Depart([]byte("P2"), 120)

	got, _ := y.Depart([]byte("P1"), 145) // W1 已等 60，恰等 Wmax 提升
	if !eqAssign(got, mkAssign([2]string{"W1", "D1"})) {
		t.Fatalf("145: promoted W1 first, got %v", got)
	}
	got, _ = y.Depart([]byte("P3"), 150) // 无冷藏车等待，T3 借 R2
	if !eqAssign(got, mkAssign([2]string{"T3", "R2"})) {
		t.Fatalf("150: T3 borrows R2, got %v", got)
	}
}

func TestSpecExample2_At144(t *testing.T) {
	y := setupExample(t)
	y.CheckIn([]byte("T3"), Dry, 80)
	y.CheckIn([]byte("W1"), Dry, 85)
	y.CheckIn([]byte("T1"), Dry, 116)
	y.CheckIn([]byte("T2"), Reefer, 118)
	y.Depart([]byte("P2"), 120)

	got, _ := y.Depart([]byte("P1"), 144) // W1 等待 59，未提升
	if !eqAssign(got, mkAssign([2]string{"T3", "D1"})) {
		t.Fatalf("144: on-time T3 first, got %v", got)
	}
}

func TestRestartFromHead(t *testing.T) {
	y := New(exCfg())
	if _, err := y.AddDock([]byte("R1"), Reefer, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := y.AddDock([]byte("R2"), Reefer, 0); err != nil {
		t.Fatal(err)
	}
	y.CheckIn([]byte("Q1"), Reefer, 0)
	y.CheckIn([]byte("Q2"), Reefer, 0)
	y.CheckIn([]byte("X"), Dry, 1)
	y.CheckIn([]byte("Y"), Reefer, 2)
	y.CheckIn([]byte("Z"), Dry, 3)

	// 释放 R1：X 因 Y 在等不可借；Y 得 R1。
	got, err := y.Depart([]byte("Q1"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if !eqAssign(got, mkAssign([2]string{"Y", "R1"})) {
		t.Fatalf("want [(Y,R1)], got %v", got)
	}
	// 再释放 R2：队列中已无冷藏车，从头重选后 X 借 R2；Z 等待。
	got, _ = y.Depart([]byte("Q2"), 11)
	if !eqAssign(got, mkAssign([2]string{"X", "R2"})) {
		t.Fatalf("want [(X,R2)], got %v", got)
	}
}

func TestBoundaryClassification(t *testing.T) {
	// s=100：准点窗 [70,115]。P 占住 D1，候补 W 作对照：
	// H 准点则先于 W；H 早到/迟到降候补则 W（序号更早）先派。
	cases := []struct {
		name string
		now  int64
		want bool
	}{
		{"s-E-1 早到 69", 69, false},
		{"s-E 取等 70", 70, true},
		{"s-E+1 71", 71, true},
		{"s 100", 100, true},
		{"s+L-1 114", 114, true},
		{"s+L 取等 115", 115, true},
		{"s+L+1 迟到 116", 116, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			y := New(appt.Config{S: 20, E: 30, L: 15, Wmax: 100000, K: 2})
			if _, err := y.AddDock([]byte("D1"), Dry, 0); err != nil {
				t.Fatal(err)
			}
			must(t, y.Book([]byte("H"), Dry, 100, 0))
			y.CheckIn([]byte("P"), Dry, 1)
			y.CheckIn([]byte("W"), Dry, 2)
			if _, err := y.CheckIn([]byte("H"), Dry, tc.now); err != nil {
				t.Fatal(err)
			}
			got, _ := y.Depart([]byte("P"), tc.now+1)
			first := string(got[0].Truck)
			if (first == "H") != tc.want {
				t.Fatalf("now=%d want ontime=%v, got first=%s list=%v", tc.now, tc.want, first, got)
			}
		})
	}
}

func TestOntimeOrderBySNotCheckIn(t *testing.T) {
	y := New(exCfg())
	if _, err := y.AddDock([]byte("D1"), Dry, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := y.AddDock([]byte("D2"), Dry, 0); err != nil {
		t.Fatal(err)
	}
	y.CheckIn([]byte("P1"), Dry, 0)
	y.CheckIn([]byte("P2"), Dry, 0)
	must(t, y.Book([]byte("A"), Dry, 100, 0))
	must(t, y.Book([]byte("B"), Dry, 80, 0))
	y.CheckIn([]byte("A"), Dry, 90) // s 更大却先签到
	y.CheckIn([]byte("B"), Dry, 91)
	got, _ := y.Depart([]byte("P1"), 100)
	if !eqAssign(got, mkAssign([2]string{"B", "D1"})) {
		t.Fatalf("smaller s first regardless of check-in time, got %v", got)
	}
}

func TestRejectionOrder(t *testing.T) {
	y := New(exCfg())
	if _, err := y.AddDock([]byte("D1"), Dry, 10); err != nil {
		t.Fatal(err)
	}

	if _, err := y.AddDock(nil, Dry, 5); !errors.Is(err, appt.ErrInvalid) {
		t.Fatalf("invalid > clock, got %v", err)
	}
	if _, err := y.AddDock([]byte("D1"), Dry, 5); !errors.Is(err, appt.ErrClock) {
		t.Fatalf("clock > duplicate, got %v", err)
	}
	if _, err := y.AddDock([]byte("D1"), Dry, 10); !errors.Is(err, appt.ErrDuplicate) {
		t.Fatalf("duplicate, got %v", err)
	}
	if _, err := y.Depart([]byte("ghost"), 10); !errors.Is(err, appt.ErrNotFound) {
		t.Fatalf("depart unknown = not found, got %v", err)
	}
	y.CheckIn([]byte("W"), Dry, 11)
	if _, err := y.Depart([]byte("W"), 11); err != nil {
		t.Fatal(err)
	}
	if _, err := y.Depart([]byte("W"), 12); !errors.Is(err, appt.ErrState) {
		t.Fatalf("depart departed = state, got %v", err)
	}
	y.CheckIn([]byte("V"), Dry, 13)
	if _, err := y.CheckIn([]byte("V"), Dry, 13); !errors.Is(err, appt.ErrState) {
		t.Fatalf("repeat checkin = state, got %v", err)
	}
	must(t, y.Book([]byte("R"), Reefer, 140, 13))
	if _, err := y.CheckIn([]byte("R"), Dry, 100); !errors.Is(err, appt.ErrState) {
		t.Fatalf("kind mismatch = state, got %v", err)
	}
	must(t, y.Book([]byte("Q"), Dry, 120, 13))
	if err := y.Book([]byte("Q"), Dry, 160, 10); !errors.Is(err, appt.ErrClock) {
		t.Fatalf("book clock > duplicate, got %v", err)
	}
	if err := y.Book([]byte("Q"), Dry, 160, 13); !errors.Is(err, appt.ErrDuplicate) {
		t.Fatalf("duplicate book, got %v", err)
	}
	for _, id := range []string{"a1", "a2"} {
		must(t, y.Book([]byte(id), Dry, 200, 13))
	}
	if err := y.Book([]byte("a3"), Dry, 200, 13); !errors.Is(err, appt.ErrCapacity) {
		t.Fatalf("capacity full, got %v", err)
	}
}

func TestReplayDeterminism(t *testing.T) {
	run := func() []Assignment {
		y := New(exCfg())
		y.AddDock([]byte("R1"), Reefer, 0)
		y.AddDock([]byte("R2"), Reefer, 0)
		y.AddDock([]byte("D1"), Dry, 0)
		y.CheckIn([]byte("P2"), Reefer, 1)
		y.CheckIn([]byte("P3"), Reefer, 1)
		y.CheckIn([]byte("P1"), Dry, 1)
		y.Book([]byte("T1"), Dry, 100, 0)
		y.Book([]byte("T2"), Reefer, 120, 0)
		y.Book([]byte("T3"), Dry, 100, 0)
		y.CheckIn([]byte("T3"), Dry, 80)
		y.CheckIn([]byte("W1"), Dry, 85)
		y.CheckIn([]byte("T1"), Dry, 116)
		y.CheckIn([]byte("T2"), Reefer, 118)
		g, _ := y.Depart([]byte("P2"), 120)
		g2, _ := y.Depart([]byte("P1"), 145)
		g3, _ := y.Depart([]byte("P3"), 150)
		return append(append(g, g2...), g3...)
	}
	if !eqAssign(run(), run()) {
		t.Fatal("replay nondeterministic")
	}
}

func TestConcurrentSafe(t *testing.T) {
	for i := 0; i < 8; i++ {
		y := New(exCfg())
		var wg sync.WaitGroup
		for _, op := range []struct {
			id string
			k  Kind
		}{{"A", Dry}, {"B", Reefer}, {"C", Dry}, {"D", Reefer}} {
			wg.Add(1)
			go func(id string, k Kind) {
				defer wg.Done()
				_, _ = y.AddDock([]byte(id+"D"), k, 1)
				_, _ = y.CheckIn([]byte(id), k, 2)
			}(op.id, op.k)
		}
		wg.Wait()
	}
}
