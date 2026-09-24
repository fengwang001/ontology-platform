package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/audit"
	"ontology/handle"
	"ontology/table"
)

var failed bool

func check(name string, ok bool) {
	mark := "OK  "
	if !ok {
		mark, failed = "FAIL", true
	}
	fmt.Println(mark, name)
}

func snapshot(t *table.Table) (audit.Stats, []int32) {
	_, free := t.Inspect()
	return audit.Collect(t), free
}

func sameSnap(t *table.Table, st audit.Stats, free []int32) bool {
	now, nowFree := snapshot(t)
	return now.Alive == st.Alive && now.Free == st.Free && now.Exhausted == st.Exhausted &&
		now.Cap == st.Cap && slices.Equal(now.Gens, st.Gens) && slices.Equal(nowFree, free)
}

func abaTest() bool {
	tab, _ := table.New(1)
	h, _ := tab.Insert("v0")
	_ = tab.Remove(h) // h 已失效，槽位 0 将被反复复用
	var bad atomic.Int32
	var done atomic.Bool
	var wg, readers sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !done.Load() {
			if nh, err := tab.Insert("x"); err == nil {
				_ = tab.Remove(nh)
			}
		}
	}()
	for i := 0; i < 8; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for j := 0; j < 2000; j++ {
				if _, err := tab.Get(h); err == nil {
					bad.Add(1)
				}
			}
		}()
	}
	readers.Wait()
	done.Store(true)
	wg.Wait()
	return bad.Load() == 0 && audit.Check(tab) == nil
}

func probe(capacity int) int {
	tab, _ := table.New(capacity)
	hs := make([]handle.Handle, 0, capacity)
	for i := 0; i < capacity; i++ {
		h, _ := tab.Insert(i)
		hs = append(hs, h)
	}
	for i := 0; i < capacity; i += 2 {
		_ = tab.Remove(hs[i])
	}
	_, _ = tab.Insert("probe")
	f := reflect.ValueOf(tab).Elem().FieldByName("free") // 仅演示读取非导出计数器
	return int(f.Elem().FieldByName("visited").Int())
}

func main() {
	t1, _ := table.New(4)
	t2, _ := table.New(4)
	h1, _ := t1.Insert("alpha")
	v, err := t1.Get(h1)
	check("插入后可取出", err == nil && v == "alpha" && t1.Len() == 1)
	_ = t1.Remove(h1)
	_, err = t1.Get(h1)
	check("Remove后老句柄失效", errors.Is(err, table.ErrStale))
	for i := 0; i < 50; i++ {
		nh, _ := t1.Insert(i)
		_ = t1.Remove(nh)
	}
	_, err = t1.Get(h1)
	check("复用50次后老句柄仍失效", errors.Is(err, table.ErrStale) && t1.Len() == 0)
	t3, _ := table.New(3)
	g0, _ := t3.Insert("x")
	g1, _ := t3.Insert("y")
	g2, _ := t3.Insert("z")
	_ = t3.Remove(g0)
	var last handle.Handle
	for {
		last, _ = t3.Insert("w")
		if last.Gen() == handle.MaxGen {
			break
		}
		_ = t3.Remove(last)
	}
	_ = t3.Remove(last) // 槽位 0 代号触顶退役
	_, errEx := t3.Get(last)
	_, errOld := t3.Get(g0)
	gy, errY := t3.Get(g1)
	_ = t3.Remove(g2)
	g3, errIns := t3.Insert("new")
	check("代号耗尽槽位退役且整表可用", errors.Is(errEx, table.ErrSlotExhausted) &&
		errors.Is(errOld, table.ErrStale) && errY == nil && gy == "y" && errIns == nil && t3.Len() == 2)
	st := audit.Collect(t3)
	check("不变量3等式与自检", st.Alive+st.Free+st.Exhausted == st.Cap && audit.Check(t3, g1, g3) == nil)
	t4, _ := table.New(2)
	a1, _ := t4.Insert(1)
	_, _ = t4.Insert(2)
	st4, free4 := snapshot(t4)
	_, _ = t4.Insert(3)             // 满：ErrTableFull
	_, _ = t4.Get(handle.Handle(0)) // 零值：ErrZeroHandle
	_, _ = t2.Get(a1)               // 跨表：ErrWrongTable
	_ = t4.Remove(h1)               // 他表句柄：ErrWrongTable
	check("被拒操作不留痕", sameSnap(t4, st4, free4))
	var zero handle.Handle
	_, err = t4.Get(zero)
	check("零值句柄无效", errors.Is(err, table.ErrZeroHandle))
	_, err = t2.Get(a1)
	check("跨表句柄无效", errors.Is(err, table.ErrWrongTable))
	_, e1 := t1.Get(h1)
	_, e2 := t1.Get(h1)
	check("失效句柄两次同错", errors.Is(e1, table.ErrStale) && errors.Is(e2, table.ErrStale))
	check("并发ABA老句柄零次成功", abaTest())
	v1k, v100k := probe(1000), probe(100000)
	check(fmt.Sprintf("访问记录数 1k:%d 100k:%d (上限4)", v1k, v100k),
		v1k >= 1 && v1k <= 4 && v1k == v100k)
	if failed {
		fmt.Println("RESULT FAIL")
		os.Exit(1)
	}
	fmt.Println("RESULT OK")
}
