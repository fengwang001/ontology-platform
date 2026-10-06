package defassign

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

// gen 随机生成结构良好（配对）的 DSL 程序，嵌套深度受限以控制朴素枚举规模。
func gen(r *rand.Rand) string {
	vars := []string{"x", "y", "z"}
	var b strings.Builder
	b.WriteString("var " + strings.Join(vars, " ") + "\n")

	var emitBlock func(depth int, maxStmts int, inLoop, inTry bool)
	emitBlock = func(depth int, maxStmts int, inLoop, inTry bool) {
		n := r.Intn(maxStmts + 1)
		for i := 0; i < n; i++ {
			choices := []int{0, 0, 1, 1, 2, 2} // assign/use 加权
			if inLoop {
				choices = append(choices, 3, 4) // break / return
			} else {
				choices = append(choices, 4)
			}
			switch choices[r.Intn(len(choices))] {
			case 0:
				fmt.Fprintf(&b, " assign %s\n", vars[r.Intn(len(vars))])
			case 1:
				fmt.Fprintf(&b, " use %s\n", vars[r.Intn(len(vars))])
			case 3:
				b.WriteString(" break\n")
			case 4:
				b.WriteString(" return\n")
			case 2:
				if depth >= 3 {
					fmt.Fprintf(&b, " assign %s\n", vars[r.Intn(len(vars))])
					continue
				}
				switch r.Intn(3) {
				case 0:
					cc := []string{"if", "iftrue", "iffalse"}[r.Intn(3)]
					b.WriteString(" " + cc + "\n")
					emitBlock(depth+1, 3, inLoop, inTry)
					if r.Intn(2) == 0 {
						b.WriteString(" else\n")
						emitBlock(depth+1, 3, inLoop, inTry)
					}
					b.WriteString(" end\n")
				case 1:
					kw := "loop"
					if r.Intn(2) == 0 {
						kw = "loop1"
					}
					b.WriteString(" " + kw + "\n")
					emitBlock(depth+1, 3, true, inTry)
					b.WriteString(" end\n")
				case 2:
					b.WriteString(" try\n")
					emitBlock(depth+1, 3, inLoop, true)
					nh := r.Intn(2)
					for h := 0; h < nh; h++ {
						b.WriteString(" handler\n")
						emitBlock(depth+1, 2, inLoop, true)
					}
					if r.Intn(2) == 0 {
						b.WriteString(" cleanup\n")
						emitBlock(depth+1, 2, inLoop, true)
					}
					b.WriteString(" end\n")
				}
			}
		}
	}
	emitBlock(0, 6, false, false)
	return b.String()
}

func canonDiags(rep *Report) []string {
	out := make([]string, len(rep.Diags))
	for i, d := range rep.Diags {
		ws := append([]string(nil), d.Witness...)
		sort.Strings(ws)
		out[i] = fmt.Sprintf("%d:%d:%s:{%s}", d.Kind, d.At, d.Var, strings.Join(ws, ","))
	}
	sort.Strings(out)
	return out
}

// TestNaiveDifferential：随机程序上传播器诊断集合必须与朴素枚举一致。
// 每个用例都打印输入、两边输出与一致性判定（-v 查看依据）。
func TestNaiveDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	r := rand.New(rand.NewSource(20261006))
	const N = 1500
	mism := 0
	for i := 0; i < N; i++ {
		src := gen(r)
		rep, err := CheckText("rand", src)
		if err != nil {
			t.Fatalf("generated invalid program: %v\n%s", err, src)
		}
		nrep, err := NaiveCheck(mustParse(t, src))
		if err != nil {
			t.Fatalf("naive rejected valid program: %v\n%s", err, src)
		}
		got, want := canonDiags(rep), canonDiags(nrep)
		if strings.Join(got, ";") != strings.Join(want, ";") {
			mism++
			if mism <= 5 {
				t.Errorf("MISMATCH on:\n%s--- analyzer: %v\n--- naive:    %v", src, got, want)
			}
		}
		t.Logf("input #%d:\n%sanalyzer=%v naive=%v match=%v",
			i, src, got, want, strings.Join(got, ";") == strings.Join(want, ";"))
	}
}

func mustParse(t *testing.T, src string) *Program {
	t.Helper()
	p, err := Parse("rand", src)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, src)
	}
	return p
}

// TestDeterministicAndConcurrent：同一程序重复检查字节一致；多程序并发互不影响。
func TestDeterministicAndConcurrent(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	var progs []string
	for i := 0; i < 32; i++ {
		progs = append(progs, gen(r))
	}
	base := make([]string, len(progs))
	for i, src := range progs {
		rep, err := CheckText("p", src)
		if err != nil {
			t.Fatal(err)
		}
		base[i] = rep.Text()
	}
	var wg sync.WaitGroup
	for round := 0; round < 4; round++ {
		for i, src := range progs {
			wg.Add(1)
			go func(i int, src string) {
				defer wg.Done()
				rep, err := CheckText("p", src)
				if err != nil {
					t.Error(err)
					return
				}
				if rep.Text() != base[i] {
					t.Errorf("nondeterministic output for program %d", i)
				}
			}(i, src)
		}
	}
	wg.Wait()
}

// TestSortedDiagnostics：诊断按出现位置（再类别、变量）排序。
func TestSortedDiagnostics(t *testing.T) {
	src := "var x y\nuse y\nuse x\n"
	rep := mustCheck(t, src)
	if len(rep.Diags) != 2 || rep.Diags[0].At > rep.Diags[1].At {
		t.Fatalf("diagnostics not sorted:\n%s", rep.Text())
	}
}
