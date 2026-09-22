package timer

import "testing"

func TestStateMachine(t *testing.T) {
	cases := []struct {
		name  string
		ops   []string // cancel / fire / reset
		want  []bool   // 每个 op 的返回值
		final State
	}{
		{"cancel once", []string{"cancel"}, []bool{true}, Cancelled},
		{"cancel idempotent", []string{"cancel", "cancel"}, []bool{true, false}, Cancelled},
		{"fire once", []string{"fire"}, []bool{true}, Fired},
		{"fire idempotent", []string{"fire", "fire"}, []bool{true, false}, Fired},
		{"cancel after fire", []string{"fire", "cancel"}, []bool{true, false}, Fired},
		{"fire after cancel", []string{"cancel", "fire"}, []bool{true, false}, Cancelled},
		{"reset pending", []string{"reset"}, []bool{true}, Pending},
		{"reset cancelled", []string{"cancel", "reset"}, []bool{true, false}, Cancelled},
		{"reset fired", []string{"fire", "reset"}, []bool{true, false}, Fired},
	}
	for _, c := range cases {
		tm := New(1, 100, nil)
		for i, op := range c.ops {
			var got bool
			switch op {
			case "cancel":
				got = tm.Cancel()
			case "fire":
				got = tm.Fire()
			case "reset":
				got = tm.Reset(2, 200)
			}
			if got != c.want[i] {
				t.Fatalf("%s op %d (%s) = %v, want %v", c.name, i, op, got, c.want[i])
			}
		}
		if tm.State() != c.final {
			t.Fatalf("%s: final = %s, want %s", c.name, tm.State(), c.final)
		}
	}
}

func TestResetReassigns(t *testing.T) {
	tm := New(1, 100, nil)
	if !tm.Reset(7, 300) {
		t.Fatal("Reset on pending should succeed")
	}
	if tm.Seq() != 7 || tm.Deadline() != 300 {
		t.Fatalf("Reset: seq=%d deadline=%d, want 7/300", tm.Seq(), tm.Deadline())
	}
	e := tm.Entry()
	if e.Seq != 7 || e.Deadline != 300 || e.H != tm {
		t.Fatalf("Entry = %+v", e)
	}
}
