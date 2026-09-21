package fsm

import "testing"

// 回归：Log() 曾把内部切片直接交给调用方，外部改动会污染机器状态。
// 根因：Log 直接 return m.log，未做拷贝。
func TestRegressionLogReturnsCopy(t *testing.T) {
	m := newTestMachine(t)
	for _, e := range []Event{"connect", "ping"} {
		if _, err := m.Fire(e); err != nil {
			t.Fatal(err)
		}
	}

	log := m.Log()
	log[0] = Transition{From: "x", Event: "y", To: "z"}
	log = append(log, Transition{From: "a", Event: "b", To: "c"})

	again := m.Log()
	if len(again) != 2 {
		t.Fatalf("log len = %d, want 2", len(again))
	}
	want := Transition{From: "idle", Event: "connect", To: "active"}
	if again[0] != want {
		t.Fatalf("log[0] = %+v, want %+v", again[0], want)
	}
}
