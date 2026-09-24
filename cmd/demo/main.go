// Command demo 逐条打印去重引擎各判定的 OK/FAIL，输出不超过 10 行。
package main

import "errors"
import "fmt"
import "math/rand"
import "os"
import "reflect"
import "strconv"
import "sync"
import "time"
import "ontology/api"
import "ontology/rank"

var failed = false

func check(name string, ok bool) {
	s := "OK "
	if !ok {
		s, failed = "FAIL ", true
	}
	fmt.Println(s + name)
}
func ch(op byte, id, key string, tv int64) api.Change {
	return api.Change{Op: op, ID: id, Key: key, T: tv}
}
func ou(op byte, key, id string, tv int64) api.Out { return api.Out{Op: op, Key: key, ID: id, T: tv} }
func R(id string, tv int64) api.Row                { return api.Row{ID: id, T: tv} }
func main() {
	a := api.New(100) // 1) 九步序列：每步日志 + 第 4/5/6 步判定
	cs := []api.Change{
		ch('+', "a", "k", 5), ch('+', "b", "k", 8), ch('+', "p", "k", 3),
		ch('+', "m", "k", 3), ch('-', "m", "", 0), ch('+', "e", "k", 1),
		ch('-', "b", "", 0), ch('-', "e", "", 0), ch('-', "p", "", 0),
	}
	outs := [][]api.Out{
		{ou('+', "k", "a", 5)}, {},
		{ou('-', "k", "a", 5), ou('+', "k", "p", 3)}, {ou('-', "k", "p", 3), ou('+', "k", "m", 3)},
		{ou('-', "k", "m", 3), ou('+', "k", "p", 3)}, {ou('-', "k", "p", 3), ou('+', "k", "e", 1)},
		{}, {ou('-', "k", "e", 1), ou('+', "k", "p", 3)}, {ou('-', "k", "p", 3), ou('+', "k", "a", 5)},
	}
	leaders := []api.Row{R("a", 5), R("a", 5), R("p", 3), R("m", 3), R("p", 3), R("e", 1), R("e", 1), R("p", 3), R("a", 5)}
	ok := true
	for i, c := range cs {
		got, err := a.Apply([]api.Change{c})
		ok = ok && err == nil && reflect.DeepEqual(got, outs[i]) && a.View()["k"] == leaders[i]
	}
	check("nine-step log & step 4/5/6 judgments", ok)
	t1, t2 := api.New(10), api.New(10) // 2) T 相等按 ID 字节字典序
	t1.Apply([]api.Change{ch('+', "p", "k", 3), ch('+', "m", "k", 3)})
	t2.Apply([]api.Change{ch('+', "a9", "k", 1), ch('+', "a10", "k", 1)})
	check("tie T breaks by ID byte order", t1.View()["k"].ID == "m" && t2.View()["k"].ID == "a10")
	rng, r := rand.New(rand.NewSource(7)), api.New(100000) // 3)+4) 随机序列对拍
	alive, keyOf, down := map[string]api.Row{}, map[string]string{}, map[string]api.Row{}
	logOK, nid := true, 0
	for i := 0; i < 300; i++ {
		nid++
		c := ch('+', "id"+strconv.Itoa(nid), "k"+strconv.Itoa(rng.Intn(5)), rng.Int63n(2001)-1000)
		if len(alive) > 0 && rng.Intn(10) >= 7 {
			for id := range alive {
				c = ch('-', id, keyOf[id], 0)
				break
			}
		}
		got, err := r.Apply([]api.Change{c})
		if err != nil || len(got) > 2 || len(got) == 2 && got[0].ID == got[1].ID {
			logOK = false
		}
		if c.Op == '+' {
			alive[c.ID], keyOf[c.ID] = R(c.ID, c.T), c.Key
		} else {
			delete(alive, c.ID)
		}
		for _, e := range got {
			cur, has := down[e.Key]
			if e.Op == '-' && (!has || cur != R(e.ID, e.T)) || e.Op == '+' && has {
				logOK = false
			}
			if e.Op == '-' {
				delete(down, e.Key)
			} else {
				down[e.Key] = R(e.ID, e.T)
			}
		}
	}
	want := map[string]api.Row{}
	for id, x := range alive {
		if cur, has := want[keyOf[id]]; !has || x.T < cur.T || x.T == cur.T && id < cur.ID {
			want[keyOf[id]] = R(id, x.T)
		}
	}
	check("random view equals naive recompute", reflect.DeepEqual(r.View(), want))
	check("log prefixes self-consistent & minimal", logOK && reflect.DeepEqual(down, want))
	e := api.New(2) // 5)+6) 四类哨兵互不相同；被拒整批不留痕、可继续用
	e.Apply([]api.Change{ch('+', "a", "k", 1)})
	snap := e.View()
	got4 := []error{}
	for _, c := range [][]api.Change{{ch('+', "", "k", 1)}, {ch('+', "a", "k", 2)}, {ch('-', "z", "", 0)}, {ch('+', "b", "k", 2), ch('+', "c", "k", 3)}} {
		_, err := e.Apply(c)
		got4 = append(got4, err)
	}
	sents := []error{api.ErrInvalidChange, api.ErrIDExists, api.ErrIDMissing, api.ErrLimit}
	ok5 := true
	for i := range sents {
		ok5 = ok5 && errors.Is(got4[i], sents[i])
		for j := i + 1; j < 4; j++ {
			ok5 = ok5 && !errors.Is(sents[i], sents[j])
		}
	}
	check("four distinct decidable errors", ok5)
	ok6 := reflect.DeepEqual(e.View(), snap)
	_, useErr := e.Apply([]api.Change{ch('-', "a", "", 0)})
	check("rejected batch leaves state unchanged", ok6 && useErr == nil)
	bigOK, prev := true, time.Duration(0) // 7) 大 m 下比较次数不随 m 线性增长（计时比）
	for _, m := range []int{100, 10000} {
		s, rr := rank.New(), rand.New(rand.NewSource(int64(m)))
		for i := 0; i < m; i++ {
			s.Insert(rank.Row{ID: "id" + strconv.Itoa(i), T: rr.Int63n(int64(m) * 4)})
		}
		start := time.Now()
		for rep := 0; rep < 20000; rep++ {
			s.Insert(rank.Row{ID: "zz" + strconv.Itoa(rep), T: int64(m) * 4})
			head, _ := s.Min()
			s.Delete(head)
		}
		dur := time.Since(start)
		if prev > 0 && dur > prev*30 { // 线性扫描应慢约 100 倍，对数仅约 2-3 倍
			bigOK = false
		}
		prev = dur
	}
	check("big-m comparisons grow sub-linearly", bigOK)
	var wg sync.WaitGroup // 8) 并发只读一致
	views := make([]map[string]api.Row, 16)
	wg.Add(16)
	for i := range views {
		go func(i int) { defer wg.Done(); views[i] = r.View() }(i)
	}
	wg.Wait()
	ok8 := true
	for i := 1; i < 16; i++ {
		ok8 = ok8 && reflect.DeepEqual(views[i], want)
	}
	check("concurrent readers agree", ok8)
	check("SelfCheck", api.New(8).SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
