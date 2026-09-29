package antientropy

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
)

func mustRegister(t *testing.T, reg *Registry, name string, cfg ...Config) *Replica {
	t.Helper()
	var c Config
	if len(cfg) > 0 {
		c = cfg[0]
	}
	rep, err := reg.Register(name, c)
	if err != nil {
		t.Fatalf("register %s: %v", name, err)
	}
	return rep
}

func assertEmpty(t *testing.T, r *Replica) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.log) != 0 || len(r.vector) != 0 {
		t.Fatalf("state must remain untouched: log=%v vector=%v", r.log, r.vector)
	}
}

// TestDiffIsMinimum：差集只包含目标已见序号之后的变更，且按 (Source, Seq) 有序。
func TestDiffIsMinimum(t *testing.T) {
	reg := NewRegistry()
	a := mustRegister(t, reg, "a")
	b := mustRegister(t, reg, "b")

	a.Write("k1", "a1")
	a.Write("k2", "a2")
	b.Write("k3", "b1")

	a.mu.Lock()
	batch := a.diffLocked(b.vector)
	a.mu.Unlock()
	if len(batch) != 2 {
		t.Fatalf("expected 2 changes, got %d: %+v", len(batch), batch)
	}
	for i, c := range batch {
		if c.Source != "a" || c.Seq != i+1 {
			t.Fatalf("batch[%d] = %+v, want a#%d", i, c, i+1)
		}
	}

	// 同步一次后再发，差集必须为空——增量同步不重发。
	if n, err := Sync(a, b, nil); err != nil || n != 2 {
		t.Fatalf("first sync = (%d, %v)", n, err)
	}
	a.mu.Lock()
	batch = a.diffLocked(b.vector)
	a.mu.Unlock()
	if len(batch) != 0 {
		t.Fatalf("expected empty diff after sync, got %+v", batch)
	}

	// 部分进度：目标已见 a#1 时只发 #2；未知来源条目不参与。
	a.mu.Lock()
	batch = a.diffLocked(Version{"a": 1, "ghost": 5})
	a.mu.Unlock()
	if len(batch) != 1 || batch[0].Seq != 2 {
		t.Fatalf("partial-knowledge diff = %+v, want only a#2", batch)
	}
}

// TestUnknownSourceIgnored：目标从未见过的来源不产生变更、不写入向量。
func TestUnknownSourceIgnored(t *testing.T) {
	reg := NewRegistry()
	a := mustRegister(t, reg, "a")
	b := mustRegister(t, reg, "b")
	c := mustRegister(t, reg, "c")
	_ = c
	a.Write("k", "v")

	// b 的向量中出现一个它从未见过的来源条目（外部传入）。
	b.mu.Lock()
	b.vector["unknown-src"] = 9
	before := copyVersion(b.vector)
	b.mu.Unlock()

	if n, err := Sync(a, b, nil); err != nil || n != 1 {
		t.Fatalf("sync = (%d, %v)", n, err)
	}
	after := b.Vector()
	if after["unknown-src"] != 9 {
		t.Fatalf("unknown source entry must be untouched, vector = %v", after)
	}
	if _, ok := after["c"]; ok {
		t.Fatalf("source c with zero changes must never be written into vector: %v", after)
	}
	if !reflect.DeepEqual(before, Version{"unknown-src": 9}) {
		t.Fatalf("precondition changed: %v", before)
	}
}

func copyVersion(v Version) Version {
	out := make(Version, len(v))
	for k, n := range v {
		out[k] = n
	}
	return out
}

// TestNonContiguousRejected：序号缺失、重复、乱序必须整体拒绝且状态不变。
func TestNonContiguousRejected(t *testing.T) {
	reg := NewRegistry()
	a := mustRegister(t, reg, "a")
	b := mustRegister(t, reg, "b")
	c1 := a.Write("k", "1")
	_ = a.Write("k", "2")

	err := b.applyBatch([]Change{c1, {Source: "a", Seq: 3, Key: "k", Value: "3", TS: 3}})
	if !errors.Is(err, ErrNonContiguousChange) {
		t.Fatalf("gap: want ErrNonContiguousChange, got %v", err)
	}
	assertEmpty(t, b)

	err = b.applyBatch([]Change{c1, c1})
	if !errors.Is(err, ErrNonContiguousChange) {
		t.Fatalf("duplicate: want ErrNonContiguousChange, got %v", err)
	}
	assertEmpty(t, b)

	a2 := Change{Source: "a", Seq: 2, Key: "k", Value: "2", TS: 2}
	err = b.applyBatch([]Change{a2, c1})
	if !errors.Is(err, ErrNonContiguousChange) {
		t.Fatalf("out-of-order: want ErrNonContiguousChange, got %v", err)
	}
	assertEmpty(t, b)

	err = b.applyBatch([]Change{{Source: "a", Seq: 0, Key: "k", Value: "0", TS: 0}})
	if !errors.Is(err, ErrNonContiguousChange) {
		t.Fatalf("seq 0: want ErrNonContiguousChange, got %v", err)
	}
	assertEmpty(t, b)
}

