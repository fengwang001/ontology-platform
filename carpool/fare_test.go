package carpool

import "testing"

func mkOrder(id string, seq int, pickup, dropoff int64, persons int) *Order {
	return &Order{ID: id, Seq: seq, Pickup: pickup, Dropoff: dropoff, Persons: persons}
}

// 取整差额由该段内下单最早的乘客承担，且每段分摊之和恰好等于基础费用。
func TestRoundingDiffToEarliest(t *testing.T) {
	orders := []*Order{
		mkOrder("a", 0, 0, 10, 1),
		mkOrder("b", 1, 0, 10, 1),
		mkOrder("c", 2, 0, 10, 1),
	}
	raw := computeRawFares(orders, 1, nil)
	// 段基础费用 10，每人 ceil(10/3)=4，差额 2 归最早的 a。
	if raw["a"] != 2 || raw["b"] != 4 || raw["c"] != 4 {
		t.Fatalf("raw = %v, want a=2 b=4 c=4", raw)
	}
	if raw["a"]+raw["b"]+raw["c"] != 10 {
		t.Fatalf("segment sum = %d, want 10", raw["a"]+raw["b"]+raw["c"])
	}
}

// 最早下单订单有多名乘客时，差额只从该订单的总份额中扣除一次。
func TestRoundingDiffWithGroupOrder(t *testing.T) {
	orders := []*Order{
		mkOrder("a", 0, 0, 10, 2),
		mkOrder("b", 1, 0, 10, 1),
	}
	raw := computeRawFares(orders, 1, nil)
	// 段基础费用 10，每人 ceil(10/3)=4，a 承担 2*4-2=6，b 承担 4。
	if raw["a"] != 6 || raw["b"] != 4 {
		t.Fatalf("raw = %v, want a=6 b=4", raw)
	}
}

// 多段行程的分摊：重叠段等分，独占段全额。
func TestMultiSegmentSplit(t *testing.T) {
	orders := []*Order{
		mkOrder("a", 0, 0, 10, 1),
		mkOrder("b", 1, 5, 15, 1),
	}
	raw := computeRawFares(orders, 2, nil)
	// 段 0-5：a 独占 10；段 5-10：各 5；段 10-15：b 独占 10。
	if raw["a"] != 15 || raw["b"] != 15 {
		t.Fatalf("raw = %v, want a=15 b=15", raw)
	}
}

// 无重叠订单时分摊恰等于独行费用。
func TestSplitEqualsSoloFare(t *testing.T) {
	s := mustService(t, Config{StopDuration: 1, TimePerDistance: 1, UnitPrice: 3, CancelFee: 5, MaxActiveOrders: 4})
	mustAddVehicle(t, s, "v", 4, 0, 0)
	mustSubmit(t, s, "a", 0, 10, 1, 0, 100, 1)
	mustSubmit(t, s, "b", 10, 20, 1, 0, 100, 2)
	for _, id := range []string{"a", "b"} {
		view := mustQuery(t, s, id)
		if view.EstimatedFare != 30 || view.Cap != 30 || view.SoloFare != 30 {
			t.Fatalf("order %s view = %+v, want fare=cap=solo=30", id, view)
		}
	}
}

func TestCeilDiv(t *testing.T) {
	cases := []struct{ a, b, want int64 }{
		{10, 3, 4}, {9, 3, 3}, {1, 3, 1}, {0, 5, 0}, {7, 1, 7},
	}
	for _, c := range cases {
		if got := ceilDiv(c.a, c.b); got != c.want {
			t.Fatalf("ceilDiv(%d,%d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
