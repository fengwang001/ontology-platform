package api_test

import (
	"errors"
	"math/rand"
	"strconv"
	"sync"
	"testing"

	"ontology/api"
	"ontology/drf"
)

func frac(n, d int64) drf.Frac {
	f, err := drf.NewFrac(n, d)
	if err != nil {
		panic(err)
	}
	return f
}

// 五类故障注入：每类都有可判定哨兵错误，且两两互不相同。
func TestSentinelErrors(t *testing.T) {
	sentinels := []error{api.ErrEmptyID, api.ErrDuplicateID, api.ErrNegative, api.ErrZeroDemand, api.ErrBadCapacity}
	for i, s := range sentinels {
		for j := i + 1; j < len(sentinels); j++ {
			if errors.Is(s, sentinels[j]) {
				t.Fatalf("sentinels %d and %d are not distinct", i, j)
			}
		}
	}
	a, err := api.New(60, 60)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		id       string
		cpu, mem int64
		want     error
	}{
		{"empty id", "", 1, 1, api.ErrEmptyID},
		{"negative cpu", "n1", -1, 1, api.ErrNegative},
		{"negative mem", "n2", 1, -2, api.ErrNegative},
		{"zero demand", "z", 0, 0, api.ErrZeroDemand},
		{"ok first", "ok", 1, 6, nil},
		{"duplicate id", "ok", 2, 2, api.ErrDuplicateID},
	}
	for _, c := range cases {
		if got := a.Add(c.id, c.cpu, c.mem); !errors.Is(got, c.want) {
			t.Fatalf("%s: err=%v want %v", c.name, got, c.want)
		}
	}
	for _, c := range []struct {
		cc, cm int64
	}{{0, 60}, {-1, 60}, {60, 0}, {60, -1}} {
		if _, got := api.New(c.cc, c.cm); !errors.Is(got, api.ErrBadCapacity) {
			t.Fatalf("New(%d,%d): err=%v want ErrBadCapacity", c.cc, c.cm, got)
		}
	}
}

// I4：被拒操作不留痕，且拒绝后分配器仍可正常使用。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	a, _ := api.New(60, 60)
	if err := a.Add("keep", 1, 6); err != nil {
		t.Fatal(err)
	}
	before := a.Allocate()
	bad := []struct {
		id       string
		cpu, mem int64
	}{
		{"", 1, 1}, {"keep", 1, 1}, {"x", -3, 1}, {"y", 1, -3}, {"q", 0, 0},
	}
	for _, b := range bad {
		if a.Add(b.id, b.cpu, b.mem) == nil {
			t.Fatalf("Add(%v) unexpectedly accepted", b)
		}
	}
	after := a.Allocate() // 被拒后仍可正常使用
	if len(after) != 1 || drf.Cmp(after["keep"], before["keep"]) != 0 {
		t.Fatalf("state changed by rejected ops: before=%v after=%v", before, after)
	}
	if err := a.Add("fresh", 4, 1); err != nil {
		t.Fatalf("allocator unusable after rejects: %v", err)
	}
	if got := a.Allocate(); drf.Cmp(got["keep"], frac(8, 1)) != 0 || drf.Cmp(got["fresh"], frac(12, 1)) != 0 {
		t.Fatalf("post-reject allocation wrong: %v", got)
	}
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// 并发：N 个 goroutine 以随机顺序各 Add 一个不同 id（不用 sleep），
// 全部结束后结果与顺序 Add 完全一致且满足两条硬约束。
func TestConcurrentAdd(t *testing.T) {
	for _, n := range []int{16, 128, 512} {
		ids := make([]int, n)
		for i := range ids {
			ids[i] = i
		}
		rand.New(rand.NewSource(int64(n))).Shuffle(n, func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
		conc, _ := api.New(1000, 1000)
		var wg sync.WaitGroup
		for _, i := range ids {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if err := conc.Add("t"+strconv.Itoa(i), int64(i%5), int64(i%7)+1); err != nil {
					t.Errorf("concurrent Add %d: %v", i, err)
				}
			}(i)
		}
		wg.Wait()
		seq, _ := api.New(1000, 1000)
		for i := 0; i < n; i++ {
			if err := seq.Add("t"+strconv.Itoa(i), int64(i%5), int64(i%7)+1); err != nil {
				t.Fatal(err)
			}
		}
		got, want := conc.Allocate(), seq.Allocate()
		if len(got) != n {
			t.Fatalf("n=%d: got %d ids, want %d", n, len(got), n)
		}
		var uC, uM drf.Frac
		for i := 0; i < n; i++ {
			id := "t" + strconv.Itoa(i)
			if drf.Cmp(got[id], want[id]) != 0 {
				t.Fatalf("n=%d: %s concurrent=%v sequential=%v", n, id, got[id], want[id])
			}
			uC = drf.Add(uC, drf.Mul(got[id], frac(int64(i%5), 1)))
			uM = drf.Add(uM, drf.Mul(got[id], frac(int64(i%7)+1, 1)))
		}
		if drf.Cmp(uC, frac(1000, 1)) > 0 || drf.Cmp(uM, frac(1000, 1)) > 0 {
			t.Fatalf("n=%d: hard constraints violated cpu=%v mem=%v", n, uC, uM)
		}
	}
}
