package sepkey

import (
	"bytes"
	"errors"
	"sync"
	"testing"
)

// 构造三块索引:
//
//	块0: last="abc"  next="aex"  sep="ac"
//	块1: last="af"   next="cz"   sep="b"
//	块2: last="d"    (Finish)    sep="e"
func newThreeBlockBuilder(t *testing.T) *Builder {
	t.Helper()
	var b Builder
	mustAdd(t, &b, []byte("abc"), []byte("aex"))
	mustAdd(t, &b, []byte("af"), []byte("cz"))
	mustFinish(t, &b, []byte("d"))
	want := [][]byte{[]byte("ac"), []byte("b"), []byte("e")}
	got := sepsOf(t, &b)
	if len(got) != len(want) {
		t.Fatalf("seps=%q, 期望 %q", got, want)
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("seps=%q, 期望 %q", got, want)
		}
	}
	return &b
}

// 对所有落在块内的键，Seek 返回其真实所在块。
func TestSeekWithinBlocks(t *testing.T) {
	b := newThreeBlockBuilder(t)
	cases := []struct {
		key  string
		want int
	}{
		// 块0 数据范围: (-∞, "abc"]
		{"", 0},
		{"a", 0},
		{"ab", 0},
		{"abc", 0},
		// 块0 与块1 之间、不超过 sep0="ac" 的键仍落块0
		{"abd", 0},
		{"ac", 0},
		// 块1 数据范围: ["aex", "af"]
		{"aex", 1},
		{"aez", 1},
		{"af", 1},
		// 块1 与块2 之间、不超过 sep1="b" 的键仍落块1
		{"ag", 1},
		{"b", 1},
		// 块2 数据范围: ["cz", "d"]
		{"bz", 2},
		{"c", 2},
		{"cz", 2},
		{"d", 2},
	}
	for _, tc := range cases {
		got, err := b.Seek([]byte(tc.key))
		if err != nil {
			t.Fatalf("Seek(%q) 出错: %v", tc.key, err)
		}
		if got != tc.want {
			t.Fatalf("Seek(%q)=%d, 期望块 %d", tc.key, got, tc.want)
		}
		t.Logf("Seek(%q)=%d 判定依据: 第一个 sep>=key 的分隔键为 seps[%d]", tc.key, got, got)
	}
}

// key 大于全部分隔键时返回越界。
func TestSeekOutOfRange(t *testing.T) {
	b := newThreeBlockBuilder(t)
	for _, key := range []string{"e\x00", "f", "\xff"} {
		idx, err := b.Seek([]byte(key))
		if !errors.Is(err, ErrOutOfRange) {
			t.Fatalf("Seek(%q): err=%v, 期望 ErrOutOfRange", key, err)
		}
		if idx != -1 {
			t.Fatalf("Seek(%q)=%d, 期望 -1", key, idx)
		}
	}
}

// 相同登记序列重放得到逐字节相同的分隔键。
func TestReplayDeterminism(t *testing.T) {
	ops := [][2][]byte{
		{[]byte("abcdefg"), []byte("abzzz")},
		{[]byte("ac"), []byte("b")},
		{[]byte("\xff\x01"), []byte("\xff\x02")},
	}
	finish := []byte("\xff\xffa")

	build := func() [][]byte {
		var b Builder
		for _, op := range ops {
			mustAdd(t, &b, op[0], op[1])
		}
		mustFinish(t, &b, finish)
		return b.Seps()
	}
	first := build()
	for i := 0; i < 5; i++ {
		again := build()
		if len(again) != len(first) {
			t.Fatalf("重放结果块数不同: %d vs %d", len(again), len(first))
		}
		for j := range first {
			if !bytes.Equal(first[j], again[j]) {
				t.Fatalf("第 %d 次重放 seps[%d]=%q, 首次为 %q", i, j, again[j], first[j])
			}
		}
	}
}

// AddBlock/Finish 与 Seek/Seps 并发调用，结果等价于某个串行顺序。
func TestConcurrent(t *testing.T) {
	var b Builder
	const blocks = 64

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := []byte{byte(id)}
			for {
				select {
				case <-stop:
					return
				default:
				}
				idx, err := b.Seek(key)
				if err == nil && (idx < 0 || idx >= len(b.Seps())) {
					t.Errorf("Seek 返回非法下标 %d", idx)
					return
				}
				if err != nil && !errors.Is(err, ErrNotFinished) && !errors.Is(err, ErrOutOfRange) {
					t.Errorf("Seek 返回未知错误 %v", err)
					return
				}
				_ = b.Seps()
			}
		}(g)
	}

	for i := 0; i < blocks; i++ {
		last := []byte{byte(i), 'l'}
		next := []byte{byte(i), 'n'}
		if err := b.AddBlock(last, next); err != nil {
			t.Fatalf("AddBlock 第 %d 块被拒绝: %v", i, err)
		}
	}
	if err := b.Finish([]byte{blocks, 'l'}); err != nil {
		t.Fatalf("Finish 被拒绝: %v", err)
	}
	close(stop)
	wg.Wait()

	// 与串行重放逐字节对比
	var ref Builder
	for i := 0; i < blocks; i++ {
		mustAdd(t, &ref, []byte{byte(i), 'l'}, []byte{byte(i), 'n'})
	}
	mustFinish(t, &ref, []byte{blocks, 'l'})
	got, want := b.Seps(), ref.Seps()
	if len(got) != len(want) {
		t.Fatalf("并发结果块数 %d, 串行为 %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("seps[%d]: 并发 %q != 串行 %q", i, got[i], want[i])
		}
	}
}

// Seps 返回副本，外部修改不影响构建器。
func TestSepsReturnsCopy(t *testing.T) {
	b := newThreeBlockBuilder(t)
	seps := b.Seps()
	seps[0][0] = 'X'
	again := b.Seps()
	if !bytes.Equal(again[0], []byte("ac")) {
		t.Fatalf("外部修改影响了内部分隔键: %q", again[0])
	}
}
