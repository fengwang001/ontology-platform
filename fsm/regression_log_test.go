package fsm

import "testing"

// 回归：Log 必须返回副本，调用方的改动不得影响内部状态。
// 根因：Log 直接 return m.log，把内部切片的底层数组泄露给了调用方。
func TestLogReturnsIndependentCopy(t *testing.T) {
	m := newTestMachine(t)
	if _, err := m.Fire("connect"); err != nil {
		t.Fatal(err)
	}

	// 篡改返回值：改写元素并追加，内部日志都不得受影响。
	got := m.Log()
	got[0] = Transition{From: "x", Event: "y", To: "z"}
	_ = append(got, Transition{From: "x", Event: "y", To: "z"})

	if _, err := m.Fire("ping"); err != nil {
		t.Fatal(err)
	}
	log := m.Log()
	want := []Transition{
		{From: "idle", Event: "connect", To: "active"},
		{From: "active", Event: "ping", To: "active"},
	}
	if len(log) != len(want) {
		t.Fatalf("log len = %d, want %d", len(log), len(want))
	}
	for i, tr := range want {
		if log[i] != tr {
			t.Fatalf("log[%d] = %+v, want %+v", i, log[i], tr)
		}
	}
}
