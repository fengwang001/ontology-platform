package netmatch

import "testing"

func TestDbg(t *testing.T) {
	a, e1 := ParseAddress("10.0.0.2")
	p, e2 := ParsePrefix("10.0.0.0/8")
	t.Logf("addr=%v %v prefix=%v %v is4=%v p4=%v contains=%v",
		a, e1, p, e2, a.Is4(), p.Addr().Is4(), Contains(p, a))
}
