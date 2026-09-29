package outbox

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func msgIDs(ms []Message) []string {
	ids := make([]string, len(ms))
	for i, m := range ms {
		ids[i] = m.ID
	}
	return ids
}

// 崩溃后重投：最后一条已投递未标记，重启后重投，下游按标识去重。
func TestCrashRedelivery(t *testing.T) {
	store := NewStore(0)
	sink := NewIdempotentSink()

	input := []Message{{ID: "m1", Payload: "a"}, {ID: "m2", Payload: "b"}, {ID: "m3", Payload: "c"}}
	t.Logf("输入: tx1 写入 %v 并提交", msgIDs(input))
	if err := store.Begin("tx1"); err != nil {
		t.Fatal(err)
	}
	for _, m := range input {
		if err := store.Write("tx1", m); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Commit("tx1"); err != nil {
		t.Fatal(err)
	}

	crashed := NewRelay(store, sink)
	crashed.SetCrashBeforeLastMark(true)
	got := crashed.RunOnce()
	t.Logf("崩溃前投递序列: %v", msgIDs(got))
	if len(got) != 3 {
		t.Fatalf("崩溃前应投递 3 条，实际 %d", len(got))
	}
	if n := store.PendingLen(); n != 1 {
		t.Fatalf("判定依据: 最后一条已投递未标记，待投应为 1，实际 %d", n)
	}
	t.Logf("判定依据: 崩溃点在最后一条投递后、标记前，pending 剩余 %v", msgIDs(store.Pending()))

	restarted := NewRelay(store, sink)
	redelivered := restarted.RunOnce()
	t.Logf("重启后重投序列: %v", msgIDs(redelivered))
	if !reflect.DeepEqual(msgIDs(redelivered), []string{"m3"}) {
		t.Fatalf("重启后应只重投未标记的 m3，实际 %v", msgIDs(redelivered))
	}

	want := []string{"m1", "m2", "m3"}
	if got := msgIDs(sink.Applied()); !reflect.DeepEqual(got, want) {
		t.Fatalf("下游应用序列应为 %v（无重复），实际 %v", want, got)
	}
	if n := store.PendingLen(); n != 0 {
		t.Fatalf("全部标记后待投应为 0，实际 %d", n)
	}
	t.Logf("判定依据: 下游按消息标识幂等去重，最终应用序列 %v", msgIDs(sink.Applied()))
}

// 被中止事务的消息永不投递。
func TestAbortedTxNeverDelivered(t *testing.T) {
	store := NewStore(0)
	sink := NewIdempotentSink()

	t.Logf("输入: tx1 写入 [a1 a2] 后中止，tx2 写入 [b1] 后提交")
	mustRun(t, store, "tx1", []Message{{ID: "a1", Payload: "x"}, {ID: "a2", Payload: "y"}}, false)
	mustRun(t, store, "tx2", []Message{{ID: "b1", Payload: "z"}}, true)

	relay := NewRelay(store, sink)
	got := relay.RunOnce()
	t.Logf("投递序列: %v", msgIDs(got))
	if !reflect.DeepEqual(msgIDs(got), []string{"b1"}) {
		t.Fatalf("中止事务的消息不得投递，应只投递 [b1]，实际 %v", msgIDs(got))
	}
	t.Logf("判定依据: 中止事务的消息从未进入待投队列，最终应用 %v", msgIDs(sink.Applied()))
}

func mustRun(t *testing.T, store *Store, tx string, msgs []Message, commit bool) {
	t.Helper()
	if err := store.Begin(tx); err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if err := store.Write(tx, m); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	if commit {
		err = store.Commit(tx)
	} else {
		err = store.Abort(tx)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// 下游按消息标识幂等去重。
func TestIdempotentSink(t *testing.T) {
	sink := NewIdempotentSink()
	m := Message{ID: "m1", Payload: "p"}
	for i := 0; i < 3; i++ {
		if err := sink.Apply(m); err != nil {
			t.Fatal(err)
		}
	}
	if got := sink.Applied(); len(got) != 1 {
		t.Fatalf("同一标识重复投递应只应用一次，实际 %d 次", len(got))
	}
	if !sink.IsDuplicate("m1") {
		t.Fatal("已应用的标识应判定为重复")
	}
	t.Logf("判定依据: 标识 m1 应用 3 次，下游实际生效 %v", msgIDs(sink.Applied()))
}

// 各类非法输入被拒绝且原因可区分，且不改变任何计数器与队列。
func TestInvalidInputs(t *testing.T) {
	store := NewStore(2)
	if err := store.Begin("tx1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Write("tx1", Message{ID: "ok1", Payload: "p"}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"重复开启事务", func() error { return store.Begin("tx1") }, ErrTxExists},
		{"写入未知事务", func() error { return store.Write("nope", Message{ID: "x", Payload: "p"}) }, ErrTxNotFound},
		{"空消息标识", func() error { return store.Write("tx1", Message{Payload: "p"}) }, ErrInvalidMessage},
		{"空消息内容", func() error { return store.Write("tx1", Message{ID: "x"}) }, ErrInvalidMessage},
		{"提交未知事务", func() error { return store.Commit("nope") }, ErrTxNotFound},
		{"中止未知事务", func() error { return store.Abort("nope") }, ErrTxNotFound},
	}
	for _, c := range cases {
		if err := c.op(); !errors.Is(err, c.want) {
			t.Fatalf("%s: 应拒绝为 %v，实际 %v", c.name, c.want, err)
		}
		t.Logf("拒绝: %s -> %v", c.name, c.want)
	}

	if err := store.Write("tx1", Message{ID: "ok2", Payload: "p"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Commit("tx1"); err != nil {
		t.Fatal(err)
	}

	// 已提交事务不再可用。
	for name, op := range map[string]func() error{
		"写入已提交事务": func() error { return store.Write("tx1", Message{ID: "x", Payload: "p"}) },
		"重复提交":    func() error { return store.Commit("tx1") },
		"中止已提交事务": func() error { return store.Abort("tx1") },
	} {
		if err := op(); !errors.Is(err, ErrTxNotActive) {
			t.Fatalf("%s: 应拒绝为 %v，实际 %v", name, ErrTxNotActive, err)
		}
		t.Logf("拒绝: %s -> %v", name, ErrTxNotActive)
	}

	// 积压超限：上限 2，当前待投 2，再提交 1 条应被拒绝。
	if err := store.Begin("tx2"); err != nil {
		t.Fatal(err)
	}
	if err := store.Write("tx2", Message{ID: "ok3", Payload: "p"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Commit("tx2"); !errors.Is(err, ErrBacklogExceeded) {
		t.Fatalf("积压超限应拒绝为 %v，实际 %v", ErrBacklogExceeded, err)
	}
	t.Logf("拒绝: 积压超限 -> %v（上限 2，当前待投 %d）", ErrBacklogExceeded, store.PendingLen())

	// 判定依据：被拒绝的操作不改变提交序与待投队列。
	if got := msgIDs(store.CommittedOrder()); !reflect.DeepEqual(got, []string{"ok1", "ok2"}) {
		t.Fatalf("被拒绝的操作不得改变提交序，已提交序列应为 [ok1 ok2]，实际 %v", got)
	}
	if n := store.PendingLen(); n != 2 {
		t.Fatalf("被拒绝的操作不得改变待投队列，长度应为 2，实际 %d", n)
	}

	// 被拒绝的提交不分配提交序：tx2 保持活跃，腾空后可重试成功。
	sink := NewIdempotentSink()
	relay := NewRelay(store, sink)
	relay.RunOnce() // 投递并标记 ok1、ok2，腾空积压
	if err := store.Commit("tx2"); err != nil {
		t.Fatalf("腾空积压后重试提交应成功，实际 %v", err)
	}
	relay.RunOnce()
	want := []string{"ok1", "ok2", "ok3"}
	if got := msgIDs(sink.Applied()); !reflect.DeepEqual(got, want) {
		t.Fatalf("最终应用序列应为 %v，实际 %v", want, got)
	}
	t.Logf("判定依据: 被拒绝的提交未占用提交序，最终应用序列 %v", msgIDs(sink.Applied()))
}

// 并发写入、提交与中继：最终应用序列与朴素参照一致且无重复。
func TestConcurrentMatchesReference(t *testing.T) {
	store := NewStore(0)
	sink := NewIdempotentSink()
	relay := NewRelay(store, sink)

	const writers = 8
	const msgsPerTx = 5
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			tx := fmt.Sprintf("tx-%d", w)
			if err := store.Begin(tx); err != nil {
				t.Error(err)
				return
			}
			for i := 0; i < msgsPerTx; i++ {
				m := Message{ID: fmt.Sprintf("m-%d-%d", w, i), Payload: "p"}
				if err := store.Write(tx, m); err != nil {
					t.Error(err)
					return
				}
			}
			if err := store.Commit(tx); err != nil {
				t.Error(err)
			}
		}(w)
	}
	// 并发中继：两个中继循环取投。
	stop := make(chan struct{})
	var rwg sync.WaitGroup
	for r := 0; r < 2; r++ {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					relay.RunOnce()
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	rwg.Wait()
	relay.RunOnce() // 收尾排空

	want := msgIDs(store.CommittedOrder())
	got := msgIDs(sink.Applied())
	t.Logf("朴素参照（提交序+写入序）: %v", want)
	t.Logf("下游实际应用序列: %v", got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("并发下应用序列应与朴素参照一致\n参照: %v\n实际: %v", want, got)
	}
	if len(got) != writers*msgsPerTx {
		t.Fatalf("应用总数应为 %d，实际 %d", writers*msgsPerTx, len(got))
	}
	if n := store.PendingLen(); n != 0 {
		t.Fatalf("全部投递标记后待投应为 0，实际 %d", n)
	}
	t.Log("判定依据: 应用序列 == 提交序参照，无丢失、无重复")
}

// 同一输入序列反复计算得到完全相同的输出。
func TestDeterministic(t *testing.T) {
	run := func() []string {
		store := NewStore(0)
		sink := NewIdempotentSink()
		mustRun(t, store, "tx1", []Message{{ID: "a", Payload: "1"}, {ID: "b", Payload: "2"}}, true)
		mustRun(t, store, "tx2", []Message{{ID: "c", Payload: "3"}}, false)
		mustRun(t, store, "tx3", []Message{{ID: "d", Payload: "4"}}, true)
		NewRelay(store, sink).RunOnce()
		return msgIDs(sink.Applied())
	}
	first := run()
	for i := 0; i < 5; i++ {
		if got := run(); !reflect.DeepEqual(got, first) {
			t.Fatalf("同一输入应得到相同输出，首次 %v，第 %d 次 %v", first, i+2, got)
		}
	}
	t.Logf("输入: tx1 提交 [a b]，tx2 中止 [c]，tx3 提交 [d]")
	t.Logf("判定依据: 6 次运行输出完全一致: %v", first)
}
