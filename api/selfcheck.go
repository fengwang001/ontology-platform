package api

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"

	"ontology/wal"
)

// SelfCheck 在全新的内部实例上跑内置操作序列，核验第二节四条不变量；
// 不读取也不改动 a 的状态，可被多个 goroutine 并发调用。
func (a *API) SelfCheck() error {
	x := New(1 << 20)
	r := rand.New(rand.NewSource(7))
	var chunks [][3]int64 // lo, hi, H（单线程，H=EndChunk 后此刻位置）
	nOut := 0
	shadow := map[int64]string{}
	apply := func(batch []Out) error { // 不变量 2：逐前缀重放 == 当时视图
		for _, o := range batch {
			if o.Op == Delete {
				if _, ok := shadow[o.Key]; !ok {
					return fmt.Errorf("删除不存在的键 %d", o.Key)
				}
				delete(shadow, o.Key)
			} else {
				shadow[o.Key] = o.Val
			}
		}
		if !maps.Equal(shadow, x.View()) {
			return errors.New("前缀重放与视图不符")
		}
		return nil
	}
	for i := 0; i < 5; i++ {
		lo := int64(i * 100)
		spam(x, r, lo-50, lo+150, 20)
		if err := x.BeginChunk(lo, lo+100); err != nil {
			return err
		}
		spam(x, r, lo-50, lo+150, 8)
		if err := x.ReadChunk(); err != nil {
			return err
		}
		spam(x, r, lo-50, lo+150, 8)
		o, err := x.EndChunk()
		if err != nil {
			return err
		}
		if err := apply(o); err != nil {
			return err
		}
		chunks = append(chunks, [3]int64{lo, lo + 100, x.log.Pos()})
		nOut += len(o)
		spam(x, r, lo-50, lo+150, 15)
		if o, err = x.Poll(); err != nil {
			return err
		}
		if err := apply(o); err != nil {
			return err
		}
		nOut += len(o)
	}
	if err := checkView(x, chunks); err != nil { // 不变量 1
		return err
	}
	if want := wantOuts(x.log, chunks); want != nOut { // 不变量 3：输出总数
		return fmt.Errorf("输出总数 %d != 应有 %d（重复或回退）", nOut, want)
	}
	return checkReject() // 不变量 4
}

func spam(x *API, r *rand.Rand, lo, hi int64, n int) {
	for i := 0; i < n; i++ {
		k := lo + r.Int63n(hi-lo)
		if r.Intn(4) == 0 {
			x.Append(Entry{Op: Delete, Key: k})
		} else {
			x.Append(Entry{Op: Upsert, Key: k, Val: "v"})
		}
	}
}

// checkView 不变量 1：视图 == 源表当前状态中键落在任一已完成 chunk 内的行。
func checkView(x *API, chunks [][3]int64) error {
	want := map[int64]string{}
	for k, v := range x.log.Table() {
		for _, c := range chunks {
			if k >= c[0] && k < c[1] {
				want[k] = v
			}
		}
	}
	if !maps.Equal(x.View(), want) {
		return errors.New("视图与源表∩已完成范围不符")
	}
	return nil
}

// wantOuts 不变量 3 的期望输出总数：每 chunk 内 H 时存在的键各一条，
// 加源日志中 LSN>H 的各次写入产生的输出（删除仅当键当时在视图中）。
func wantOuts(l *wal.Log, chunks [][3]int64) int {
	n := 0
	for _, c := range chunks {
		st, ex := map[int64]string{}, map[int64]bool{}
		for _, e := range l.Between(0, c[2]) {
			if e.Key >= c[0] && e.Key < c[1] {
				if e.Op == wal.Upsert {
					st[e.Key] = e.Val
				} else {
					delete(st, e.Key)
				}
			}
		}
		n += len(st)
		for k := range st {
			ex[k] = true
		}
		for _, e := range l.Between(c[2], l.Pos()) {
			if e.Key < c[0] || e.Key >= c[1] {
				continue
			}
			if e.Op == wal.Upsert {
				n++
				ex[e.Key] = true
			} else if ex[e.Key] {
				n++
				ex[e.Key] = false
			}
		}
	}
	return n
}
