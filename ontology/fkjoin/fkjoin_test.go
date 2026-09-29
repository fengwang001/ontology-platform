package fkjoin

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// threadSafeBuffer 是并发安全的日志缓冲，日志中保留输入、输出条目与判定依据。
type threadSafeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *threadSafeBuffer) Printf(format string, args ...any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	fmt.Fprintf(&b.buf, format+"\n", args...)
}

func newLoggedJoiner(t *testing.T, maxPending int) (*Joiner, *bytes.Buffer) {
	t.Helper()
	log := &threadSafeBuffer{}
	return New(maxPending, log), &log.buf
}

func ids(rows []Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.LeftID
	}
	return out
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantErrIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want error %v, got %v", target, err)
	}
}

func sameRow(a, b Row) bool {
	return a.LeftID == b.LeftID && a.LeftValue == b.LeftValue &&
		bytes.Equal(a.RightKey, b.RightKey) && a.RightValue == b.RightValue
}

// 基础流程：左行订阅后右行到达，排空后得到一条内连接结果。
func TestBasicJoinAfterDrain(t *testing.T) {
	j, _ := newLoggedJoiner(t, 100)

	mustOK(t, j.UpsertLeft("L1", "lv", []byte("K1")))
	if got := j.Snapshot(); len(got) != 0 {
		t.Fatalf("before delivery result must be empty, got %v", got)
	}
	mustOK(t, j.PutRight([]byte("K1"), "rv"))
	if got := j.Pending(); got != 2 {
		t.Fatalf("pending = %d, want 2 (lookup + put)", got)
	}
	mustOK(t, j.DeliverOne()) // lookup: miss
	if got := j.Snapshot(); len(got) != 0 {
		t.Fatalf("miss response must not add result, got %v", got)
	}
	mustOK(t, j.DeliverOne()) // put: hit
	got := j.Snapshot()
	if len(got) != 1 || got[0].LeftID != "L1" || got[0].RightValue != "rv" ||
		string(got[0].RightKey) != "K1" || got[0].LeftValue != "lv" {
		t.Fatalf("unexpected result: %+v", got)
	}
	if j.Dropped() != 0 {
		t.Fatalf("dropped = %d, want 0", j.Dropped())
	}
}

// 先有右行再有左行：lookup 直接命中。
func TestLookupHit(t *testing.T) {
	j, _ := newLoggedJoiner(t, 100)
	mustOK(t, j.PutRight([]byte("K1"), "rv"))
	mustOK(t, j.UpsertLeft("L1", "lv", []byte("K1")))
	mustOK(t, j.DeliverOne())
	if got := ids(j.Snapshot()); len(got) != 1 || got[0] != "L1" {
		t.Fatalf("snapshot = %v", got)
	}
}

// 哈希校验丢弃过期响应：投递前改订阅，旧 K1 响应因哈希不一致被丢弃计数。
func TestHashMismatchDropsStaleResponse(t *testing.T) {
	j, log := newLoggedJoiner(t, 100)

	mustOK(t, j.UpsertLeft("L1", "v1", []byte("K1"))) // lookup(K1) miss
	mustOK(t, j.PutRight([]byte("K1"), "r1"))         // put(K1)
	mustOK(t, j.UpsertLeft("L1", "v1", []byte("K2"))) // 改订阅：旧响应作废

	mustOK(t, j.DeliverOne()) // 旧 lookup(K1)：哈希不一致 -> 丢弃
	mustOK(t, j.DeliverOne()) // 旧 put(K1)：哈希不一致 -> 丢弃
	if j.Dropped() != 2 {
		t.Fatalf("dropped = %d, want 2", j.Dropped())
	}
	if got := j.Snapshot(); len(got) != 0 {
		t.Fatalf("stale responses must not affect result, got %v", got)
	}
	mustOK(t, j.PutRight([]byte("K2"), "r2"))
	mustOK(t, j.DeliverOne()) // 新 lookup(K2) miss
	mustOK(t, j.DeliverOne()) // put(K2) hit
	got := j.Snapshot()
	if len(got) != 1 || got[0].LeftID != "L1" || string(got[0].RightKey) != "K2" ||
		got[0].RightValue != "r2" {
		t.Fatalf("unexpected result after re-subscribe: %+v", got)
	}
	if !strings.Contains(log.String(), "cause=hash-mismatch") {
		t.Fatalf("log missing hash-mismatch rationale:\n%s", log.String())
	}
}

