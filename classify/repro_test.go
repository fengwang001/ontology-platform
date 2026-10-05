package classify

import "testing"

func TestReproWhatIf(t *testing.T) {
	e, _ := New(10)
	e.AddColumn("Z", 2, 3)
	e.Raise("Z", 2, 9)
	e.Declass("Z", 1, 16, 15)
	e.Declass("Z", 4, 16, 15)
	w, err := e.WhatIf("Z", 4)
	t.Logf("WhatIf=%v err=%v", w, err)
	c, err := e.SetBase("Z", 4, 15)
	t.Logf("SetBase=%v err=%v", c, err)
	eff, _ := e.Eff("Z")
	t.Logf("Eff=%d", eff)
}
