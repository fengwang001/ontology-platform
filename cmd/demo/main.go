// Command demo 逐条打印增量左外连接的判定（OK/FAIL），不读参数不联网；任一 FAIL 退出码非 0。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"strconv"
	"sync"

	"ontology/api"
	"ontology/jstate"
)

var failed bool

func report(n string, ok bool, d string) {
	failed = failed || !ok
	fmt.Printf("%s %s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], n, d)
}
func o(op byte, l, r string) api.Out { return api.Out{Op: op, LID: l, RID: r} }
func ch(s jstate.Side, op byte, id, k string) api.Change {
	return api.Change{Side: s, Op: op, ID: id, Key: k}
}
func fo(x api.Out) string {
	return string(x.Op) + "(" + x.LID + "," + map[bool]string{true: "NULL", false: x.RID}[x.RID == ""] + ")"
}
func naive(lm, rm map[string]string) []api.Out {
	byK := map[string][]string{}
	for id, k := range rm {
		byK[k] = append(byK[k], id)
	}
	ls := make([]string, 0, len(lm))
	for id := range lm {
		ls = append(ls, id)
	}
	sort.Strings(ls)
	out := []api.Out{}
	for _, l := range ls {
		rs := byK[lm[l]]
		if len(rs) == 0 {
			rs = []string{""}
		}
		sort.Strings(rs)
		for _, r := range rs {
			out = append(out, api.Out{LID: l, RID: r})
		}
	}
	return out
}
func main() {
	nine := []api.Change{ch(jstate.SideR, '+', "r1", "x"), ch(jstate.SideL, '+', "l1", "x"), ch(jstate.SideL, '+', "l2", "y"), ch(jstate.SideL, '+', "l3", "y"), ch(jstate.SideR, '+', "r2", "y"), ch(jstate.SideR, '+', "r3", "y"), ch(jstate.SideR, '-', "r2", ""), ch(jstate.SideR, '-', "r3", ""), ch(jstate.SideR, '-', "r1", "")}
	j, steps, detail := api.New(0), [][]api.Out{}, ""
	for i, c := range nine {
		out, err := j.Apply([]api.Change{c})
		if err != nil {
			report("九步推演", false, err.Error())
			os.Exit(1)
		}
		steps = append(steps, out)
		detail += strconv.Itoa(i+1) + ":"
		if len(out) == 0 {
			detail += "无"
		}
		for _, x := range out {
			detail += fo(x)
		}
		detail += " "
	}
	nineOK := reflect.DeepEqual(steps[4], []api.Out{o('-', "l2", ""), o('+', "l2", "r2"), o('-', "l3", ""), o('+', "l3", "r2")}) && reflect.DeepEqual(steps[5], []api.Out{o('+', "l2", "r3"), o('+', "l3", "r3")}) && reflect.DeepEqual(steps[7], []api.Out{o('-', "l2", "r3"), o('+', "l2", ""), o('-', "l3", "r3"), o('+', "l3", "")})
	report("九步推演(5/6/8判定)", nineOK, detail)
	report("R先于L", len(steps[0]) == 0 && reflect.DeepEqual(steps[1], []api.Out{o('+', "l1", "r1")}), "R先到无输出，L到→"+fo(steps[1][0]))
	rng, w := rand.New(rand.NewSource(20260924)), api.New(0)
	lm, rm := map[string]string{}, map[string]string{}
	refs := map[jstate.Side]map[string]string{jstate.SideL: lm, jstate.SideR: rm}
	ms, preOK := map[[2]string]int{}, true
	for n := 0; n < 200; n++ {
		s := []jstate.Side{jstate.SideL, jstate.SideR}[rng.Intn(2)]
		id, k := string(s)+strconv.Itoa(n), "k"+strconv.Itoa(rng.Intn(5))
		out, _ := w.Apply([]api.Change{ch(s, '+', id, k)})
		refs[s][id] = k
		for _, x := range out {
			kk := [2]string{x.LID, x.RID}
			if (x.Op == '+' && ms[kk] != 0) || (x.Op == '-' && ms[kk] != 1) || (x.RID != "" && ms[[2]string{x.LID, ""}] == 1) {
				preOK = false
			}
			ms[kk] = 1
			if x.Op == '-' {
				delete(ms, kk)
			}
		}
	}
	report("视图==朴素左外连接", reflect.DeepEqual(w.View(), naive(lm, rm)), "200条随机到达逐行相同")
	report("日志前缀自洽且NULL互斥", preOK, "每个前缀计数0/1且NULL互斥")
	e := api.New(2)
	e.Apply([]api.Change{ch(jstate.SideL, '+', "a", "x")})
	before := e.View()
	bads := [][]api.Change{{ch(jstate.SideL, '+', "", "x")}, {ch(jstate.SideL, '+', "a", "x")}, {ch(jstate.SideL, '-', "ghost", "")}, {ch(jstate.SideL, '+', "b", "x"), ch(jstate.SideR, '+', "c", "x")}}
	wants := []error{api.ErrInvalidChange, api.ErrDuplicateID, api.ErrMissingID, api.ErrTooManyRows}
	distinct, traceOK := map[error]bool{}, true
	for i, b := range bads {
		ret, err := e.Apply(b)
		if err != wants[i] || ret != nil || !reflect.DeepEqual(e.View(), before) {
			traceOK = false
		}
		distinct[wants[i]] = true
	}
	_, contErr := e.Apply([]api.Change{ch(jstate.SideR, '+', "ok", "z")})
	report("四类错误哨兵互不相同", len(distinct) == 4, "invalid/dup/missing/limit")
	report("被拒不留痕且仍可用", traceOK && contErr == nil, "视图日志不变、后续可提交")
	bigOK, expect := true, []api.Out{o('+', "lz", ""), o('-', "lz", ""), o('+', "lz", "rz")}
	for _, m := range []int{100, 1000, 10000} {
		z, lb, rb := api.New(0), make([]api.Change, 0, m), make([]api.Change, 0, m)
		for n := 0; n < m; n++ {
			d := "d" + strconv.Itoa(n)
			lb = append(lb, ch(jstate.SideL, '+', "l"+d, d))
			rb = append(rb, ch(jstate.SideR, '+', "r"+d, d))
		}
		z.Apply(lb)
		z.Apply(rb)
		z1, _ := z.Apply([]api.Change{ch(jstate.SideL, '+', "lz", "z")})
		z2, _ := z.Apply([]api.Change{ch(jstate.SideR, '+', "rz", "z")})
		bigOK = bigOK && reflect.DeepEqual(append(append([]api.Out{}, z1...), z2...), expect)
	}
	report("大m下z键输出与m无关", bigOK, fo(expect[0])+" "+fo(expect[1])+" "+fo(expect[2]))
	var wg sync.WaitGroup
	start, res := make(chan struct{}), make(chan bool, 16)
	wantV := w.View()
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res <- reflect.DeepEqual(w.View(), wantV) && w.SelfCheck() == nil
		}()
	}
	close(start)
	wg.Wait()
	close(res)
	concOK := true
	for ok := range res {
		concOK = concOK && ok
	}
	report("并发只读视图逐行一致", concOK, "16 goroutines race-clean")
	if failed {
		os.Exit(1)
	}
}