// 值变更同样改变哈希，旧响应被丢弃，结果取最新左行值。
func TestValueChangeDropsStaleResponse(t *testing.T) {
	j, _ := newLoggedJoiner(t, 100)
	mustOK(t, j.UpsertLeft("L1", "old", []byte("K1")))
	mustOK(t, j.PutRight([]byte("K1"), "r1"))
	mustOK(t, j.UpsertLeft("L1", "new", []byte("K1")))
	mustOK(t, j.Drain())
	got := j.Snapshot()
	if len(got) != 1 || got[0].LeftValue != "new" || got[0].RightValue != "r1" {
		t.Fatalf("result must reflect newest left value, got %+v", got)
	}
	if j.Dropped() != 2 {
		t.Fatalf("dropped = %d, want 2", j.Dropped())
	}
}

// 删除左行后，其在途响应按“左行不存在”丢弃。
func TestRemoveLeftDropsInFlight(t *testing.T) {
	j, log := newLoggedJoiner(t, 100)
	mustOK(t, j.UpsertLeft("L1", "v", []byte("K1")))
	mustOK(t, j.PutRight([]byte("K1"), "r"))
	mustOK(t, j.RemoveLeft("L1"))
	mustOK(t, j.Drain())
	if j.Dropped() != 2 {
		t.Fatalf("dropped = %d, want 2", j.Dropped())
	}
	if got := j.Snapshot(); len(got) != 0 {
		t.Fatalf("result must be empty, got %v", got)
	}
	if !strings.Contains(log.String(), "cause=left-row-gone") {
		t.Fatalf("log missing left-row-gone rationale:\n%s", log.String())
	}
}

// 外键改为空：立即撤回结果、不再订阅，旧订阅的在途响应被丢弃。
func TestForeignKeySetToNull(t *testing.T) {
	j, _ := newLoggedJoiner(t, 100)
	mustOK(t, j.PutRight([]byte("K1"), "r1"))
	mustOK(t, j.UpsertLeft("L1", "v", []byte("K1")))
	mustOK(t, j.Drain())
	if got := j.Snapshot(); len(got) != 1 {
		t.Fatalf("expected joined row, got %v", got)
	}
	mustOK(t, j.UpsertLeft("L1", "v", nil))
	if got := j.Snapshot(); len(got) != 0 {
		t.Fatalf("null fk must withdraw result immediately, got %v", got)
	}
	if j.Pending() != 0 {
		t.Fatalf("null fk enqueues no response, pending = %d", j.Pending())
	}
	// 右行之后再变化，空外键左行不订阅、不收响应。
	mustOK(t, j.PutRight([]byte("K1"), "r2"))
	if j.Pending() != 0 {
		t.Fatalf("null-fk row must not subscribe, pending = %d", j.Pending())
	}
	if got := j.Snapshot(); len(got) != 0 {
		t.Fatalf("result must stay empty, got %v", got)
	}
}

// 删除右表键：排空 remove 响应后撤回结果，重新插入后可重新加入。
func TestRemoveRightWithdrawsResult(t *testing.T) {
	j, log := newLoggedJoiner(t, 100)
	mustOK(t, j.PutRight([]byte("K1"), "r1"))
	mustOK(t, j.UpsertLeft("L1", "v", []byte("K1")))
	mustOK(t, j.UpsertLeft("L2", "w", []byte("K1")))
	mustOK(t, j.Drain())
	if got := len(j.Snapshot()); got != 2 {
		t.Fatalf("snapshot len = %d, want 2", got)
	}
	mustOK(t, j.RemoveRight([]byte("K1")))
	// 删除响应投递前结果暂时保留。
	if got := len(j.Snapshot()); got != 2 {
		t.Fatalf("result withdraws only after delivery, got %d", got)
	}
	mustOK(t, j.Drain())
	if got := j.Snapshot(); len(got) != 0 {
		t.Fatalf("result must be withdrawn, got %v", got)
	}
	if !strings.Contains(log.String(), "output withdraw") {
		t.Fatalf("log missing withdraw rationale:\n%s", log.String())
	}
	mustOK(t, j.PutRight([]byte("K1"), "r2"))
	mustOK(t, j.Drain())
	got := j.Snapshot()
	if len(got) != 2 {
		t.Fatalf("expected 2 rejoined rows, got %v", got)
	}
	for _, r := range got {
		if r.RightValue != "r2" {
			t.Fatalf("stale right value: %+v", r)
		}
	}
}

