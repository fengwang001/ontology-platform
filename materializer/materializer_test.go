package materializer

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
)

func newTestMaterializer(t *testing.T) (*Materializer, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	return New(logger), &buf
}

func strptr(s string) *string { return &s }

func TestPutThenDeleteSameKeyOrderSemantics(t *testing.T) {
	m, _ := newTestMaterializer(t)

	// 同键先写后删：第二条 DELETE 的前置期望必须看到第一条 PUT 的新值。
	batch := []Event{
		{Key: "k", Op: Put, Value: "v1", Expect: nil},
		{Key: "k", Op: Delete, Expect: strptr("v1")},
	}
	if err := m.Apply(batch); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if _, ok := m.Get("k"); ok {
		t.Fatalf("key k should be deleted")
	}
}

func TestChainedExpectations(t *testing.T) {
	m, _ := newTestMaterializer(t)

	// 链式期望：put a -> put b -> 覆盖 a(expect 旧值可见) -> delete b。
	batch := []Event{
		{Key: "a", Op: Put, Value: "1", Expect: nil},
		{Key: "b", Op: Put, Value: "2", Expect: nil},
		{Key: "a", Op: Put, Value: "3", Expect: strptr("1")},
		{Key: "b", Op: Delete, Expect: strptr("2")},
	}
	if err := m.Apply(batch); err != nil {
		t.Fatalf("chained batch should apply: %v", err)
	}
	if v, ok := m.Get("a"); !ok || v != "3" {
		t.Fatalf("a = %q,%v want 3,true", v, ok)
	}
	if _, ok := m.Get("b"); ok {
		t.Fatalf("b should be absent")
	}
}

func TestPriorDeleteVisibleToLaterExpectAbsent(t *testing.T) {
	m, _ := newTestMaterializer(t)

	if err := m.Apply([]Event{{Key: "k", Op: Put, Value: "old", Expect: nil}}); err != nil {
		t.Fatal(err)
	}
	// 删除后，同批后续事件用 expect-absent 必须能观察到删除。
	batch := []Event{
		{Key: "k", Op: Delete, Expect: strptr("old")},
		{Key: "k", Op: Put, Value: "new", Expect: nil},
	}
	if err := m.Apply(batch); err != nil {
		t.Fatalf("delete then re-put should apply: %v", err)
	}
	if v, ok := m.Get("k"); !ok || v != "new" {
		t.Fatalf("k = %q,%v want new,true", v, ok)
	}
}

func TestAllOrNothingFailureLeavesNoTrace(t *testing.T) {
	m, _ := newTestMaterializer(t)

	if err := m.Apply([]Event{{Key: "a", Op: Put, Value: "1", Expect: nil}}); err != nil {
		t.Fatal(err)
	}

	before := m.Snapshot()
	// 前两条会通过（含对新键 b 的写入），第三条期望失败：整批必须无痕。
	bad := []Event{
		{Key: "b", Op: Put, Value: "2", Expect: nil},
		{Key: "a", Op: Put, Value: "9", Expect: strptr("1")},
		{Key: "a", Op: Put, Value: "0", Expect: strptr("not-match")},
	}
	err := m.Apply(bad)
	if err == nil {
		t.Fatal("bad batch must be rejected")
	}

	after := m.Snapshot()
	if fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("state changed after rejection: before=%v after=%v", before, after)
	}
	if _, ok := m.Get("b"); ok {
		t.Fatal("staged key b from rejected batch must not be visible")
	}

	// 被拒后仍可正常使用。
	if err := m.Apply([]Event{{Key: "c", Op: Put, Value: "3", Expect: nil}}); err != nil {
		t.Fatalf("materializer unusable after rejection: %v", err)
	}
	if v, ok := m.Get("c"); !ok || v != "3" {
		t.Fatalf("c = %q,%v want 3,true", v, ok)
	}
}

func TestFailureReportsFirstFailedEvent(t *testing.T) {
	m, _ := newTestMaterializer(t)

	bad := []Event{
		{Key: "a", Op: Put, Value: "1", Expect: nil},
		{Key: "a", Op: Delete, Expect: strptr("1")},
		{Key: "a", Op: Put, Value: "2", Expect: strptr("still-1")}, // idx2: 此时 a 已被前序删除
		{Key: "a", Op: Put, Value: "3", Expect: nil},               // idx3 也不满足，但不应报告它
	}
	err := m.Apply(bad)
	var fe *FailureError
	if !errors.As(err, &fe) {
		t.Fatalf("want *FailureError, got %T", err)
	}
	if fe.Index != 2 {
		t.Fatalf("failure index = %d, want 2", fe.Index)
	}
	if !errors.Is(err, ErrExpectationFailed) {
		t.Fatalf("want ErrExpectationFailed, got %v", err)
	}
}

