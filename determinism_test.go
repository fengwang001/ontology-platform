package ontology

import (
	"fmt"
	"math/rand"
	"testing"
)

// unionPairs 是一组固定的合并操作，构成 3 个等价类：
// {a,b,c,d}、{e,f,g}、{h}（h 为孤立元素）。
var unionPairs = [][2]string{
	{"b", "c"},
	{"a", "b"},
	{"c", "d"},
	{"f", "g"},
	{"e", "f"},
}

var allIDs = []string{"a", "b", "c", "d", "e", "f", "g", "h"}

func snapshotFinds(t *testing.T, u *UnionFind) map[string]string {
	t.Helper()
	got := make(map[string]string, len(allIDs))
	for _, id := range allIDs {
		rep, err := u.Find(id)
		if err != nil {
			t.Fatalf("Find(%q) 出错: %v", id, err)
		}
		got[id] = rep
	}
	return got
}

// TestFindDeterministicAcrossPermutations 用同一批 Union 的
// 20 种不同执行顺序构造并查集，断言每个元素的 Find 结果逐个完全相同。
func TestFindDeterministicAcrossPermutations(t *testing.T) {
	const permutations = 20
	var reference map[string]string
	for p := 0; p < permutations; p++ {
		pairs := append([][2]string(nil), unionPairs...)
		rand.New(rand.NewSource(int64(p))).Shuffle(len(pairs), func(i, j int) {
			pairs[i], pairs[j] = pairs[j], pairs[i]
		})
		u := New()
		for _, id := range allIDs {
			u.Add(id)
		}
		for _, pair := range pairs {
			u.Union(pair[0], pair[1])
		}
		got := snapshotFinds(t, u)
		if p == 0 {
			reference = got
			continue
		}
		for _, id := range allIDs {
			if got[id] != reference[id] {
				t.Fatalf("排列 %d: Find(%q)=%q, 期望 %q", p, id, got[id], reference[id])
			}
		}
	}
	// 代表元必须恒为类内字典序最小 ID。
	want := map[string]string{
		"a": "a", "b": "a", "c": "a", "d": "a",
		"e": "e", "f": "e", "g": "e",
		"h": "h",
	}
	for id, rep := range want {
		if reference[id] != rep {
			t.Fatalf("Find(%q)=%q, 期望代表元 %q", id, reference[id], rep)
		}
	}
}

// TestLateSmallerIDBecomesRepresentative 先把大类建好，
// 最后并入一个字典序更小的新元素，代表元必须更新为新元素。
func TestLateSmallerIDBecomesRepresentative(t *testing.T) {
	u := New()
	// 先建一个 1000 个元素的大类，最小 ID 是 "m000"。
	for i := 0; i < 1000; i++ {
		u.Union("m000", fmt.Sprintf("m%03d", i))
	}
	if rep, err := u.Find("m999"); err != nil || rep != "m000" {
		t.Fatalf("合并前 Find(m999)=%q, %v; 期望 m000, nil", rep, err)
	}
	// 最后并入字典序更小的 "aaa"。
	u.Union("m500", "aaa")
	for _, id := range []string{"m000", "m500", "m999", "aaa"} {
		rep, err := u.Find(id)
		if err != nil {
			t.Fatalf("Find(%q) 出错: %v", id, err)
		}
		if rep != "aaa" {
			t.Fatalf("Find(%q)=%q, 期望代表元更新为 aaa", id, rep)
		}
	}
}

// TestRepresentativeIsLexicographicMinimum 随机合并后，
// 每个元素的代表元必须等于其等价类中的最小 ID。
func TestRepresentativeIsLexicographicMinimum(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	ids := make([]string, 200)
	for i := range ids {
		ids[i] = fmt.Sprintf("id-%03d", rng.Intn(400)) // 故意制造重复
	}
	u := New()
	for _, id := range ids {
		u.Add(id)
	}
	for i := 0; i < 500; i++ {
		u.Union(ids[rng.Intn(len(ids))], ids[rng.Intn(len(ids))])
	}
	for _, class := range u.Classes() {
		for _, id := range class {
			rep, err := u.Find(id)
			if err != nil {
				t.Fatalf("Find(%q) 出错: %v", id, err)
			}
			if rep != class[0] { // class 已按字典序排列
				t.Fatalf("Find(%q)=%q, 期望类内最小 ID %q", id, rep, class[0])
			}
		}
	}
}