// 交错变更与分批投递：排空后视图必须与朴素内连接一致。
func TestInterleavedMatchesNaiveJoin(t *testing.T) {
	j, _ := newLoggedJoiner(t, 1000)

	type op struct {
		kind    string
		a, b    string
		deliver int
	}
	ops := []op{
		{kind: "lu", a: "L1", b: "K1"},
		{kind: "lu", a: "L2", b: "K2"},
		{kind: "rp", a: "K1", b: "a"},
		{kind: "deliver", deliver: 2},
		{kind: "rp", a: "K2", b: "b"},
		{kind: "lu", a: "L3", b: "K3"},
		{kind: "rr", a: "K1"},
		{kind: "deliver", deliver: 1},
		{kind: "lu", a: "L1", b: "K2"},
		{kind: "rp", a: "K3", b: "c"},
		{kind: "lu", a: "L4", b: ""},
		{kind: "deliver", deliver: 3},
		{kind: "rr", a: "K2"},
		{kind: "lu", a: "L2", b: ""},
		{kind: "rp", a: "K1", b: "d"},
		{kind: "deliver", deliver: 10},
	}
	for _, o := range ops {
		switch o.kind {
		case "lu":
			var fk []byte
			if o.b != "" {
				fk = []byte(o.b)
			}
			mustOK(t, j.UpsertLeft(o.a, o.a+"-v", fk))
		case "rp":
			mustOK(t, j.PutRight([]byte(o.a), o.b))
		case "rr":
			mustOK(t, j.RemoveRight([]byte(o.a)))
		case "deliver":
			for k := 0; k < o.deliver && j.Pending() > 0; k++ {
				mustOK(t, j.DeliverOne())
			}
		}
	}
	for j.Pending() > 0 {
		mustOK(t, j.DeliverOne())
	}

	got := j.Snapshot()
	want := j.NaiveJoin()
	if len(got) != len(want) {
		t.Fatalf("snapshot %v != naive %v", got, want)
	}
	for i := range want {
		if !sameRow(got[i], want[i]) {
			t.Fatalf("row %d: got %+v want %+v", i, got[i], want[i])
		}
	}
}

// 非法输入：键为空必须以 ErrNullKey 拒绝，且不产生任何副作用。
func TestRejectNullKeys(t *testing.T) {
	j, _ := newLoggedJoiner(t, 100)
	cases := []struct {
		name string
		fn   func() error
	}{
		{"upsert-left-empty-id", func() error { return j.UpsertLeft("", "v", []byte("K")) }},
		{"upsert-left-empty-fk-bytes", func() error { return j.UpsertLeft("L", "v", []byte{}) }},
		{"remove-left-empty-id", func() error { return j.RemoveLeft("") }},
		{"put-right-nil-key", func() error { return j.PutRight(nil, "v") }},
		{"put-right-empty-bytes", func() error { return j.PutRight([]byte{}, "v") }},
		{"remove-right-nil-key", func() error { return j.RemoveRight(nil) }},
		{"remove-right-empty-bytes", func() error { return j.RemoveRight([]byte{}) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantErrIs(t, c.fn(), ErrNullKey)
		})
	}
	if j.Pending() != 0 || j.Dropped() != 0 || len(j.Snapshot()) != 0 || len(j.NaiveJoin()) != 0 {
		t.Fatalf("rejected operations must have no side effects: pending=%d dropped=%d",
			j.Pending(), j.Dropped())
	}
}

