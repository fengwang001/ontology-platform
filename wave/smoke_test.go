package wave_test

import (
	"testing"

	"ontology/slot"
	"ontology/wave"
)

func setupExample(t *testing.T) *wave.Coordinator {
	t.Helper()
	c := wave.New()
	must(t, c.SetPallet("x", 10))
	must(t, c.PutStock("B1", slot.Bulk, "x", 25))
	must(t, c.PutStock("B2", slot.Bulk, "x", 10))
	must(t, c.PutStock("K1", slot.Pick, "x", 4))
	must(t, c.PutStock("K2", slot.Pick, "x", 3))
	return c
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func allocMap(t *testing.T, c *wave.Coordinator, order string) map[string]int64 {
	t.Helper()
	rows, err := c.OrderAllocs(order)
	must(t, err)
	m := map[string]int64{}
	for _, a := range rows {
		m[a.Loc] = a.Qty
	}
	return m
}

func TestSmokeExample28(t *testing.T) {
	c := setupExample(t)
	must(t, c.AddOrder("O1", 5, []wave.Line{{SKU: "x", Qty: 28}}))
	res, err := c.Release([]string{"O1"})
	must(t, err)
	if res[0].State != wave.StatusAllocated {
		t.Fatalf("state=%v err=%v", res[0].State, res[0].Err)
	}
	got := allocMap(t, c, "O1")
	want := map[string]int64{"B1": 21, "K1": 4, "K2": 3}
	if len(got) != len(want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("loc %s: got %d want %d (%v)", k, got[k], v, got)
		}
	}
}

func TestSmokeExample30(t *testing.T) {
	c := setupExample(t)
	must(t, c.AddOrder("O1", 5, []wave.Line{{SKU: "x", Qty: 30}}))
	res, err := c.Release([]string{"O1"})
	must(t, err)
	if res[0].State != wave.StatusAllocated {
		t.Fatalf("state=%v", res[0].State)
	}
	got := allocMap(t, c, "O1")
	want := map[string]int64{"B1": 20, "B2": 10}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("loc %s: got %d want %d (%v)", k, got[k], v, got)
		}
	}
}

func TestSmokeExample9(t *testing.T) {
	c := setupExample(t)
	must(t, c.AddOrder("O1", 5, []wave.Line{{SKU: "x", Qty: 9}}))
	res, err := c.Release([]string{"O1"})
	must(t, err)
	if res[0].State != wave.StatusAllocated {
		t.Fatalf("state=%v", res[0].State)
	}
	got := allocMap(t, c, "O1")
	want := map[string]int64{"K1": 4, "K2": 3, "B2": 2}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("loc %s: got %d want %d (%v)", k, got[k], v, got)
		}
	}
}

func TestSmokeShortPick(t *testing.T) {
	c := setupExample(t)
	must(t, c.AddOrder("O1", 5, []wave.Line{{SKU: "x", Qty: 28}}))
	must(t, c.AddOrder("O2", 1, []wave.Line{{SKU: "x", Qty: 5}}))
	if _, err := c.Release([]string{"O1", "O2"}); err != nil {
		t.Fatal(err)
	}
	must(t, c.ShortPick("O1", "K1", 1))
	got := allocMap(t, c, "O1")
	if got["B2"] != 3 {
		t.Fatalf("O1 B2 = %d, want 3, all=%v", got["B2"], got)
	}
	if _, ok := got["K1"]; ok {
		t.Fatalf("K1 should be gone: %v", got)
	}
	st, short, err := c.Status("O1")
	must(t, err)
	if st != wave.StatusAllocated || short != 0 {
		t.Fatalf("O1 state=%v short=%d", st, short)
	}
	l, _ := c.LocView("K1")
	if !l.Locked || l.OnHand != 3 {
		t.Fatalf("K1 locked=%v onHand=%d", l.Locked, l.OnHand)
	}
}
