package main

import (
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/rbuf"
	"ontology/spill"
)

var failed bool

func eqBlocks(a, b []int) bool { // 空切片与 nil 视为相等
	return len(a) == len(b) && (len(a) == 0 || reflect.DeepEqual(a, b))
}

func check(name string, cond bool) {
	if cond {
		fmt.Println("OK", name)
	} else {
		failed = true
		fmt.Println("FAIL", name)
	}
}

func main() {
	// spill：存取/删除/容量/块号不复用
	st := spill.New(2)
	n0 := st.Put(1, []string{"a", "b"})
	n1 := st.Put(2, []string{"c"})
	got := append(st.Get(n0), st.Get(n1)...)
	ok := st.Full() && n0 == 0 && n1 == 1 && got[0] == "a" && got[2] == "c"
	st.DeleteTx(1)
	ok = ok && st.Count() == 1 && eqBlocks(st.Blocks(), []int{1})
	ok = ok && st.Put(3, []string{"d"}) == 2 // 块号不复用
	check("spill", ok)

	// rbuf：第三节十二步，逐步核对 M 与溢写块号，末步核对输出行序
	rb := rbuf.New(4, spill.New(2))
	for _, tx := range []int{1, 2, 3} {
		rb.Begin(tx)
	}
	steps := []struct {
		op, row string
		tx, m   int
		blk     []int
	}{
		{"A", "a1", 1, 1, nil}, {"A", "b1", 2, 2, nil}, {"A", "b2", 2, 3, nil},
		{"A", "a2", 1, 4, nil}, {"A", "c1", 3, 3, []int{0}}, {"A", "a3", 1, 4, []int{0}},
		{"A", "c2", 3, 3, []int{0, 1}}, {"R", "", 2, 3, []int{0}}, {"A", "a4", 1, 4, []int{0}},
		{"A", "a5", 1, 2, []int{0, 2}}, {"A", "a6", 1, 3, []int{0, 2}}, {"C", "", 1, 2, nil},
	}
	ok = true
	for i, s := range steps {
		switch s.op {
		case "A":
			ok = ok && rb.Append(s.tx, s.row) == nil
		case "C":
			ok = ok && rb.Commit(s.tx) == nil
		case "R":
			ok = ok && rb.Rollback(s.tx) == nil
		}
		if rb.M() != s.m || !eqBlocks(rb.Blocks(), s.blk) {
			fmt.Printf("  step %d: M=%d blocks=%v\n", i+1, rb.M(), rb.Blocks())
			ok = false
		}
	}
	check("twelve-steps", ok)
	check("commit-order", reflect.DeepEqual(rb.Log(),
		[]string{"a1", "a2", "a3", "a4", "a5", "a6"}))

	// api：参数校验 + SelfCheck（随机对拍朴素参照、四类错误、被拒不变）
	if _, err := api.New(0, 1); err != api.ErrParam {
		check("api-selfcheck", false)
	} else {
		a, _ := api.New(4, 2)
		check("api-selfcheck", a.SelfCheck() == nil)
	}

	// 回滚后溢写块被清理；M==limit 不溢写
	b, _ := api.New(2, 3)
	b.Begin(1)
	b.Begin(2)
	for i := 0; i < 4; i++ {
		b.Append(1, fmt.Sprintf("a%d", i))
		b.Append(2, fmt.Sprintf("b%d", i))
	}
	nb := len(b.SpillBlocks())
	b.Rollback(2)
	ok = nb > 0 && len(b.SpillBlocks()) < nb
	c, _ := api.New(2, 1)
	c.Begin(1)
	c.Append(1, "p")
	c.Append(1, "q") // M==2==limit，不应溢写
	ok = ok && len(c.SpillBlocks()) == 0
	check("rollback-clean+limit-eq", ok)

	// 大 m 下堆访问数不随 m 线性增长（只报结论）
	check("spill-select-sublinear", rbuf.CheckSpillSelect() == nil)

	// 并发：N 个事务并发追加提交，行连续有序、存储清空
	d, _ := api.New(3, 1<<16)
	var wg sync.WaitGroup
	ok = true
	for tx := 1; tx <= 32; tx++ {
		ok = ok && d.Begin(tx) == nil
		wg.Add(1)
		go func(tx int) {
			defer wg.Done()
			for r := 0; r < 10; r++ {
				if d.Append(tx, fmt.Sprintf("t%02d-%d", tx, r)) != nil {
					ok = false
				}
			}
			if d.Commit(tx) != nil {
				ok = false
			}
		}(tx)
	}
	wg.Wait()
	log := d.Log()
	seen := map[string]bool{}
	for i := 0; i < len(log); i += 10 {
		tx := log[i][:3]
		if seen[tx] {
			ok = false
		}
		seen[tx] = true
		for j := 0; j < 10; j++ {
			if log[i+j] != fmt.Sprintf("%s-%d", tx, j) {
				ok = false
			}
		}
	}
	check("concurrent", ok && len(log) == 320 && len(d.SpillBlocks()) == 0)

	if failed {
		os.Exit(1)
	}
}
