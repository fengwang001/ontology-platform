package fsm

import "testing"

// 回归：自转移曾触发 exit/entry。
// 根因：Fire 中动作用 `if true` 包裹，未判断 From == To。
func TestRegressionSelfTransitionSkipsActions(t *testing.T) {
	m := newTestMachine(t)
	if _, err := m.Fire("connect"); err != nil {
		t.Fatal(err)
	}
	var entries, exits int
	m.OnEntry("active", func() error { entries++; return nil })
	m.OnExit("active", func() { exits++ })
	ch := m.Observe()

	for i := 0; i < 3; i++ {
		if s, err := m.Fire("ping"); err != nil || s != "active" {
			t.Fatalf("Fire(ping) = %q, %v", s, err)
		}
	}
	if entries != 0 || exits != 0 {
		t.Fatalf("self transition ran actions: entries=%d exits=%d, want 0/0", entries, exits)
	}
	if got := len(m.Log()); got != 4 {
		t.Fatalf("log len = %d, want 4 (self transitions still logged)", got)
	}
	for i := 0; i < 3; i++ {
		if got, ok := recvWithTimeout(t, ch); !ok || got != "active" {
			t.Fatalf("observer notification %d = %q, %v", i, got, ok)
		}
	}
}