// 空队列投递必须以 ErrEmptyQueue 拒绝，且不改变丢弃数。
func TestRejectDeliverEmptyQueue(t *testing.T) {
	j, log := newLoggedJoiner(t, 10)
	wantErrIs(t, j.DeliverOne(), ErrEmptyQueue)
	wantErrIs(t, j.Drain(), ErrEmptyQueue)
	if j.Dropped() != 0 || j.Pending() != 0 {
		t.Fatalf("empty-queue delivery must not change counters")
	}
	// 排空后再次投递同样被拒绝。
	mustOK(t, j.UpsertLeft("L1", "v", []byte("K1")))
	mustOK(t, j.Drain())
	wantErrIs(t, j.DeliverOne(), ErrEmptyQueue)
	if !strings.Contains(log.String(), "reject DeliverOne: empty queue") {
		t.Fatalf("log missing empty-queue rationale:\n%s", log.String())
	}
}

// 待投递响应超限：左表单响应与右表扇出超限都返回 ErrQueueOverflow，
// 且被拒绝的操作不改变两表、订阅、队列、结果与丢弃数。
func TestRejectQueueOverflow(t *testing.T) {
	t.Run("left-overflow", func(t *testing.T) {
		j, log := newLoggedJoiner(t, 1)
		mustOK(t, j.UpsertLeft("L1", "v", []byte("K1"))) // 队列占满
		before := j.Pending()
		wantErrIs(t, j.UpsertLeft("L2", "v", []byte("K2")), ErrQueueOverflow)
		if j.Pending() != before {
			t.Fatalf("rejected upsert changed queue: %d -> %d", before, j.Pending())
		}
		// 被拒绝的 L2 确实未写入左表：排空后朴素连接为空。
		mustOK(t, j.Drain())
		if got := j.NaiveJoin(); len(got) != 0 {
			t.Fatalf("rejected left row must not exist, got %v", got)
		}
		if !strings.Contains(log.String(), "queue overflow") {
			t.Fatalf("log missing overflow rationale:\n%s", log.String())
		}
	})

	t.Run("right-fanout-overflow", func(t *testing.T) {
		j, _ := newLoggedJoiner(t, 3)
		mustOK(t, j.UpsertLeft("L1", "v", []byte("K1")))
		mustOK(t, j.UpsertLeft("L2", "v", []byte("K1")))
		// lookup x2 入队；put 的 2 个扇出使总数达到 4 > 3 -> 超限。
		wantErrIs(t, j.PutRight([]byte("K1"), "r"), ErrQueueOverflow)
		if j.Pending() != 2 {
			t.Fatalf("rejected put changed queue, pending = %d", j.Pending())
		}
		if _, ok := j.rightSnapshot()["K1"]; ok {
			t.Fatalf("rejected put must not store right row")
		}
		// 先投递一个 lookup 腾出空间：put 恰好 3 个，成功。
		mustOK(t, j.DeliverOne())
		mustOK(t, j.PutRight([]byte("K1"), "r"))
		if j.Pending() != 3 {
			t.Fatalf("pending = %d, want 3", j.Pending())
		}
		// 队列再次占满，扇出必超限。
		wantErrIs(t, j.RemoveRight([]byte("K1")), ErrQueueOverflow)
		if j.Pending() != 3 {
			t.Fatalf("rejected remove changed queue, pending = %d", j.Pending())
		}
		mustOK(t, j.Drain())
		joined := j.Snapshot()
		if len(joined) != 2 {
			t.Fatalf("after successful put both subscribers join, got %v", joined)
		}
		for _, r := range joined {
			if r.RightValue != "r" {
				t.Fatalf("unexpected right value: %+v", r)
			}
		}
	})
}

// rightSnapshot 仅供测试检查右表内容。
func (j *Joiner) rightSnapshot() map[string]string {
	j.mu.RLock()
	defer j.mu.RUnlock()
	out := make(map[string]string, len(j.right))
	for k, v := range j.right {
		out[k] = v
	}
	return out
}

