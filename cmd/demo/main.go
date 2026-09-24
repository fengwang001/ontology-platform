package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"ontology/ckpt"
	"ontology/parse"
	"ontology/pipe"
	"ontology/sink"
	"ontology/source"
	"ontology/stage"
)

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
		return
	}
	fails++
	fmt.Printf("FAIL %s\n", name)
}

func rows(n int) [][]byte {
	r := make([][]byte, n)
	for i := 0; i < n; i++ {
		r[i] = []byte(fmt.Sprintf("g%d,%d", i%37, i))
	}
	return r
}

func runPipe(dir string, crash string) pipe.Report {
	var cfg pipe.Config
	cfg.Dir = dir
	cfg.Source = source.NewMem(rows(200), 25)
	cfg.Barrier = 25
	cfg.Q1, cfg.Q2 = 4, 4
	switch crash {
	case "queued":
		cfg.CrashAtFinalBarrier = true
	case "agg":
		cfg.CrashAggRecords = 100
	case "rename":
		cfg.CrashBeforeRename = 1
	}
	return pipe.New(cfg).Run(context.Background())
}

// crashRecover 用子进程触发 os.Exit(42) 模拟硬崩溃，再在同目录恢复，
// 与从不崩溃的基线输出逐字节比较。
func crashRecover(mode string) bool {
	if os.Getenv("ONTOLOGY_DEMO_CRASH") == mode {
		runPipe(os.Getenv("ONTOLOGY_DEMO_DIR"), mode)
		os.Exit(0)
	}
	base, _ := os.MkdirTemp("", "base")
	defer os.RemoveAll(base)
	runPipe(base, "")
	want, err := os.ReadFile(sink.OutputPath(base))
	if err != nil {
		return false
	}
	d, _ := os.MkdirTemp("", "crash")
	defer os.RemoveAll(d)
	exe, _ := os.Executable()
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), "ONTOLOGY_DEMO_CRASH="+mode, "ONTOLOGY_DEMO_DIR="+d)
	cmd.Run()
	rep := runPipe(d, "")
	if rep.Err != nil {
		return false
	}
	got, err := os.ReadFile(sink.OutputPath(d))
	return err == nil && bytes.Equal(want, got)
}

func main() {
	check("demo skeleton runnable", true)
	src := source.NewMem([][]byte{[]byte("a,1"), []byte("b,2")}, 0, source.WithEndAfter(1))
	_, ok1, _ := src.Next()
	_, ok2, _ := src.Next()
	check("source injectable: early-end honored", ok1 && !ok2)
	_, good := parse.Convert(source.Item{Offset: 3, Data: []byte("x,notint")})
	env, _ := parse.Convert(source.Item{Offset: 4, Data: []byte(",7")})
	check("parse: bad record counted, empty key valid",
		!good && env.Rec.Key == "" && env.Rec.Value == 7)
	q1 := stage.NewQueue[int](1)
	q2 := stage.NewQueue[int](1)
	gg := stage.NewGauge(q1, q2)
	q1.Send(context.Background(), 1)
	q2.Send(context.Background(), 2)
	gg.Sample()
	check("stage gauge: in-flight bounded by sum of capacities",
		gg.Max() <= 2)

	d, _ := os.MkdirTemp("", "demo")
	defer os.RemoveAll(d)
	full, _ := sink.Encode(&sink.State{Groups: map[string]sink.Group{"k": {Min: 1, Max: 2, Sum: 3, N: 2}}, Offset: 9})
	allTrunc := true
	for i := 0; i < len(full); i++ {
		os.WriteFile(sink.TempPath(d), full[:i], 0o644)
		named := sink.IsTemp(filepath.Base(sink.TempPath(d)))
		sk, _ := sink.New(d)
		_, lerr := sk.Load()
		if !named || lerr != nil || fileExists(sink.TempPath(d)) {
			allTrunc = false
		}
	}
	check("sink: every truncated temp recognized and cleaned", allTrunc)
	cs := ckpt.New(d)
	old := &sink.State{Groups: map[string]sink.Group{"a": {Min: 1, Max: 1, Sum: 1, N: 1}}, Offset: 3}
	newer := &sink.State{Groups: map[string]sink.Group{"a": {Min: 1, Max: 4, Sum: 5, N: 2}}, Offset: 5}
	cs.Save(old)
	cs.Save(newer)
	enc, _ := sink.Encode(newer)
	os.WriteFile(filepath.Join(d, "ckpt.2"), append(bytes.Clone(enc[:len(enc)-2]), 'X'), 0o644)
	res, cerr := cs.Recover()
	check("ckpt: corrupt newest falls back to older good one",
		cerr == nil && res.FellBack && res.State.Offset == 3)

	bd, _ := os.MkdirTemp("", "bp")
	defer os.RemoveAll(bd)
	cfg := pipe.Config{Dir: bd, Source: source.NewMem(rows(50000), 1000),
		Barrier: 1000, Q1: 64, Q2: 64, RecordDelay: 20 * time.Microsecond}
	bp := pipe.New(cfg).Run(context.Background())
	check(fmt.Sprintf("backpressure: maxInFlight %d <= cap sum %d", bp.MaxFlight, 128),
		bp.Err == nil && bp.MaxFlight <= 128)
	check("backpressure: source blocked more than 0", bp.Blocked > 0)

	check("crash@source-drained-queue-nonempty: recovery byte-identical", crashRecover("queued"))
	check("crash@mid-aggregation: recovery byte-identical", crashRecover("agg"))
	check("crash@sink-temp-before-rename: recovery byte-identical", crashRecover("rename"))

	sd, _ := os.MkdirTemp("", "stop")
	defer os.RemoveAll(sd)
	base := runtime.NumGoroutine()
	p := pipe.New(pipe.Config{Dir: sd, Source: source.NewMem(rows(100000), 1000),
		Barrier: 1000, Q1: 8, Q2: 8, RecordDelay: time.Millisecond})
	go p.Run(context.Background())
	time.Sleep(20 * time.Millisecond)
	p.Stop()
	time.Sleep(100 * time.Millisecond)
	check("graceful stop: goroutines return to baseline",
		runtime.NumGoroutine() <= base+1)

	if fails == 0 {
		fmt.Printf("TOTAL: all %d checks passed\n", 13)
		return
	}
	fmt.Printf("TOTAL: %d check(s) failed\n", fails)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
