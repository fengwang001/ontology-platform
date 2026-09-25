package main

import "errors"
import "fmt"
import "os"
import "sync"

import "ontology/api"
import "ontology/cluster"
import "ontology/rnd"

var fails int

func ok(n string, c bool) {
	if !c {
		fails++
	}
	fmt.Println(map[bool]string{true: "OK  ", false: "FAIL "}[c] + n)
}

// add feeds ids to either cluster's or locator's AddNode method value.
func add(fn func(string) error, ids ...string) {
	for _, id := range ids {
		fn(id)
	}
}

// table is the section-3 fixed weight table, treated as hash output.
var table = map[string]map[string]uint64{
	"k1": {"A": 10, "B": 7, "C": 3, "D": 11},
	"k2": {"A": 4, "B": 9, "C": 5, "D": 3},
	"k3": {"A": 6, "B": 6, "C": 2, "D": 6},
	"k4": {"A": 8, "B": 2, "C": 9, "D": 7},
}

func tw(k, n string) uint64 { return table[k][n] }

type ofn func(string) (string, error)

func om(fn ofn, ks []string) map[string]string {
	m := map[string]string{}
	for _, k := range ks {
		m[k], _ = fn(k)
	}
	return m
}

func moved(a, b map[string]string) (n int) {
	for k := range a {
		if a[k] != b[k] {
			n++
		}
	}
	return
}

// skew sends the three special keys to A and every other key to B.
func skew(sp map[string]bool) func(string, string) uint64 {
	return func(k, n string) uint64 {
		if n == "A" {
			return map[bool]uint64{true: 100, false: 1}[sp[k]]
		}
		return map[string]uint64{"B": 50, "C": 2}[n]
	}
}

func main() {
	t1, _ := rnd.Pick("k3", []string{"A", "B", "C"}, tw) // 1. tie 6=6 -> min ID.
	ok("rnd 确定性; k3并列6=6取ID字典序最小->A", rnd.Weight("k3", "A") == rnd.Weight("k3", "A") && t1 == "A")
	c := cluster.New(tw) // 2. ①k1 ②k2 ③k3 ④k4 ⑤AddNode(D) ⑥RemoveNode(A).
	add(c.AddNode, "A", "B", "C")
	ks := []string{"k1", "k2", "k3", "k4"}
	pre := om(c.Owner, ks)
	c.AddNode("D")
	pd := om(c.Owner, ks)
	c.RemoveNode("A")
	pa := om(c.Owner, ks)
	ok("六步: ④A,B,A,C; ⑤仅k1:A->D; ⑥仅k3:A->B; 末态D/B/B/C",
		pre["k1"] == "A" && pre["k2"] == "B" && pre["k3"] == "A" && pre["k4"] == "C" &&
			moved(pre, pd) == 1 && pd["k1"] == "D" && pd["k3"] == "A" &&
			moved(pd, pa) == 1 && pa["k1"] == "D" && pa["k3"] == "B" && pa["k4"] == "C")
	l := api.New() // 3. Naive-scan equivalence; determinism, add-order free.
	ns := []string{"n1", "n2", "n3", "n4", "n5"}
	add(l.AddNode, ns...)
	good, serial := true, map[string]string{}
	for i := 0; i < 200; i++ {
		k := fmt.Sprintf("key-%04d", i)
		got, _ := l.Owner(k)
		want, _ := rnd.Pick(k, ns, rnd.Weight)
		again, _ := l.Owner(k)
		good, serial[k] = good && got == want && again == got, got
	}
	l2 := api.New()
	add(l2.AddNode, "n5", "n3", "n1", "n4", "n2")
	for k, want := range serial {
		if got, _ := l2.Owner(k); got != want {
			good = false
		}
	}
	ok("Owner 与朴素扫描一致; 重复调用/不同加入顺序结果一致", good)
	l3 := api.New() // 4. Three distinct sentinels; rejected ops leave no trace.
	l3.AddNode("x")
	keep, _ := l3.Owner("keep")
	errOK := errors.Is(l3.AddNode(""), api.ErrEmptyNodeID) &&
		errors.Is(l3.AddNode("x"), api.ErrDuplicate) &&
		errors.Is(l3.RemoveNode("ghost"), api.ErrNodeMissing)
	keep2, _ := l3.Owner("keep")
	ok("三类哨兵错误可判定且互不相同; 被拒后状态不变仍可用",
		errOK && keep == keep2 && l3.AddNode("y") == nil)
	big := true // 5. Large m: only A's 3 keys relocate; independent of m.
	for _, m := range []int{100, 1000, 10000} {
		sp := map[string]bool{"k00000": true, "k00001": true, "k00002": true}
		bc := cluster.New(skew(sp))
		add(bc.AddNode, "A", "B", "C")
		bk := make([]string, m)
		for i := range bk {
			bk[i] = fmt.Sprintf("k%05d", i)
		}
		b0 := om(bc.Owner, bk)
		bc.RemoveNode("A")
		big = big && moved(b0, om(bc.Owner, bk)) == 3
	}
	ok("大m(100/1000/10000)删A仅3键迁移, 检查数不随m线性增长", big)
	ck := make([]string, 100) // 6. Concurrent Owners equal the serial result.
	for i := range ck {
		ck[i] = fmt.Sprintf("ck-%03d", i)
	}
	want := om(l.Owner, ck)
	start, wg, concOK := make(chan struct{}), sync.WaitGroup{}, true
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for _, k := range ck {
				got, _ := l.Owner(k)
				if got != want[k] {
					concOK = false
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	ok("16 goroutine 并发 Owner 逐键与串行结果一致", concOK)
	ok("SelfCheck 四条不变量全过", api.New().SelfCheck() == nil) // 7.
	if fails > 0 {
		os.Exit(1)
	}
}
