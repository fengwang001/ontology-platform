package api

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"

	"ontology/entry"
)

// SelfCheck 对内置操作序列核验四条不变量，可并发调用；全部通过返回 nil。
func SelfCheck() error {
	sv := New()
	sv[0].SetTerm(1)
	Append(sv[0], "a")
	Replicate(sv[0], sv[1], 0)
	CommitIndex(sv[0]) // 步 1
	Append(sv[0], "b")
	Replicate(sv[0], sv[1], 1) // 步 2
	sv[2].SetTerm(2)
	Append(sv[2], "x")
	Append(sv[2], "y") // 步 3、4
	sv[0].SetTerm(3)
	Replicate(sv[0], sv[1], 1)
	Append(sv[0], "z")
	Replicate(sv[0], sv[1], 2)             // 步 5
	if ci := CommitIndex(sv[0]); ci != 3 { // 不变量 3：老任期 b 由 z 间接提交
		return fmt.Errorf("boundary: ci=%d want 3", ci)
	}
	if err := Replicate(sv[0], sv[2], 0); err != nil { // 步 6
		return err
	}
	for i, s := range sv { // 不变量 2：已提交的 1..3 在所有副本上保留
		if l := Log(s); len(l) != 3 || l[2].Cmd != "z" {
			return fmt.Errorf("completeness: S%d=%v", i+1, l)
		}
	}
	return errors.Join(checkFail(), checkRandom()) // 不变量 4，以及 1、3（随机序列）
}

// checkFail 核验不变量 4：三类错误可判定、互不相同，被拒后状态不变且可继续用。
func checkFail() error {
	sv := New()
	sv[0].SetTerm(1)
	Append(sv[0], "a")
	sv[1].SetTerm(2)
	Append(sv[1], "x")
	b0, b1 := Log(sv[0]), Log(sv[1])
	got := []error{Append(sv[0], ""), Replicate(sv[0], sv[1], -1), Replicate(sv[0], sv[1], 2), Replicate(sv[0], sv[1], 1)}
	want := []error{ErrEmptyCmd, ErrPrevIndexRange, ErrPrevIndexRange, ErrPrevTermMismatch}
	if !slices.Equal(got, want) {
		return fmt.Errorf("failcases: got %v", got)
	}
	if !slices.Equal(Log(sv[0]), b0) || !slices.Equal(Log(sv[1]), b1) {
		return fmt.Errorf("rejected op changed state")
	}
	return Replicate(sv[0], sv[1], 0) // 被拒后仍可正常使用
}

// checkRandom 在随机操作序列上核验不变量 1（日志匹配）与 3（提交边界）。
func checkRandom() error {
	for seed := int64(0); seed < 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		sv := New()
		term := 0
		for op := 0; op < 100; op++ {
			a, b := rng.Intn(3), rng.Intn(3)
			if t := rng.Intn(3); t == 0 {
				term++
				sv[a].SetTerm(term) // 每次追加都换新任期：同 (term,index) 条目必唯一
				Append(sv[a], fmt.Sprintf("c%d", op))
			} else if t == 1 {
				Replicate(sv[a], sv[b], rng.Intn(4))
			} else if err := boundary(sv, a, Committed(sv[a]), CommitIndex(sv[a])); err != nil {
				return err
			}
			l := [3][]entry.Entry{Log(sv[0]), Log(sv[1]), Log(sv[2])}
			for _, p := range [][2]int{{0, 1}, {0, 2}, {1, 2}} {
				for k := 0; k < min(len(l[p[0]]), len(l[p[1]])); k++ {
					if l[p[0]][k].Term == l[p[1]][k].Term && !slices.Equal(l[p[0]][:k+1], l[p[1]][:k+1]) {
						return fmt.Errorf("log matching violated at %d", k+1)
					}
				}
			}
		}
	}
	return nil
}

// boundary 核验不变量 3：本次判定从 from 前进到 to 是合法的——
// to 若前进则其本身可直接提交，且 from 之后没有可直接提交却被漏掉的条目。
func boundary(sv [3]*Server, li, from, to int) error {
	l := [3][]entry.Entry{Log(sv[0]), Log(sv[1]), Log(sv[2])}
	direct := func(i int) bool {
		if i < 1 || i > len(l[li]) || l[li][i-1].Term != sv[li].Term() {
			return false
		}
		n := 0
		for j := range l {
			if len(l[j]) >= i && l[j][i-1].Term == l[li][i-1].Term {
				n++
			}
		}
		return n >= 2
	}
	if to < from || (to > from && !direct(to)) {
		return fmt.Errorf("commit boundary violated: %d -> %d", from, to)
	}
	for i := from + 1; i <= len(l[li]); i++ {
		if direct(i) && i > to {
			return fmt.Errorf("committable %d missed at ci=%d", i, to)
		}
	}
	return nil
}
