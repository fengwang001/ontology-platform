package dict

import "testing"

func TestAddDedupAndLookup(t *testing.T) {
	d := New(0)
	vals := []int64{5, -3, 5, 0, -3, 42}
	wantCodes := []uint32{0, 1, 0, 2, 1, 3}
	for i, v := range vals {
		c, err := d.Add(v)
		if err != nil {
			t.Fatal(err)
		}
		if c != wantCodes[i] {
			t.Fatalf("Add(%d)=%d want %d", v, c, wantCodes[i])
		}
	}
	if d.Cardinality() != 4 {
		t.Fatalf("cardinality=%d want 4", d.Cardinality())
	}
	for i, v := range []int64{5, -3, 0, 42} {
		got, err := d.Value(uint32(i))
		if err != nil || got != v {
			t.Fatalf("Value(%d)=%d,%v want %d", i, got, err, v)
		}
	}
	if _, err := d.Value(4); err != ErrBadCode {
		t.Fatalf("Value(4) err=%v want ErrBadCode", err)
	}
}

func TestCardinalityLimitAtomic(t *testing.T) {
	d := New(2)
	if _, err := d.Add(1); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Add(2); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Add(3); err != ErrTooManyValues {
		t.Fatalf("err=%v want ErrTooManyValues", err)
	}
	// 超限不得改变已写入状态。
	if d.Cardinality() != 2 || d.Contains(3) {
		t.Fatal("limit violation mutated state")
	}
	if c, err := d.Add(1); err != nil || c != 0 {
		t.Fatalf("re-add existing got %d,%v", c, err)
	}
}

func TestFreezeThawRoundTrip(t *testing.T) {
	d := New(0)
	for _, v := range []int64{-9223372036854775808, 0, 7, 9223372036854775807} {
		if _, err := d.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	buf := d.Freeze()
	d2, consumed, err := Thaw(buf, 0)
	if err != nil {
		t.Fatal(err)
	}
	if consumed != len(buf) {
		t.Fatalf("consumed=%d want %d", consumed, len(buf))
	}
	if d2.Cardinality() != d.Cardinality() {
		t.Fatal("cardinality mismatch")
	}
	for i := 0; i < d.Cardinality(); i++ {
		a, _ := d.Value(uint32(i))
		b, _ := d2.Value(uint32(i))
		if a != b {
			t.Fatalf("code %d: %d != %d", i, a, b)
		}
	}
}

func TestThawTruncated(t *testing.T) {
	d := New(0)
	for _, v := range []int64{10, 20, 30} {
		if _, err := d.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	buf := d.Freeze()
	for cut := 0; cut < len(buf); cut++ {
		if _, _, err := Thaw(buf[:cut], 0); err == nil {
			t.Fatalf("cut=%d expected error", cut)
		}
	}
}

func TestThawCardinalityLimit(t *testing.T) {
	d := New(0)
	for _, v := range []int64{1, 2, 3} {
		if _, err := d.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := Thaw(d.Freeze(), 2); err != ErrTooManyValues {
		t.Fatalf("err=%v want ErrTooManyValues", err)
	}
}
