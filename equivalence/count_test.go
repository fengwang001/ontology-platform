package equivalence_test

import (
	"testing"

	"ontology/equivalence"
)

func TestClassCountBasics(t *testing.T) {
	uf := equivalence.New()
	if uf.ClassCount() != 0 {
		t.Fatalf("空集合类数 = %d", uf.ClassCount())
	}

	uf.Add("a")
	uf.Add("b")
	uf.Add("c")
	if uf.ClassCount() != 3 {
		t.Fatalf("三个单元素类，类数 = %d", uf.ClassCount())
	}

	if _, merged, err := uf.Union("a", "b"); err != nil || !merged {
		t.Fatalf("跨类合并应报告 merged=true")
	}
	if uf.ClassCount() != 2 {
		t.Fatalf("合并后类数 = %d，期望 2", uf.ClassCount())
	}

	if _, merged, err := uf.Union("a", "b"); err != nil || merged {
		t.Fatalf("同类合并应报告 merged=false")
	}
	if uf.ClassCount() != 2 {
		t.Fatalf("同类合并后类数 = %d，期望仍为 2", uf.ClassCount())
	}
}

// 重复 Union 一百万次：类数不变、任何代表元不变。
func TestRepeatedUnionOneMillion(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过百万次重复合并")
	}
	uf := equivalence.New()
	for _, p := range [][2]string{{"d", "e"}, {"a", "b"}, {"b", "d"}, {"x", "y"}, {"m", "a"}} {
		if _, _, err := uf.Union(p[0], p[1]); err != nil {
			t.Fatal(err)
		}
	}

	const reps = 1_000_000
	for i := 0; i < reps; i++ {
		switch i % 4 {
		case 0:
			if _, _, err := uf.Union("e", "m"); err != nil {
				t.Fatal(err)
			}
		case 1:
			if _, _, err := uf.Union("x", "y"); err != nil {
				t.Fatal(err)
			}
		case 2:
			if _, _, err := uf.Union("a", "a"); err != nil {
				t.Fatal(err)
			}
		default:
			if _, _, err := uf.Union("m", "d"); err != nil {
				t.Fatal(err)
			}
		}
	}

	if count := uf.ClassCount(); count != 2 {
		t.Fatalf("百万次重复 Union 后类数 = %d，期望 2", count)
	}
	want := map[string]string{
		"a": "a", "b": "a", "d": "a", "e": "a", "m": "a",
		"x": "x", "y": "x",
	}
	for id, expected := range want {
		if rep, err := uf.Find(id); err != nil || rep != expected {
			t.Fatalf("Find(%q) = (%q,%v)，期望 %q", id, rep, err, expected)
		}
	}
}
