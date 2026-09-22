package journal

import (
	"errors"
	"testing"
)

func TestAppendReadLast(t *testing.T) {
	j := New(0)
	ids := []string{"a", "b", "a", "b", "a"}
	for i, id := range ids {
		r, err := j.Append(Record{InstanceID: id, StepIndex: i % 2, Direction: Forward})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		if r.Seq != int64(i+1) {
			t.Fatalf("seq=%d want %d", r.Seq, i+1)
		}
	}
	if got := len(j.Read("a")); got != 3 {
		t.Fatalf("read a = %d records, want 3", got)
	}
	last, ok := j.Last("b")
	if !ok || last.Seq != 4 {
		t.Fatalf("last b = %+v ok=%v", last, ok)
	}
	if _, ok := j.Last("missing"); ok {
		t.Fatal("Last must report missing instance")
	}
}

func TestLimitIsAtomic(t *testing.T) {
	cases := []struct {
		name    string
		max     int
		appends int
		wantErr bool
		wantLen int
	}{
		{"under limit", 2, 2, false, 2},
		{"over limit", 2, 3, true, 2},
		{"unlimited", 0, 5, false, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := New(tc.max)
			var err error
			for i := 0; i < tc.appends; i++ {
				_, err = j.Append(Record{InstanceID: "x"})
			}
			if tc.wantErr && !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("err=%v want ErrLimitExceeded", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected err %v", err)
			}
			if got := j.Len(); got != tc.wantLen {
				t.Fatalf("len=%d want %d (no half-written records)", got, tc.wantLen)
			}
		})
	}
}
