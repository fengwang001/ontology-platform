package trie

import (
	"errors"
	"math/rand"
	"slices"
	"strings"
	"testing"
)

func TestCheckString(t *testing.T) {
	if err := CheckString(""); !errors.Is(err, ErrEmptyString) {
		t.Errorf("empty: %v", err)
	}
	for _, s := range []string{"hello", "中文串", "a"} {
		if err := CheckString(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	for s, off := range map[string]int{"a\xffb": 1, "\xc3\x28": 0} {
		err := CheckString(s)
		var u *UTF8Error
		if !errors.Is(err, ErrInvalidUTF8) || !errors.As(err, &u) || u.Offset != off {
			t.Errorf("%q: err=%v want offset %d", s, err, off)
		}
	}
}

// randStr 生成 n 字节、字母表大小 alpha 的随机串。
func randStr(rng *rand.Rand, n, alpha int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + rng.Intn(alpha))
	}
	return string(b)
}

// 复杂度：单次 Insert/Delete 访问节点数 == 路径长 L+1，不随库规模 m 增长。
func TestInsertDeleteVisitsIndependentOfM(t *testing.T) {
	const L = 12
	probe := strings.Repeat("z", L)
	for _, m := range []int{100, 1000, 10000} {
		tr, rng := New(), rand.New(rand.NewSource(int64(m)))
		for i := 0; i < m; i++ {
			_ = tr.Insert(randStr(rng, L, 10), 1)
		}
		v0 := tr.visited
		if err := tr.Insert(probe, 1); err != nil {
			t.Fatal(err)
		}
		ins := tr.visited - v0
		v0 = tr.visited
		if err := tr.Delete(probe); err != nil {
			t.Fatal(err)
		}
		del := tr.visited - v0
		if ins > L+1 || del > L+1 {
			t.Errorf("m=%d: visits ins=%d del=%d, want <= %d", m, ins, del, L+1)
		}
	}
}

// audit 朴素重数子树终端数，校验引用计数自洽且无未剪的零引用节点。
func audit(n *node, isRoot bool) (int, bool) {
	sum, ok := 0, true
	if n.terminal {
		sum = 1
	}
	for _, c := range n.children {
		s, o := audit(c, false)
		sum += s
		ok = ok && o
	}
	if n.refs != sum || (!isRoot && n.refs == 0) {
		ok = false
	}
	return sum, ok
}

func TestRefcountInvariant(t *testing.T) {
	tr, m, rng := New(), map[string]int{}, rand.New(rand.NewSource(7))
	for i := 0; i < 500; i++ {
		s := randStr(rng, 1+rng.Intn(6), 4)
		if rng.Intn(3) > 0 {
			f := 1 + rng.Intn(5)
			m[s] += f
			_ = tr.Insert(s, f)
		} else if _, ok := m[s]; ok {
			delete(m, s)
			if err := tr.Delete(s); err != nil {
				t.Fatal(err)
			}
		}
		if sum, ok := audit(tr.root, true); !ok || sum != len(m) || tr.Count() != len(m) {
			t.Fatalf("step %d: sum=%d |m|=%d count=%d", i, sum, len(m), tr.Count())
		}
	}
}

func TestRejectedOpsNoSideEffects(t *testing.T) {
	tr := New()
	_ = tr.Insert("apple", 3)
	_ = tr.Insert("app", 1)
	snap := func() (int, []Candidate) {
		a, _ := tr.Collect("a")
		return tr.Count(), a
	}
	c0, a0 := snap()
	bads := []error{tr.Insert("", 1), tr.Insert("x\xff", 1), tr.Delete(""), tr.Delete("absent")}
	wants := []error{ErrEmptyString, ErrInvalidUTF8, ErrEmptyString, ErrNotFound}
	for i := range bads {
		if !errors.Is(bads[i], wants[i]) {
			t.Errorf("op %d: got %v, want %v", i, bads[i], wants[i])
		}
	}
	if errors.Is(bads[1], ErrEmptyString) || errors.Is(bads[3], ErrEmptyString) ||
		errors.Is(bads[3], ErrInvalidUTF8) || errors.Is(bads[0], ErrNotFound) {
		t.Error("sentinels not mutually distinct")
	}
	if c1, a1 := snap(); c0 != c1 || !slices.Equal(a0, a1) {
		t.Error("rejected ops changed state")
	}
}

func TestCollectOnlyPrefix(t *testing.T) {
	tr := New()
	words := []string{"app", "apple", "apricot", "application", "banana", "band"}
	for i, w := range words {
		_ = tr.Insert(w, i+1)
	}
	for _, p := range []string{"a", "ap", "app", "appl", "b", "ban", "x"} {
		got, _ := tr.Collect(p)
		want := 0
		for _, w := range words {
			if strings.HasPrefix(w, p) {
				want++
			}
		}
		if len(got) != want {
			t.Errorf("prefix %q: got %d candidates, want %d", p, len(got), want)
		}
		for _, c := range got {
			if !strings.HasPrefix(c.Word, p) {
				t.Errorf("prefix %q: stray candidate %q", p, c.Word)
			}
		}
	}
}
