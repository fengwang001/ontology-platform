package span

import "testing"

func TestEntryAddAndAggregation(t *testing.T) {
	e := New("t")
	if !e.Add("a", 10, 5, false) {
		t.Fatal("first add must succeed")
	}
	if !e.Add("b", 20, 40, true) {
		t.Fatal("second add must succeed")
	}
	if e.Add("a", 30, 1, false) {
		t.Fatal("duplicate spanID must be rejected without changing state")
	}
	t.Logf("input=dup(a) output=rejected spans=%d lastSeen=%d maxDur=%d err=%v",
		e.Spans(), e.LastSeen(), e.MaxDur(), e.HasError())
	if e.Spans() != 2 || e.LastSeen() != 20 || e.MaxDur() != 40 || !e.HasError() {
		t.Fatalf("unexpected state: %+v", e)
	}
}

func TestEntryNoError(t *testing.T) {
	e := New("t")
	e.Add("s1", 1, 100, false)
	if e.HasError() {
		t.Fatal("no error flag expected")
	}
	if e.MaxDur() != 100 {
		t.Fatalf("maxDur=%d want 100", e.MaxDur())
	}
	t.Logf("input=(s1,100,noerr) output=maxDur=100 hasError=false")
}
