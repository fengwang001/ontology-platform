package activity

import "testing"

// s2s 到期时刻（唤醒后 g+s2s）早于 sc：必须先 s2s 终局，而非等 sc。
// 这是 Waiting 唤醒与 sc 先后顺序的回归用例。
func TestWakeBeforeSC(t *testing.T) {
	e := NewDiscard()
	id := []byte("w")
	// hb=1 第一次执行在 1 失败，退避 d0=4 → g'=5；s2s=4 → s2s@9；sc=29。
	cfg := Config{S2S: 4, S2C: 2, HB: 1, SC: 29, M: 3, D0: 4, Cap: 4}
	must(t, e.Schedule(id, cfg, 0))
	if _, _, err := e.Start(id, 1); err != nil {
		t.Fatal(err)
	}
	// 一次 Status(30) 跨越 hb 失败→Waiting→唤醒→s2s 到期。
	st, _ := e.Status(id, 30)
	if st.Terminal != TermTimedOutS2S || st.At != 10 || st.Attempt != 2 {
		t.Fatalf("got %+v, want TimedOutS2S@10 k=2", st)
	}
}

func TestHeapProbeCount(t *testing.T) {
	// Status 不回写探针；用一个会被接受、且此前无到期的写操作测“无到期=1”。
	e3 := NewDiscard()
	id3 := []byte("p3")
	must(t, e3.Schedule(id3, Config{S2C: 100, HB: 0, SC: 0, M: 2, D0: 1, Cap: 1}, 0))
	if _, _, err := e3.Start(id3, 0); err != nil {
		t.Fatal(err)
	}
	must(t, e3.Heartbeat(id3, 1, 5, 1))
	probe3, _ := e3.probeOf(id3)
	if probe3 != 1 {
		t.Fatalf("probe with no expiry = %d, want 1", probe3)
	}

	// 单事件场景：Start(9) 时真实活动尚在 Running（Status 不回写），
	// advance 处理 hb@4（1 事件）→Waiting→在 9 唤醒→Scheduled，再由 Start 接收，
	// 末尾空堆 Peek 1 次停止 → 探针 2 = 事件数 + 1。
	e4 := NewDiscard()
	id4 := []byte("p4")
	must(t, e4.Schedule(id4, Config{S2C: 100, HB: 4, SC: 0, M: 3, D0: 5, Cap: 20}, 0))
	if _, _, err := e4.Start(id4, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e4.Start(id4, 9); err != nil {
		t.Fatal(err)
	}
	probe4, _ := e4.probeOf(id4)
	if probe4 != 2 {
		t.Fatalf("probe with 1 hb expiry = %d, want 2 (events+1)", probe4)
	}
}