// 确定性：同一输入序列两次计算得到完全相同的结果与丢弃数。
func TestDeterministicReplay(t *testing.T) {
	run := func() ([]Row, int, int) {
		j, _ := newLoggedJoiner(t, 1000)
		mustOK(t, j.UpsertLeft("L1", "a", []byte("K1")))
		mustOK(t, j.PutRight([]byte("K1"), "r1"))
		mustOK(t, j.UpsertLeft("L2", "b", []byte("K2")))
		mustOK(t, j.UpsertLeft("L1", "a2", []byte("K2")))
		mustOK(t, j.PutRight([]byte("K2"), "r2"))
		mustOK(t, j.RemoveRight([]byte("K1")))
		mustOK(t, j.UpsertLeft("L3", "c", nil))
		mustOK(t, j.Drain())
		return j.Snapshot(), j.Dropped(), j.Pending()
	}
	rows1, dropped1, pending1 := run()
	rows2, dropped2, pending2 := run()
	if dropped1 != dropped2 || pending1 != pending2 || len(rows1) != len(rows2) {
		t.Fatalf("non-deterministic replay: %+v/%d/%d vs %+v/%d/%d",
			rows1, dropped1, pending1, rows2, dropped2, pending2)
	}
	for i := range rows1 {
		if !sameRow(rows1[i], rows2[i]) {
			t.Fatalf("row %d differs: %+v vs %+v", i, rows1[i], rows2[i])
		}
	}
	if len(rows1) == 0 {
		t.Fatalf("expected non-empty result in replay")
	}
}

// 并发：写入、投递与快照读取并行执行不发生数据竞争，
// 排空后仍与朴素内连接一致。
func TestConcurrentReadersAndWriters(t *testing.T) {
	j, _ := newLoggedJoiner(t, 10000)
	var wg sync.WaitGroup

	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for n := 0; n < 50; n++ {
				id := fmt.Sprintf("L%d-%d", g, n%10)
				key := fmt.Sprintf("K%d", (g+n)%5)
				var fk []byte
				if n%7 != 0 {
					fk = []byte(key)
				}
				_ = j.UpsertLeft(id, "v", fk)
				if n%3 == 0 {
					_ = j.PutRight([]byte(key), "r")
				}
				if n%11 == 0 {
					_ = j.DeliverOne()
				}
			}
		}(g)
	}
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				_ = j.Snapshot()
				_ = j.Pending()
				_ = j.Dropped()
			}
		}()
	}
	wg.Wait()
	for j.Pending() > 0 {
		mustOK(t, j.DeliverOne())
	}

	got := j.Snapshot()
	want := j.NaiveJoin()
	if len(got) != len(want) {
		t.Fatalf("after drain snapshot %v != naive %v", got, want)
	}
	for i := range want {
		if !sameRow(got[i], want[i]) {
			t.Fatalf("row %d mismatch: %+v vs %+v", i, got[i], want[i])
		}
	}
}

// 快照返回的字节切片是副本，外部修改不影响组件内部状态。
func TestSnapshotIsolation(t *testing.T) {
	j, _ := newLoggedJoiner(t, 100)
	mustOK(t, j.PutRight([]byte("K1"), "r1"))
	mustOK(t, j.UpsertLeft("L1", "v", []byte("K1")))
	mustOK(t, j.Drain())
	rows := j.Snapshot()
	rows[0].RightKey[0] = 'X'
	rows[0].RightValue = "hacked"
	again := j.Snapshot()
	if string(again[0].RightKey) != "K1" || again[0].RightValue != "r1" {
		t.Fatalf("snapshot must be an isolated copy, got %+v", again[0])
	}
}

// 日志必须包含输入、输出条目与判定依据。
func TestLogsContainInputsOutputsAndRationale(t *testing.T) {
	j, log := newLoggedJoiner(t, 100)
	mustOK(t, j.PutRight([]byte("K1"), "r1"))
	mustOK(t, j.UpsertLeft("L1", "v1", []byte("K1")))
	mustOK(t, j.DeliverOne())
	mustOK(t, j.RemoveRight([]byte("K1")))
	mustOK(t, j.DeliverOne())

	text := log.String()
	for _, want := range []string{
		"input PutRight key=\"K1\" value=\"r1\"",
		"input UpsertLeft id=\"L1\"",
		"output join id=\"L1\"",
		"reason=\"lookup\"",
		"input RemoveRight key=\"K1\"",
		"output withdraw id=\"L1\" key=\"K1\"",
		"reason=\"remove\"",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("log missing %q:\n%s", want, text)
		}
	}
}
