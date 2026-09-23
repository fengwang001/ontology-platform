package policy

import "testing"

func TestLookup(t *testing.T) {
	tab := Default()
	cases := []struct {
		name  string
		list  bool
		merge Merge
	}{
		{"content-length", false, Error}, // 名字大小写不敏感
		{"Content-Length", false, Error},
		{"ACCEPT", true, Join},
		{"If-Match", true, Join},
		{"X-Last", false, Last},
		{"X-Multi", false, Join},
		{"X-Unknown", false, First}, // 未登记：fallback
	}
	for _, c := range cases {
		p := tab.Lookup(c.name)
		if p.List != c.list || p.Merge != c.merge {
			t.Errorf("Lookup(%q) = %+v, want List=%v Merge=%v",
				c.name, p, c.list, c.merge)
		}
	}
}

func TestRegisterOverrides(t *testing.T) {
	tab := New(Policy{Merge: First})
	tab.Register("x-Thing", Policy{List: true, Merge: Join})
	if p := tab.Lookup("X-THING"); !p.List || p.Merge != Join {
		t.Errorf("registered policy not found: %+v", p)
	}
	tab.Register("X-Thing", Policy{Merge: Last})
	if p := tab.Lookup("x-thing"); p.Merge != Last {
		t.Errorf("override failed: %+v", p)
	}
}