// TestInvalidVectorRejected：负序号向量整体拒绝，双方状态不变。
func TestInvalidVectorRejected(t *testing.T) {
	reg := NewRegistry()
	a := mustRegister(t, reg, "a")
	b := mustRegister(t, reg, "b")
	a.Write("k", "1")

	b.mu.Lock()
	b.vector["a"] = -1
	b.mu.Unlock()

	va, la := a.Vector(), a.Log()
	vb, lb := b.Vector(), b.Log()
	_, err := Sync(a, b, nil)
	if !errors.Is(err, ErrInvalidVector) {
		t.Fatalf("want ErrInvalidVector, got %v", err)
	}
	if !reflect.DeepEqual(va, a.Vector()) || !reflect.DeepEqual(vb, b.Vector()) {
		t.Fatalf("vectors changed after rejection")
	}
	if !reflect.DeepEqual(la, a.Log()) || !reflect.DeepEqual(lb, b.Log()) {
		t.Fatalf("logs changed after rejection")
	}
}

// TestLogLimitRejected：差集条数超限整体拒绝、不留痕。
func TestLogLimitRejected(t *testing.T) {
	reg := NewRegistry()
	a := mustRegister(t, reg, "a")
	b := mustRegister(t, reg, "b", Config{MaxBatch: 2})
	for i := 0; i < 3; i++ {
		a.Write(fmt.Sprintf("k%d", i), "v")
	}
	_, err := Sync(a, b, nil)
	if !errors.Is(err, ErrLogLimitExceeded) {
		t.Fatalf("want ErrLogLimitExceeded, got %v", err)
	}
	assertEmpty(t, b)

	// 发送方上限同样生效。
	reg2 := NewRegistry()
	x := mustRegister(t, reg2, "x", Config{MaxBatch: 1})
	y := mustRegister(t, reg2, "y")
	x.Write("k", "1")
	x.Write("k", "2")
	if _, err := Sync(x, y, nil); !errors.Is(err, ErrLogLimitExceeded) {
		t.Fatalf("sender limit: want ErrLogLimitExceeded, got %v", err)
	}
	assertEmpty(t, y)

	// 四类错误互不相同、可区分。
	errs := []error{ErrUnregisteredReplica, ErrNonContiguousChange, ErrInvalidVector, ErrLogLimitExceeded}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				t.Fatalf("error classes %v and %v must be distinct", errs[i], errs[j])
			}
		}
	}
}

// TestUnregisteredReplicaRejected：未注册副本/来源必须拒绝且不留痕。
func TestUnregisteredReplicaRejected(t *testing.T) {
	reg := NewRegistry()
	a := mustRegister(t, reg, "a")
	if _, err := reg.Register("a", Config{}); !errors.Is(err, ErrUnregisteredReplica) {
		t.Fatalf("duplicate register: want ErrUnregisteredReplica, got %v", err)
	}

	other := NewRegistry()
	x := mustRegister(t, other, "x")
	if _, err := Sync(a, x, nil); !errors.Is(err, ErrUnregisteredReplica) {
		t.Fatalf("cross-registry sync: want ErrUnregisteredReplica, got %v", err)
	}

	b := mustRegister(t, reg, "b")
	c := Change{Source: "ghost", Seq: 1, Key: "k", Value: "v", TS: 1}
	if err := b.applyBatch([]Change{c}); !errors.Is(err, ErrUnregisteredReplica) {
		t.Fatalf("unknown change source: want ErrUnregisteredReplica, got %v", err)
	}
	assertEmpty(t, b)
}

// TestReadViewWinner：(TS, Source, Seq) 字典序最大者胜出。
func TestReadViewWinner(t *testing.T) {
	reg := NewRegistry()
	a := mustRegister(t, reg, "a")
	b := mustRegister(t, reg, "b")
	a.Write("k", "from-a")
	b.Write("k", "from-b") // 同 TS，来源名 b > a

	if n, err := Sync(b, a, nil); err != nil || n != 1 {
		t.Fatalf("sync = (%d,%v)", n, err)
	}
	if v := a.View()["k"]; v != "from-b" {
		t.Fatalf("tie should be broken by source name, got %q", v)
	}

	c1 := Change{Source: "a", Seq: 1, Key: "x", Value: "lo", TS: 5}
	c2 := Change{Source: "a", Seq: 2, Key: "x", Value: "hi", TS: 5}
	if !c2.wins(c1) {
		t.Fatalf("higher seq must win on (ts, source) tie")
	}
}

// TestSyncLogsSteps：日志中打印每步的向量、差集与判定依据。
func TestSyncLogsSteps(t *testing.T) {
	reg := NewRegistry()
	a := mustRegister(t, reg, "a")
	b := mustRegister(t, reg, "b")
	a.Write("k", "v")
	var buf bytes.Buffer
	if _, err := Sync(a, b, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"target vector", "reason:", "send a#1", "applied 1 changes", "new vector"} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Fatalf("sync log missing %q:\n%s", want, out)
		}
	}
	if os.Getenv("SYNC_STEPS") != "" {
		t.Logf("\n%s", out)
	}
}
