package txnset

import (
	"bytes"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func TestMergeAndCanonical(t *testing.T) {
	s := New()
	mustMerge(t, s, "b:10-20,1;b:30;a:5")
	mustMerge(t, s, "a:6-8;b:2")
	want := "a:5-8;b:1-2,10-20,30"
	if got := s.Canonical(); got != want {
		t.Fatalf("canonical = %q, want %q", got, want)
	}
}

func TestMergeRejectedLeavesStateUntouched(t *testing.T) {
	s := New()
	mustMerge(t, s, "a:1-10")
	before := s.Canonical()

	// 前段合法、后段非法：整段必须拒绝，前段不得并入。
	bad := []string{
		"a:20;b:1 1",             // 空白
		"b:1;c.bad:2",            // 标识非法
		"a:18446744073709551616", // 数值溢出
		"a:9-1",                  // 区间倒置
	}
	for _, in := range bad {
		if err := s.Merge(in); err == nil {
			t.Fatalf("Merge(%q) expected error", in)
		}
		if got := s.Canonical(); got != before {
			t.Fatalf("after rejected Merge(%q), state changed: %q != %q", in, got, before)
		}
	}
}

func TestDifferenceDoesNotChangeState(t *testing.T) {
	src, _ := Parse("a:1-10,20-30;b:1-5")
	local, _ := Parse("a:3-7,25-40;c:99")
	srcBefore := src.Canonical()
	localBefore := local.Canonical()

	rem := src.Difference(local)
	want := "a:1-2,8-10,20-24;b:1-5"
	if got := rem.Canonical(); got != want {
		t.Fatalf("difference = %q, want %q", got, want)
	}
	if got := src.Canonical(); got != srcBefore {
		t.Fatalf("left operand mutated: %q != %q", got, srcBefore)
	}
	if got := local.Canonical(); got != localBefore {
		t.Fatalf("right operand mutated: %q != %q", got, localBefore)
	}
	if rem == src || rem == local {
		t.Fatal("difference returned an operand instead of a new set")
	}
}

func TestDifferenceEndpoints(t *testing.T) {
	src, _ := Parse("a:1-100")
	local, _ := Parse("a:1,50,100")
	if got := src.Difference(local).Canonical(); got != "a:2-49,51-99" {
		t.Fatalf("endpoint removal = %q", got)
	}
}

func TestDifferenceBySourceIsIndependent(t *testing.T) {
	src, _ := Parse("a:1-5;b:1-5")
	local, _ := Parse("a:2-3;c:1-5")
	// b 在 local 中不存在来源 -> 原样保留；c 不在 src 中 -> 不产生结果。
	if got := src.Difference(local).Canonical(); got != "a:1,4-5;b:1-5" {
		t.Fatalf("got %q", got)
	}
}

func TestResumeScenario(t *testing.T) {
	// 断点续传：源端已执行集合 - 本地已执行集合 = 仍需补偿的事务。
	source, _ := Parse("mysql-a:100-200,201-300,500;pg-b:1-9")
	local, _ := Parse("mysql-a:100-250;pg-b:1-9")
	got := source.Difference(local).Canonical()
	want := "mysql-a:251-300,500"
	if got != want {
		t.Fatalf("resume diff = %q, want %q", got, want)
	}
}

func TestArbitrarySplitAndOrderByteIdentical(t *testing.T) {
	chunks := []string{
		"z:9,1-2", "a:1-5,3-8", "z:3", "a:20", "m:100-101,102-200",
		"a:7-9", "z:10,11-20", "m:1", "a:0",
	}
	// 参考：一次性合并
	ref := New()
	for _, c := range chunks {
		mustMerge(t, ref, c)
	}
	want := ref.Canonical()

	// 同一切片集合的多种排列都必须逐字节相等。
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 20; iter++ {
		perm := rng.Perm(len(chunks))
		s := New()
		for _, idx := range perm {
			mustMerge(t, s, chunks[idx])
		}
		if got := s.Canonical(); got != want {
			t.Fatalf("iter %d perm=%v:\n got %q\nwant %q", iter, perm, got, want)
		}
	}

	// 任意切分：把同样的区间重新切成不同大小的批次。
	flat := []string{"a:1-8", "a:0", "a:9", "a:20", "m:1", "m:100-200", "z:1-3", "z:9-11", "z:12-20"}
	for step := 1; step <= 4; step++ {
		s := New()
		for i := 0; i < len(flat); i += step {
			end := i + step
			if end > len(flat) {
				end = len(flat)
			}
			// 通过 Merge 逐批并入。
			if err := s.Merge(joinChunks(flat[i:end])); err != nil {
				t.Fatalf("step %d merge: %v", step, err)
			}
		}
		if got := s.Canonical(); got != want {
			t.Fatalf("step %d: got %q want %q", step, got, want)
		}
	}
}

func TestConcurrentMergeDisjointEqualsSingle(t *testing.T) {
	// 各 goroutine 合并互不重叠的来源/区间。
	var parts []string
	for i := 0; i < 16; i++ {
		parts = append(parts, fmt.Sprintf("src%02d:%d-%d", i, i*100, i*100+49))
	}

	seq := New()
	for _, p := range parts {
		mustMerge(t, seq, p)
	}
	want := seq.Canonical()

	par := New()
	var wg sync.WaitGroup
	for _, p := range parts {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			if err := par.Merge(p); err != nil {
				t.Errorf("concurrent merge: %v", err)
			}
		}(p)
	}
	wg.Wait()
	if got := par.Canonical(); got != want {
		t.Fatalf("concurrent = %q, want %q", got, want)
	}
}

