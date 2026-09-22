package equivalence_test

import (
	"bytes"
	"math/rand"
	"testing"

	"ontology/equivalence"
)

func serialize(classes [][]string) []byte {
	var buf bytes.Buffer
	for _, class := range classes {
		for i, id := range class {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.WriteString(id)
		}
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// 多种合并顺序 + 重复导出，Classes 输出必须逐字节一致、确定排序。
func TestClassesDeterministic(t *testing.T) {
	pairs := samplePairs()
	var baseline []byte

	for seed := int64(0); seed < 20; seed++ {
		uf := equivalence.New()
		order := rand.New(rand.NewSource(seed + 100)).Perm(len(pairs))
		for _, idx := range order {
			p := pairs[idx]
			if _, _, err := uf.Union(p[0], p[1]); err != nil {
				t.Fatal(err)
			}
		}
		for repeat := 0; repeat < 5; repeat++ {
			got := serialize(uf.Classes())
			if baseline == nil {
				baseline = got
				continue
			}
			if !bytes.Equal(got, baseline) {
				t.Fatalf("seed %d 第 %d 次导出与基线不一致:\n%s\n%s", seed, repeat, got, baseline)
			}
		}
	}
}

func TestClassesOrdering(t *testing.T) {
	uf := equivalence.New()
	for _, p := range [][2]string{{"d", "c"}, {"a", "b"}, {"c", "a"}, {"z", "y"}, {"m", "n"}} {
		if _, _, err := uf.Union(p[0], p[1]); err != nil {
			t.Fatal(err)
		}
	}

	got := uf.Classes()
	want := [][]string{
		{"a", "b", "c", "d"},
		{"m", "n"},
		{"y", "z"},
	}

	if len(got) != len(want) {
		t.Fatalf("类数 = %d, 期望 %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("第 %d 类 %v，期望 %v", i, got[i], want[i])
		}
		for j := range want[i] {
			if got[i][j] != want[i][j] {
				t.Fatalf("第 %d 类顺序 %v，期望 %v", i, got[i], want[i])
			}
		}
	}
}

func TestClassesEmptyAndSingle(t *testing.T) {
	uf := equivalence.New()
	if classes := uf.Classes(); len(classes) != 0 {
		t.Fatalf("空集合应为零类，得到 %v", classes)
	}
	uf.Add("")
	uf.Add("solo")
	got := uf.Classes()
	if len(got) != 2 || got[0][0] != "" || got[1][0] != "solo" {
		t.Fatalf("单元素类导出异常: %q", got)
	}
}
