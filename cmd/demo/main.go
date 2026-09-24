package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"ontology/parse"
	"ontology/sink"
	"ontology/source"
	"ontology/stage"
)

type check struct {
	name   string
	ok     bool
	detail string
}

func main() {
	var results []check

	// stage：容量1的队列 + 慢消费者，背压必须传导到 Submit。
	capSum := 1
	st := stage.New[int, int](capSum, func(_ stage.Env, v int) ([]int, bool, error) {
		time.Sleep(time.Millisecond)
		return []int{v}, true, nil
	})
	go func() {
		for range st.Out() {
		}
	}()
	for i := 0; i < 200; i++ {
		st.Submit(i)
	}
	st.Close()
	st.Wait()
	maxQ, blocks := st.Stats()
	results = append(results, check{
		"stage backpressure: maxInFlight<=capSum and blocks>0",
		maxQ <= capSum && blocks > 0,
		fmt.Sprintf("max=%d cap=%d blocks=%d", maxQ, capSum, blocks),
	})

	// sink：提交后读回一致；逐字节截断每个点都必须判未完成。
	dir, _ := os.MkdirTemp("", "sinkdemo")
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "out")
	groups := map[string]sink.Agg{"a": {Sum: 10, Count: 2}, "b": {Sum: 3, Count: 1}}
	commitErr := sink.Commit(out, 3, 1, groups)
	off, bad, loaded, loadErr := sink.Load(out)
	roundtrip := commitErr == nil && loadErr == nil && off == 3 && bad == 1 &&
		loaded["a"] == groups["a"] && loaded["b"] == groups["b"]
	valid, _ := os.ReadFile(out)
	badTrunc := 0
	for n := 0; n < len(valid); n++ {
		p := filepath.Join(dir, fmt.Sprintf("t%d", n))
		if err := os.WriteFile(p, valid[:n], 0o644); err == nil {
			if _, _, _, e := sink.Load(p); e != nil {
				badTrunc++
			}
			os.Remove(p)
		}
	}
	os.WriteFile(out+".tmp", []byte("partial"), 0o644)
	swept, _ := sink.Sweep(dir)
	results = append(results, check{
		"sink: roundtrip, every truncation invalid, tmp swept",
		roundtrip && badTrunc == len(valid) && swept == 1,
		fmt.Sprintf("trunc=%d/%d swept=%d", badTrunc, len(valid), swept),
	})

	// parse：空串键合法；缺段、坏数值计坏记录且不产出。
	cases := [][]byte{
		[]byte("1||5"), []byte("2|k|7"), []byte("3|k"),
		[]byte("x|k|1"), []byte("4|k|z"),
	}
	good, bad, emptyKey := 0, 0, 0
	for _, c := range cases {
		r, ok := parse.Parse(c)
		if !ok {
			bad++
			continue
		}
		good++
		if r.Key == "" {
			emptyKey++
		}
	}
	results = append(results, check{
		"parse: bad counted and skipped, empty key legal",
		good == 2 && bad == 3 && emptyKey == 1,
		fmt.Sprintf("good=%d bad=%d emptyKey=%d", good, bad, emptyKey),
	})

	// source：FailAt=50 在第 50 条前报错；EndAt 提前结束优先被错误截断。
	src := source.New(source.Script{Total: 100, FailAt: 50}, 0)
	got := 0
	var srcErr error
	for {
		_, _, ok, err := src.Emit()
		if err != nil {
			srcErr = err
			break
		}
		if !ok {
			break
		}
		got++
	}
	end := source.New(source.Script{Total: 100, EndAt: 7}, 0)
	endN := 0
	for {
		if _, _, ok, _ := end.Emit(); !ok {
			break
		}
		endN++
	}
	results = append(results, check{
		"source: mid-run error surfaced and early-end count exact",
		got == 50 && errors.Is(srcErr, source.ErrFail) && endN == 7,
		fmt.Sprintf("emitted=%d err=%v endAt=%d", got, srcErr, endN),
	})

	pass := 0
	for _, r := range results {
		tag := "FAIL"
		if r.ok {
			tag, pass = "OK", pass+1
		}
		fmt.Printf("%s %s (%s)\n", tag, r.name, r.detail)
	}
	fmt.Printf("TOTAL %d/%d OK\n", pass, len(results))
	if pass != len(results) {
		panic("demo checks failed")
	}
}
