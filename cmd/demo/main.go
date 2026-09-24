package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"ontology/sink"
	"ontology/source"
	"ontology/stage"

	"ontology/parse"

	"ontology/ckpt"
)

var fails int

func line(ok bool, msg string) {
	tag := "OK"
	if !ok {
		tag = "FAIL"
		fails++
	}
	fmt.Printf("%s %s\n", tag, msg)
}

func drain(s source.Source) (int, error) {
	n := 0
	for {
		_, err := s.Next(context.Background())
		if errors.Is(err, io.EOF) {
			return n, nil
		}
		if err != nil {
			return n, err
		}
		n++
	}
}

func main() {
	items := make([][]byte, 10)
	for i := range items {
		items[i] = []byte("x")
	}

	n, err := drain(source.NewMem(items, source.Config{Limit: 4}))
	line(err == nil && n == 4, "source 可提前结束(4/10)")

	_, err = drain(source.NewMem(items, source.Config{ErrAt: 3}))
	line(errors.Is(err, source.ErrInjected), "source 中途报错如实上报")

	rows := [][]byte{[]byte(":1"), []byte("a:2"), []byte("noval"), []byte("a:xx"), []byte("b:3")}
	got, bad := 0, parse.CountBad(rows)
	for _, r := range rows {
		if _, perr := parse.Parse(r); perr == nil {
			got++
		}
	}
	line(got == 3 && bad == 2, "坏记录计数=2 且不中断, 空串键合法")

	base := runtime.NumGoroutine()
	q1, q2 := stage.NewQueue(8), stage.NewQueue(8)
	sinkQ := stage.NewSnapCh(8)
	agg := stage.NewAggregator(q2, sinkQ, stage.AggConfig{})
	running := true
	proc := stage.New(q1, func(it stage.Item) error {
		time.Sleep(time.Millisecond)
		if !q2.Send(stage.Item{Key: it.Key, Val: it.Val}, func() bool { return running }) {
			return io.EOF
		}
		return nil
	})
	go func() {
		for range sinkQ.C() {
			sinkQ.DoneOut()
		}
	}()
	for i := 0; i < 1500; i++ {
		if !q1.Send(stage.Item{Key: "k", Val: 1}, proc.Alive) {
			break
		}
	}
	q1.Close()
	proc.Wait()
	running = false
	q2.Close()
	agg.Wait()
	sinkQ.Close()
	time.Sleep(20 * time.Millisecond)
	capSum := q1.MaxInflight() + q2.MaxInflight() + sinkQ.MaxInflight()
	line(capSum <= 24, fmt.Sprintf("历史最大在途 %d <= 队列容量之和 24", capSum))
	line(q1.Blocks() > 0, fmt.Sprintf("source 被阻塞 %d 次(>0)", q1.Blocks()))
	now := runtime.NumGoroutine()
	line(now <= base+1, fmt.Sprintf("停止后 goroutine 回到基线 %d->%d", base, now))

	tdir, _ := os.MkdirTemp("", "sink")
	defer os.RemoveAll(tdir)
	sn, _ := sink.New(sink.Config{Dir: tdir, Name: "out"})
	payload, _ := sn.Write(stage.Snapshot{Bar: 1, Groups: []stage.Group{{Key: "a", Sum: 2}}})
	allRejected := true
	for cut := 0; cut < len(payload); cut++ {
		d, _ := os.MkdirTemp("", "cut")
		p := filepath.Join(d, "out.tmp")
		_ = os.WriteFile(p, payload[:cut], 0o644)
		if !sink.IsTemp(p) {
			allRejected = false
		}
		if sink.ValidFinal(payload[:cut]) {
			allRejected = false
		}
		if e := sink.CleanTemp(d); e != nil {
			allRejected = false
		}
		if _, e := os.Stat(p); !os.IsNotExist(e) {
			allRejected = false
		}
		_ = os.RemoveAll(d)
	}
	line(allRejected, fmt.Sprintf("临时文件逐字节截断(%d点)均判未完成并清理", len(payload)))

	cdir, _ := os.MkdirTemp("", "ckpt")
	defer os.RemoveAll(cdir)
	st0 := ckpt.New(cdir, "ckpt")
	_ = st0.Save(ckpt.State{Bar: 10, Groups: []stage.Group{{Key: "a", Sum: 1}}})
	_ = st0.Save(ckpt.State{Bar: 20, Groups: []stage.Group{{Key: "a", Sum: 2}}})
	_ = st0.Corrupt()
	got2, fellBack, cerr := ckpt.New(cdir, "ckpt").Load()
	line(cerr == nil && fellBack && got2.Bar == 10 && got2.Groups[0].Sum == 1,
		"检查点损坏后回退到上一个完好版本(bar 20->10)")

	fmt.Printf("总计: %d 项失败\n", fails)
	if fails > 0 {
		panic("demo failed")
	}
}
