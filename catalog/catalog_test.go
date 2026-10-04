package catalog

import (
	"reflect"
	"testing"
)

func TestCatalogBasics(t *testing.T) {
	c := New(3)
	if !c.Add("a", 2) {
		t.Fatal("first add should succeed")
	}
	if c.Add("a", 1) {
		t.Fatal("duplicate add should fail")
	}
	if c.Len() != 1 || !c.Has("a") || c.Has("b") {
		t.Fatal("Len/Has mismatch")
	}
	c.SetBase("a", 4)
	c.SetFloor("a", 1)
	if got := c.Get("a"); got.Base != 4 || got.Floor != 1 {
		t.Fatalf("SetBase/SetFloor: %+v", got)
	}
	c.SetCap("a", &Cap{Level: 1, Until: 100})
	if c.Len() >= c.Nmax() {
		t.Fatal("capacity accounting")
	}
	// until 恰等于 now 失效；小于等于都清。
	c.Add("b", 0)
	c.SetCap("b", &Cap{Level: 0, Until: 50})
	got := c.ExpireCaps(100)
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("ExpireCaps = %v", got)
	}
	if c.Get("a").Cap != nil || c.Get("b").Cap != nil {
		t.Fatal("caps should be cleared")
	}
	// until > now 保留。
	c.SetCap("a", &Cap{Level: 1, Until: 101})
	if got := c.ExpireCaps(100); len(got) != 0 {
		t.Fatalf("live cap must survive, got %v", got)
	}
}
