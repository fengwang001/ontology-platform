// Command demo 非参数化地打印各条不变量的 OK/FAIL 核验结果。
package main

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os/exec"

	"ontology/balance"
	"ontology/longest"
	"ontology/scan"
)

func ok(cond bool) string {
	if cond {
		return "OK"
	}
	return "FAIL"
}

// naiveBalanced 仅用于演示与测试对照：朴素枚举所有子串逐个判配平。
func naiveBalanced(s string) bool { return balance.IsBalanced(s) }

func naiveLongest(s string) (int, int) {
	start, length := 0, 0
	for i := 0; i < len(s); i++ {
		for j := i + 2; j <= len(s); j += 2 {
			if naiveBalanced(s[i:j]) && j-i > length {
				start, length = i, j-i
			}
		}
	}
	return start, length
}

func main() {
	rng := rand.New(rand.NewPCG(1, 2))
	agree := true
	for t := 0; t < 300; t++ {
		b := make([]byte, rng.IntN(40))
		for i := range b {
			b[i] = "()"[rng.IntN(2)]
		}
		sol, _ := longest.New(1 << 20)
		st, ln, _ := sol.Longest(string(b))
		ns, nl := naiveLongest(string(b))
		if st != ns || ln != nl {
			agree = false
		}
	}
	fmt.Println("naive cross-check on random:", ok(agree))
	sol, _ := longest.New(1 << 20)
	st, ln, _ := sol.Longest(")()())")
	fmt.Println(`")()())" -> (start=1,len=4):`, ok(st == 1 && ln == 4))
	fmt.Println("empty -> (0,0):", ok(func() bool {
		a, b, _ := sol.Longest("")
		return a == 0 && b == 0
	}()))
	_, allLeft, _ := sol.Longest("((((")
	_, allRight, _ := sol.Longest("))))")
	fmt.Println("all-left/all-right len=0:", ok(allLeft == 0 && allRight == 0))
	tieStart, tieLen, _ := sol.Longest("()())(())")
	fmt.Println("tie longest -> leftmost (0,4):", ok(tieStart == 0 && tieLen == 4))
	r := balance.Check(")(")
	fmt.Println("first error index 0 UnexpectedRight:",
		ok(r.Failure == balance.UnexpectedRight && r.Index == 0))
	_, _, e1 := sol.Longest("(a)")
	_, e2 := longest.New(0)
	bad, e3 := longest.New(4)
	_, _, e3b := bad.Longest("(())(")
	fmt.Println("three distinct sentinel errors:",
		ok(errors.Is(e1, scan.ErrIllegalChar) && e2 == longest.ErrInvalidLimit &&
			e3 == nil && e3b == longest.ErrTooLong))
	cmd := exec.Command("go", "test", "-count=1", "-run", "TestCharCountExactlyN", "./longest")
	cmd.Env = append(cmd.Environ(), "GOCACHE=/tmp/gocache")
	fmt.Println("char visits == n (1000/100000 x4):", ok(cmd.Run() == nil))
	var starts, lens [64]int
	done := make(chan bool, 64)
	for g := 0; g < 64; g++ {
		go func(id int) {
			s, l, _ := sol.Longest("())(())())")
			starts[id], lens[id] = s, l
			done <- true
		}(g)
	}
	for g := 0; g < 64; g++ {
		<-done
	}
	same := true
	for g := 1; g < 64; g++ {
		if starts[g] != starts[0] || lens[g] != lens[0] {
			same = false
		}
	}
	fmt.Println("concurrent results identical:", ok(same))
}