func TestIllegalInputsAreDistinctAndStatePreserving(t *testing.T) {
	m, _ := newTestMaterializer(t)
	if err := m.Apply([]Event{{Key: "a", Op: Put, Value: "1", Expect: nil}}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		batch   []Event
		wantErr error
		index   int
		wantKey string
	}{
		{
			name:    "empty batch",
			batch:   nil,
			wantErr: ErrEmptyBatch,
			index:   -1,
		},
		{
			name:    "empty key",
			batch:   []Event{{Key: "", Op: Put, Value: "x", Expect: nil}},
			wantErr: ErrEmptyKey,
			index:   0,
			wantKey: "",
		},
		{
			name: "expect absent but present",
			batch: []Event{
				{Key: "a", Op: Put, Value: "2", Expect: nil},
			},
			wantErr: ErrExpectationFailed,
			index:   0,
			wantKey: "a",
		},
		{
			name: "expect value but absent",
			batch: []Event{
				{Key: "missing", Op: Put, Value: "2", Expect: strptr("whatever")},
			},
			wantErr: ErrExpectationFailed,
			index:   0,
			wantKey: "missing",
		},
		{
			name: "expect value mismatch",
			batch: []Event{
				{Key: "a", Op: Put, Value: "2", Expect: strptr("wrong")},
			},
			wantErr: ErrExpectationFailed,
			index:   0,
			wantKey: "a",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := m.Snapshot()
			err := m.Apply(tc.batch)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want errors.Is %v", err, tc.wantErr)
			}
			fe, ok := err.(*FailureError)
			if !ok {
				t.Fatalf("want *FailureError, got %T", err)
			}
			if fe.Index != tc.index {
				t.Fatalf("index = %d, want %d", fe.Index, tc.index)
			}
			if fe.Event.Key != tc.wantKey {
				t.Fatalf("reported event key = %q, want %q", fe.Event.Key, tc.wantKey)
			}
			after := m.Snapshot()
			if fmt.Sprint(after) != fmt.Sprint(before) {
				t.Fatalf("state changed: before=%v after=%v", before, after)
			}
		})
	}

	// 三类哨兵错误必须互不相同。
	if errors.Is(ErrEmptyBatch, ErrEmptyKey) || errors.Is(ErrEmptyKey, ErrExpectationFailed) ||
		errors.Is(ErrEmptyBatch, ErrExpectationFailed) {
		t.Fatal("sentinel errors must be distinct")
	}
}

func TestSnapshotIsolation(t *testing.T) {
	m, _ := newTestMaterializer(t)
	if err := m.Apply([]Event{{Key: "a", Op: Put, Value: "1", Expect: nil}}); err != nil {
		t.Fatal(err)
	}
	snap := m.Snapshot()
	snap["a"] = "mutated"
	snap["b"] = "injected"
	if v, ok := m.Get("a"); !ok || v != "1" {
		t.Fatalf("snapshot must be a deep copy, got a=%q,%v", v, ok)
	}
	if _, ok := m.Get("b"); ok {
		t.Fatal("snapshot mutation leaked into materializer")
	}
}

func TestConcurrentReadsOnlySeeBatchBoundaries(t *testing.T) {
	var lbuf bytes.Buffer
	m := New(log.New(&lbuf, "", 0))

	// 每个批次把 keys[0..keyCount-1] 全部写成同一个值（批次号）。
	// 读取侧不变量：所有键要么整体为空（初始边界），要么全部等于同一
	// 批次号；任何半批次中间态都会让某个键落后/超前，从而被检测到。
	const batchCount = 80
	const keyCount = 12

	stop := make(chan struct{})
	var wg sync.WaitGroup

	for r := 0; r < 6; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := m.Snapshot()
				v0, ok0 := snap["k0"]
				for i := 1; i < keyCount; i++ {
					v, ok := snap[fmt.Sprintf("k%d", i)]
					if ok != ok0 || (ok && v != v0) {
						panic(fmt.Sprintf("torn read: k0=%q,%v but k%d=%q,%v", v0, ok0, i, v, ok))
					}
				}
			}
		}()
	}

	for b := 1; b <= batchCount; b++ {
		batch := make([]Event, 0, keyCount)
		val := fmt.Sprintf("v%d", b)
		for i := 0; i < keyCount; i++ {
			var expect *string
			if b > 1 {
				prev := fmt.Sprintf("v%d", b-1)
				expect = &prev
			}
			batch = append(batch, Event{
				Key: fmt.Sprintf("k%d", i), Op: Put, Value: val, Expect: expect,
			})
		}
		if err := m.Apply(batch); err != nil {
			t.Fatalf("batch %d failed: %v", b, err)
		}
	}
	close(stop)
	wg.Wait()

	final := m.Snapshot()
	if len(final) != keyCount {
		t.Fatalf("final size = %d, want %d", len(final), keyCount)
	}
}

func TestApplyIsSerializedUnderConcurrency(t *testing.T) {
	m, _ := newTestMaterializer(t)
	const goroutines = 16
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			key := fmt.Sprintf("g%d", g)
			err := m.Apply([]Event{
				{Key: key, Op: Put, Value: "x", Expect: nil},
				{Key: key, Op: Put, Value: "y", Expect: strptr("x")},
			})
			errs <- err
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent apply failed: %v", err)
		}
	}
	for g := 0; g < goroutines; g++ {
		if v, ok := m.Get(fmt.Sprintf("g%d", g)); !ok || v != "y" {
			t.Fatalf("g%d = %q,%v", g, v, ok)
		}
	}
}

func TestLogsContainInputResultAndReason(t *testing.T) {
	m, buf := newTestMaterializer(t)

	if err := m.Apply([]Event{{Key: "a", Op: Put, Value: "1", Expect: nil}}); err != nil {
		t.Fatal(err)
	}
	logs := buf.String()
	for _, want := range []string{"apply begin", `key:"a"`, "event 0 ok", "apply commit"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("commit log missing %q:\n%s", want, logs)
		}
	}

	buf.Reset()
	err := m.Apply([]Event{{Key: "a", Op: Put, Value: "2", Expect: nil}})
	if err == nil {
		t.Fatal("expected rejection")
	}
	logs = buf.String()
	for _, want := range []string{
		"apply begin",
		"apply reject",
		"event 0",
		"expected key absent",
		"state unchanged",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("reject log missing %q:\n%s", want, logs)
		}
	}
}
