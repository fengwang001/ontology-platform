package ontology

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// renderClasses 把 Classes() 的结果渲染成单个字符串，便于逐字节比较。
func renderClasses(classes [][]string) string {
	var sb strings.Builder
	for _, class := range classes {
		sb.WriteString(strings.Join(class, ","))
		sb.WriteString("\n")
	}
	return sb.String()
}

// TestClassesDeterministicOutput 同一批 Union 打乱顺序执行多次，
// Classes() 的输出必须逐字节相同，且类间、类内均按字典序排列。
func TestClassesDeterministicOutput(t *testing.T) {
	pairs := [][2]string{
		{"delta", "echo"}, {"alpha", "bravo"}, {"charlie", "delta"},
		{"foxtrot", "golf"}, {"bravo", "charlie"}, {"hotel", "india"},
		{"echo", "foxtrot"}, {"juliett", "kilo"},
	}
	solo := []string{"zulu", "yankee", "xray"}
	var reference string
	for trial := 0; trial < 30; trial++ {
		shuffled := append([][2]string(nil), pairs...)
		rand.New(rand.NewSource(int64(trial * 7919))).Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		u := New()
		solos := append([]string(nil), solo...)
		rand.New(rand.NewSource(int64(trial*104729 + 1))).Shuffle(len(solos), func(i, j int) {
			solos[i], solos[j] = solos[j], solos[i]
		})
		for _, id := range solos {
			u.Add(id)
		}
		for _, p := range shuffled {
			u.Union(p[0], p[1])
		}
		got := renderClasses(u.Classes())
		if trial == 0 {
			reference = got
			continue
		}
		if got != reference {
			t.Fatalf("第 %d 次打乱后 Classes() 输出不一致:\n got:\n%s\nwant:\n%s", trial, got, reference)
		}
	}
}

// TestClassesSorted 断言类按代表元字典序排列、类内成员按字典序排列。
func TestClassesSorted(t *testing.T) {
	u := New()
	u.Union("m", "b")
	u.Union("z", "q")
	u.Union("b", "s")
	u.Add("a")
	classes := u.Classes()
	if len(classes) != 3 {
		t.Fatalf("len(Classes())=%d, 期望 3", len(classes))
	}
	prevRep := ""
	for _, class := range classes {
		if !sort.StringsAreSorted(class) {
			t.Fatalf("类内成员未按字典序排列: %v", class)
		}
		if class[0] <= prevRep {
			t.Fatalf("类未按代表元字典序排列: %q 出现在 %q 之后", class[0], prevRep)
		}
		prevRep = class[0]
	}
	want := "a\nb,m,s\nq,z\n"
	if got := renderClasses(classes); got != want {
		t.Fatalf("Classes()=\n%s期望:\n%s", got, want)
	}
}

// TestChainFindHops 链式构造 v0..v20000 后连续 Find 一万次，
// 首次 Find 后的后续 Find 平均跳数必须小于 3。
// 没有路径压缩的实现无法通过该断言。
func TestChainFindHops(t *testing.T) {
	const n = 20000
	u := New()
	for i := 0; i < n; i++ {
		u.Union(fmt.Sprintf("v%d", i), fmt.Sprintf("v%d", i+1))
	}
	last := fmt.Sprintf("v%d", n)
	hopsBefore := u.TotalHops()
	if _, err := u.Find(last); err != nil {
		t.Fatalf("Find 出错: %v", err)
	}
	firstFindHops := u.TotalHops() - hopsBefore
	const repeats = 10000
	hopsBefore = u.TotalHops()
	for i := 0; i < repeats; i++ {
		if _, err := u.Find(last); err != nil {
			t.Fatalf("Find 出错: %v", err)
		}
	}
	subsequent := u.TotalHops() - hopsBefore
	avg := float64(subsequent) / repeats
	t.Logf("首次 Find 跳数=%d, 后续 %d 次平均跳数=%.4f", firstFindHops, repeats, avg)
	if avg >= 3 {
		t.Fatalf("后续 Find 平均跳数 %.4f >= 3, 路径压缩未生效", avg)
	}
}
