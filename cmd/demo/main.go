package main

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"os"
	"slices"
	"time"

	"ontology/api"
	"ontology/chunk"
	"ontology/wal"
)

var bad int

func rep(name string, ok bool) {
	if !ok {
		bad++
	}
	fmt.Println(map[bool]string{true: "OK   ", false: "FAIL "}[ok] + name)
}

func oup(k int64, v string) chunk.Out { return chunk.Out{Op: wal.Upsert, Key: k, Val: v} }

func app(l *wal.Log, es ...wal.Entry) {
	for _, e := range es {
		l.Append(e)
	}
}

func main() {
	s3, b20, b10, nd := scripted()
	rep("第三节 L=3 H=8 快照{15:c} EndChunk/Poll/视图", s3)
	rep("边界 键20在[10,20)外、键10在内", b20 && b10)
	rep("随机交错 视图==源表∩已完成范围", runChunks(false))
	rep("并发写入下结果正确", runChunks(true))
	rep("输出不重复不回退", nd)
	rep("四类错误可判定、被拒不留痕", rejectOK())
	rep("大 m 下修正检查数不随 m 增长", scaleOK())
	rep("SelfCheck", api.New(1).SelfCheck() == nil)
	os.Exit(min(bad, 1))
}

func scripted() (s3, b20, b10, nodup bool) {
	l := wal.New()
	m := chunk.New(l, 100)
	app(l, wal.Entry{Op: wal.Upsert, Key: 10, Val: "a"}, wal.Entry{Op: wal.Upsert, Key: 15, Val: "b"}, wal.Entry{Op: wal.Upsert, Key: 20, Val: "x"})
	m.BeginChunk(10, 20)
	app(l, wal.Entry{Op: wal.Upsert, Key: 15, Val: "c"}, wal.Entry{Op: wal.Delete, Key: 10})
	m.ReadChunk()
	app(l, wal.Entry{Op: wal.Upsert, Key: 12, Val: "d"}, wal.Entry{Op: wal.Upsert, Key: 15, Val: "e"}, wal.Entry{Op: wal.Upsert, Key: 20, Val: "y"})
	o1, _ := m.EndChunk()
	app(l, wal.Entry{Op: wal.Upsert, Key: 10, Val: "f"}, wal.Entry{Op: wal.Delete, Key: 12}, wal.Entry{Op: wal.Upsert, Key: 19, Val: "g"})
	o2, _ := m.Poll()
	outs := append(o1, o2...)
	s3 = l.Pos() == 11 && maps.Equal(m.View(), map[int64]string{10: "f", 15: "e", 19: "g"}) &&
		slices.Equal(outs, []chunk.Out{oup(12, "d"), oup(15, "e"), oup(10, "f"), chunk.Out{Op: wal.Delete, Key: 12}, oup(19, "g")})
	b20 = !slices.ContainsFunc(outs, func(o chunk.Out) bool { return o.Key == 20 })
	b10 = slices.ContainsFunc(outs, func(o chunk.Out) bool { return o.Key == 10 })
	app(l, wal.Entry{Op: wal.Upsert, Key: 1, Val: "a"}, wal.Entry{Op: wal.Upsert, Key: 2, Val: "b"})
	m.BeginChunk(0, 10)
	app(l, wal.Entry{Op: wal.Upsert, Key: 1, Val: "c"})
	m.ReadChunk()
	app(l, wal.Entry{Op: wal.Delete, Key: 2}, wal.Entry{Op: wal.Upsert, Key: 3, Val: "d"})
	o3, _ := m.EndChunk()
	app(l, wal.Entry{Op: wal.Upsert, Key: 1, Val: "e"}, wal.Entry{Op: wal.Delete, Key: 3}, wal.Entry{Op: wal.Delete, Key: 9}, wal.Entry{Op: wal.Upsert, Key: 4, Val: "f"})
	o4, _ := m.Poll()
	nodup = slices.Equal(append(o3, o4...), []chunk.Out{oup(1, "c"), oup(3, "d"), oup(1, "e"), chunk.Out{Op: wal.Delete, Key: 3}, oup(4, "f")}) &&
		maps.Equal(m.View(), map[int64]string{1: "e", 4: "f", 10: "f", 15: "e", 19: "g"})
	return
}

func runChunks(bg bool) bool {
	l := wal.New()
	m := chunk.New(l, 1<<20)
	r := rand.New(rand.NewSource(2))
	fin := make(chan struct{})
	if bg {
		go func() {
			defer close(fin)
			r2 := rand.New(rand.NewSource(5))
			for range 100000 {
				l.Append(wal.Entry{Op: wal.Upsert, Key: r2.Int63n(300), Val: "v"})
			}
		}()
	}
	var cs [][2]int64
	for i := 0; i < 4; i++ {
		lo := int64(i * 60)
		spam(l, r, lo-30, lo+90, 20)
		m.BeginChunk(lo, lo+60)
		spam(l, r, lo-30, lo+90, 8)
		m.ReadChunk()
		spam(l, r, lo-30, lo+90, 8)
		m.EndChunk()
		cs = append(cs, [2]int64{lo, lo + 60})
		m.Poll()
	}
	if bg {
		<-fin
	}
	m.Poll()
	want := map[int64]string{}
	for k, v := range l.Table() {
		for _, c := range cs {
			if k >= c[0] && k < c[1] {
				want[k] = v
			}
		}
	}
	return maps.Equal(m.View(), want)
}

func spam(l *wal.Log, r *rand.Rand, lo, hi int64, n int) {
	for i := 0; i < n; i++ {
		l.Append(wal.Entry{Op: wal.Op(r.Intn(2)), Key: lo + r.Int63n(hi-lo), Val: "v"})
	}
}

func rejectOK() bool {
	a := api.New(2)
	a.Append(wal.Entry{Op: wal.Upsert, Key: 1, Val: "a"}, wal.Entry{Op: wal.Upsert, Key: 2, Val: "b"}, wal.Entry{Op: wal.Upsert, Key: 3, Val: "c"})
	end := func() error { _, err := a.EndChunk(); return err }
	ok := errors.Is(a.BeginChunk(5, 5), api.ErrBadRange) &&
		errors.Is(a.ReadChunk(), api.ErrPhase) &&
		a.BeginChunk(0, 10) == nil && a.ReadChunk() == nil &&
		errors.Is(end(), api.ErrViewLimit)
	a.Append(wal.Entry{Op: wal.Delete, Key: 3}) // 删一行后重试 EndChunk 应成功，证明被拒不留痕
	_, err := a.EndChunk()
	return ok && err == nil && len(a.View()) == 2 && errors.Is(a.BeginChunk(5, 15), api.ErrOverlap)
}

func scaleOK() bool {
	t := func(m int) time.Duration {
		l := wal.New()
		for range m {
			l.Append(wal.Entry{Op: wal.Upsert, Key: 1, Val: "v"})
		}
		mg := chunk.New(l, 1<<20)
		mg.BeginChunk(-5, 5)
		mg.ReadChunk()
		t0 := time.Now()
		mg.EndChunk()
		return time.Since(t0)
	}
	return t(1000000) < t(2000)*20+3*time.Millisecond
}
