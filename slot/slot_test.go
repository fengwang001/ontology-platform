package slot

import "testing"

func TestNewInvalid(t *testing.T) {
	for _, cap := range []int{0, -1, 10_001} {
		if _, err := New(cap); err != ErrInvalid {
			t.Fatalf("New(%d) = %v, want ErrInvalid", cap, err)
		}
	}
}

func TestCounter(t *testing.T) {
	c, err := New(2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.Free() != 2 || c.Used() != 0 {
		t.Fatalf("initial Free=%d Used=%d, want 2/0", c.Free(), c.Used())
	}
	if !c.Acquire() || !c.Acquire() {
		t.Fatal("first two Acquire must succeed")
	}
	if c.Acquire() {
		t.Fatal("third Acquire must fail at capacity")
	}
	if c.Free() != 0 || c.Used() != 2 {
		t.Fatalf("full Free=%d Used=%d, want 0/2", c.Free(), c.Used())
	}
	c.Release()
	if c.Free() != 1 || c.Used() != 1 {
		t.Fatalf("after Release Free=%d Used=%d, want 1/1", c.Free(), c.Used())
	}
	c.Release()
	c.Release() // extra release is a no-op
	if c.Free() != 2 || c.Used() != 0 {
		t.Fatalf("after extra Release Free=%d Used=%d, want 2/0", c.Free(), c.Used())
	}
}
