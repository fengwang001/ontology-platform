package api

import (
	"math/rand"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestEightStep(t *testing.T) { // 钉住第三节八步表：写者优先（不变量 3）与互斥（不变量 2）
	rw := New()
	got := make([]Snapshot, 0, 8)
	rec := func() { got = append(got, rw.Snapshot()) }
	wait := func(ok bool, msg string) {
		if !ok {
			t.Fatal(msg)
		}
	}
	_ = rw.AcquireRead(1)
	rec() // 1
	_ = rw.AcquireRead(2)
	rec() // 2
	t3 := make(chan error, 1)
	go func() { t3 <- rw.AcquireWrite(3) }()
	wait(spin(func() bool { return rw.Snapshot().WaitingWriters == 1 }), "T3 未等待")
	rec() // 3
	t4 := make(chan error, 1)
	go func() { t4 <- rw.AcquireRead(4) }()
	wait(spin(func() bool { return len(rw.Snapshot().WaitingReaders) == 1 }), "T4 未被阻塞")
	rec() // 4
	_ = rw.ReleaseRead(1)
	rec() // 5
	_ = rw.ReleaseRead(2)
	wait(<-t3 == nil, "T3 获写失败")
	rec() // 6
	_ = rw.ReleaseWrite(3)
	wait(<-t4 == nil, "T4 获读失败")
	rec() // 7
	_ = rw.ReleaseRead(4)
	rec() // 8
	want := []Snapshot{
		{Readers: []int{1}, Writer: -1}, {Readers: []int{1, 2}, Writer: -1},
		{Readers: []int{1, 2}, Writer: -1, WaitingWriters: 1},
		{Readers: []int{1, 2}, Writer: -1, WaitingWriters: 1, WaitingReaders: []int{4}},
		{Readers: []int{2}, Writer: -1, WaitingWriters: 1, WaitingReaders: []int{4}},
		{Writer: 3, WaitingReaders: []int{4}},
		{Readers: []int{4}, Writer: -1}, {Writer: -1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("八步状态不符: %+v", got)
	}
}

func TestFaultInjection(t *testing.T) { // 四类故障注入：哨兵互异、被拒零变化、之后仍可用（不变量 4）
	if len(map[error]bool{ErrNotReader: true, ErrNotWriter: true, ErrReaderReentry: true,
		ErrWriterReentry: true, ErrUpgradeNotReader: true, ErrUpgradeConflict: true}) != 6 {
		t.Fatal("哨兵错误不互异")
	}
	cases := []struct {
		setup, op, down string
		want            error
	}{
		{"", "RR1", "", ErrNotReader},
		{"", "RW1", "", ErrNotWriter},
		{"AR1", "AR1", "RR1", ErrReaderReentry},
		{"AW1", "AW1", "RW1", ErrWriterReentry},
		{"", "UP1", "", ErrUpgradeNotReader},
		{"AR1 AR2", "UP1", "RR2 RR1", ErrUpgradeConflict},
	}
	for _, c := range cases {
		rw := New()
		for _, f := range strings.Fields(c.setup) {
			_ = realOps[f[:2]](rw, int(f[2]-'0'))
		}
		before := rw.Snapshot()
		err := realOps[c.op[:2]](rw, int(c.op[2]-'0'))
		if err != c.want || !reflect.DeepEqual(before, rw.Snapshot()) {
			t.Fatalf("%v: 错误不符或失败留痕", c.want)
		}
		for _, f := range strings.Fields(c.setup + " " + c.down) {
			_ = realOps[f[:2]](rw, int(f[2]-'0'))
		}
		if rw.AcquireWrite(9) != nil || rw.Snapshot().Writer != 9 {
			t.Fatal("被拒后锁不可用")
		}
	}
}

func TestNaiveEquivalence(t *testing.T) { // 随机操作序列下与朴素参照逐步一致（不变量 1）
	names := []string{"AR", "RR", "AW", "RW", "UP"}
	for seed := int64(0); seed < 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		rw, nv := New(), newNaive()
		for i := 0; i < 300; i++ {
			op, o := names[rng.Intn(5)], rng.Intn(4)
			blocked, want := nv.apply(op, o)
			if blocked {
				continue
			}
			got := realOps[op](rw, o)
			if got != want || !reflect.DeepEqual(rw.Snapshot(), nv.snapshot()) {
				t.Fatalf("seed=%d 步 %d: %v", seed, i, got)
			}
		}
	}
}

func TestConcurrentMonotonic(t *testing.T) { // N reader + 1 writer：值单调不减；有写者则无读者；无 sleep
	rw := New()
	var counter, bad, stop int64
	done := make(chan struct{}, 8)
	for g := 0; g < 8; g++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			var last int64
			for atomic.LoadInt64(&stop) == 0 {
				_ = rw.AcquireRead(id)
				v, s := atomic.LoadInt64(&counter), rw.Snapshot()
				_ = rw.ReleaseRead(id)
				if v < last || (s.Writer >= 0 && len(s.Readers) > 0) {
					atomic.StoreInt64(&bad, 1)
					return
				}
				last = v
			}
		}(g)
	}
	for i := 0; i < 500; i++ {
		_ = rw.AcquireWrite(99)
		atomic.AddInt64(&counter, 1)
		s := rw.Snapshot()
		_ = rw.ReleaseWrite(99)
		if s.Writer != 99 || len(s.Readers) != 0 {
			t.Fatal("持写期间 Snapshot 出现读者")
		}
	}
	atomic.StoreInt64(&stop, 1)
	for g := 0; g < 8; g++ {
		<-done
	}
	if bad != 0 || counter != 500 {
		t.Fatalf("bad=%d counter=%d", bad, counter)
	}
}
func TestSelfCheck(t *testing.T) { // 内置自检须通过（四条不变量）
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
