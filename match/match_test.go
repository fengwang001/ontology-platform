package match

import (
	"errors"
	"math/rand"
	"testing"
)

// TestLinearComparisons 钉住不变量 3：匹配阶段字符比较总次数不随 n·m 增长，
// 对每档文本长度 m 都不超过 2·(m+n)。直接读非导出字段 cmps（包内测试）。
func TestLinearComparisons(t *testing.T) {
	const n = 8
	rng := rand.New(rand.NewSource(42))
	pat := make([]byte, n)
	for i := range pat {
		pat[i] = byte('a' + rng.Intn(3)) // 小字母表，制造大量失配回退
	}
	for _, c := range []struct{ m int }{{100}, {500}, {1000}, {5000}, {10000}} {
		text := make([]byte, c.m)
		for i := range text {
			text[i] = byte('a' + rng.Intn(3))
		}
		mt := New(pat, 1<<30)
		if _, err := mt.Feed(text); err != nil {
			t.Fatalf("m=%d: %v", c.m, err)
		}
		if bound := int64(2 * (c.m + n)); mt.cmps > bound {
			t.Errorf("m=%d: cmps=%d 超过 2*(m+n)=%d，疑似退化为 O(n*m)", c.m, mt.cmps, bound)
		}
	}
}

// TestFeedAtomicOnOverflow 钉住不变量 4：超限的一批整体不生效，
// j、consumed、matches、cmps 全部不变，且之后仍可正常使用。
func TestFeedAtomicOnOverflow(t *testing.T) {
	m := New([]byte("aa"), 1)
	if _, err := m.Feed([]byte("xa")); err != nil { // j=1, consumed=2, 无匹配
		t.Fatal(err)
	}
	j0, pos0, cmps0 := m.j, m.consumed, m.cmps
	if _, err := m.Feed([]byte("aaa")); !errors.Is(err, ErrTooManyMatches) {
		t.Fatalf("want ErrTooManyMatches, got %v", err)
	}
	if m.j != j0 || m.consumed != pos0 || m.cmps != cmps0 || len(m.matches) != 0 {
		t.Errorf("被拒后状态改变: j %d->%d consumed %d->%d cmps %d->%d matches=%v",
			j0, m.j, pos0, m.consumed, cmps0, m.cmps, m.matches)
	}
	got, err := m.Feed([]byte("a")) // 被拒后仍可继续：pos=2 处完成匹配
	if err != nil || len(got) != 1 || got[0] != 2 {
		t.Errorf("复用失败: got=%v err=%v", got, err)
	}
	if all := m.Matches(); len(all) != 1 || all[0] != 2 {
		t.Errorf("Matches=%v, want [2]", all)
	}
}

// TestEmptyChunk 空 chunk 合法：无新匹配、无错误、状态不变。
func TestEmptyChunk(t *testing.T) {
	for _, chunk := range [][]byte{nil, {}} {
		m := New([]byte("ab"), 10)
		got, err := m.Feed(chunk)
		if err != nil || len(got) != 0 || m.consumed != 0 || m.j != 0 {
			t.Errorf("chunk=%v: got=%v err=%v consumed=%d j=%d", chunk, got, err, m.consumed, m.j)
		}
	}
}