func TestConcurrentMixedOps(t *testing.T) {
	s, _ := Parse("a:1-1000;b:1-10")
	stop := make(chan struct{})
	var wg sync.WaitGroup

	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			i := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				i++
				// 一半成功一半被拒，状态最终仍须是合法规范文本。
				_ = s.Merge(fmt.Sprintf("c%d:1-3", w))
				if i%3 == 0 {
					if err := s.Merge("bad space:1"); err == nil {
						t.Error("expected rejection")
					}
				}
			}
		}(w)
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local, _ := Parse("a:500-600")
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = s.Canonical()
				_ = s.Difference(local)
				_ = s.Sources()
			}
		}()
	}
	// 跑一小段时间后停（竞态由 -race 检测）。
	for k := 0; k < 50; k++ {
		_ = s.Canonical()
	}
	close(stop)
	wg.Wait()

	got := s.Canonical()
	reparsed, err := Parse(got)
	if err != nil {
		t.Fatalf("canonical output %q is not reparseable: %v", got, err)
	}
	if reparsed.Canonical() != got {
		t.Fatalf("canonical not a fixed point: %q vs %q", reparsed.Canonical(), got)
	}
}

func TestCanonicalIdempotentRoundTrip(t *testing.T) {
	s, _ := Parse("z:5,1-1,1,2,3;a:9;a:4-7")
	once := s.Canonical()
	twice, err := Parse(once)
	if err != nil {
		t.Fatalf("reparse %q: %v", once, err)
	}
	if twice.Canonical() != once {
		t.Fatalf("not idempotent: %q vs %q", twice.Canonical(), once)
	}
}

func TestAccessorsAndCopy(t *testing.T) {
	s, _ := Parse("b:1-2,5;a:9")
	if s.Len() != 2 {
		t.Fatalf("Len = %d", s.Len())
	}
	if got := fmt.Sprint(s.Sources()); got != "[a b]" {
		t.Fatalf("Sources = %s", got)
	}
	ivs := s.Intervals("b")
	ivs[0] = Interval{999, 999}
	if got := s.Canonical(); got != "a:9;b:1-2,5" {
		t.Fatalf("mutation through Intervals leaked: %q", got)
	}
	c := s.Copy()
	c.Merge("a:100")
	if s.Canonical() == c.Canonical() {
		t.Fatal("Copy shares state with original")
	}
	if New().Canonical() != "" {
		t.Fatal("empty set must render to empty string")
	}
}

func TestLoggingEmitted(t *testing.T) {
	var buf bytes.Buffer
	SetLogOutput(&buf)
	defer SetLogOutput(nil)
	if err := New().Merge("a:1-3,2-5"); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse("a:1-3"); err != nil {
		t.Fatal(err)
	}
	log := buf.String()
	for _, want := range []string{"merge", "input=", "after=", "decision", "parse"} {
		if !bytes.Contains([]byte(log), []byte(want)) {
			t.Fatalf("log missing %q:\n%s", want, log)
		}
	}
}

func mustMerge(t *testing.T, s *Set, text string) {
	t.Helper()
	if err := s.Merge(text); err != nil {
		t.Fatalf("Merge(%q): %v", text, err)
	}
}

func joinChunks(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ";"
		}
		out += p
	}
	return out
}
