package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"ontology/internal/agg"
	"ontology/internal/parse"
	"ontology/internal/source"
	"ontology/internal/stage"
)

type check struct {
	ok  bool
	msg string
}

var results []check

func judge(ok bool, msg string) { results = append(results, check{ok, msg}) }

func stageCheck() {
	st := stage.New[int, int]("demo", 1, func(m stage.Msg[int], emit func(stage.Msg[int])) error {
		emit(m)
		return nil
	})
	go st.Run()
	got := make(chan int, 100)
	go func() {
		for range st.Out() {
			got <- 1
		}
		close(got)
	}()
	for i := 1; i <= 100; i++ {
		st.In() <- stage.Msg[int]{Seq: int64(i), V: i}
	}
	close(st.In())
	n := 0
	for range got {
		n++
	}
	<-st.Done()
	judge(n == 100 && st.MaxInFlight() <= 1, fmt.Sprintf("stage cap=1: drained=%d maxInFlight=%d <=1", n, st.MaxInFlight()))
}

func sourceCheck() {
	st := stage.New[[]byte, []byte]("src-demo", 1, func(m stage.Msg[[]byte], emit func(stage.Msg[[]byte])) error {
		emit(m)
		return nil
	})
	src := source.New(source.Config{Limit: 50, FailAfter: 20}, st.In())
	srcErr := make(chan error, 1)
	stop := make(chan struct{})
	go func() { srcErr <- src.Feed(stop) }()
	// 下游先不消费，确认背压把 source 阻塞住。
	deadline := time.Now().Add(time.Second)
	for src.Blocked() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	blocked := src.Blocked()
	go st.Run()
	go func() {
		for range st.Out() {
		}
	}()
	err := <-srcErr
	close(st.In())
	<-st.Done()
	judge(blocked > 0 && err == source.ErrMidStream && src.Seq() == 20,
		fmt.Sprintf("source: blocked=%d>0 midErr=%v produced=%d", blocked, err == source.ErrMidStream, src.Seq()))
}

func parseCheck() {
	ps := parse.New()
	st := stage.New[[]byte, parse.Rec]("parse-demo", 4, ps.Work)
	go st.Run()
	raw := []string{"=5", "a=1", "garbage", "b=x", "c=3"}
	go func() {
		for i, r := range raw {
			st.In() <- stage.Msg[[]byte]{Seq: int64(i + 1), V: []byte(r)}
		}
		st.In() <- stage.Msg[[]byte]{Seq: int64(len(raw)), Barrier: true}
		close(st.In())
	}()
	good, barriers := 0, 0
	var emptyKey bool
	for m := range st.Out() {
		if m.Barrier {
			barriers++
		} else {
			good++
			emptyKey = emptyKey || m.V.Key == ""
		}
	}
	<-st.Done()
	judge(good == 3 && ps.Bad() == 2 && barriers == 1 && emptyKey,
		fmt.Sprintf("parse: good=%d bad=%d barriers=%d emptyKey=%v", good, ps.Bad(), barriers, emptyKey))
}

func aggCheck() {
	ag := agg.New(2, nil)
	st := stage.New[parse.Rec, []agg.Group]("agg-demo", 4, ag.Work)
	go st.Run()
	go func() {
		for i, r := range []parse.Rec{{Key: "a", Val: 1}, {Key: "a", Val: 2}, {Key: "b", Val: 3}, {Key: "c", Val: 1}} {
			st.In() <- stage.Msg[parse.Rec]{Seq: int64(i + 1), V: r}
		}
		st.In() <- stage.Msg[parse.Rec]{Seq: 4, Barrier: true}
		close(st.In())
	}()
	var quiet []agg.Group
	for m := range st.Out() {
		if m.Barrier {
			quiet = m.V
		}
	}
	<-st.Done()
	errSeen := st.Err()
	overLimit := errors.Is(errSeen, agg.ErrTooManyGroups)
	judge(overLimit && len(quiet) == 0,
		fmt.Sprintf("agg: ErrTooManyGroups=%v (snapshot before stop=%d)", overLimit, len(quiet)))
}

func aggOKCheck() {
	// 正常路径：同组合并，静止点快照键序确定。
	ag := agg.New(0, nil)
	st := stage.New[parse.Rec, []agg.Group]("agg-ok", 4, ag.Work)
	go st.Run()
	go func() {
		for i, r := range []parse.Rec{{Key: "", Val: 2}, {Key: "b", Val: 3}, {Key: "", Val: 5}} {
			st.In() <- stage.Msg[parse.Rec]{Seq: int64(i + 1), V: r}
		}
		st.In() <- stage.Msg[parse.Rec]{Seq: 3, Barrier: true}
		close(st.In())
	}()
	var snap []agg.Group
	for {
		m, ok := <-st.Out()
		if !ok {
			break
		}
		if m.Barrier {
			snap = m.V
		}
	}
	<-st.Done()
	ok := len(snap) == 2 && snap[0].Key == "" && snap[0].Sum == 7 && snap[0].Count == 2
	judge(ok, fmt.Sprintf("agg: groups=%d emptyKeySum=%d", len(snap), snap[0].Sum))
}

func main() {
	stageCheck()
	sourceCheck()
	parseCheck()
	aggCheck()
	aggOKCheck()
	fail := 0
	for _, r := range results {
		prefix := "OK  "
		if !r.ok {
			prefix, fail = "FAIL", fail+1
		}
		fmt.Println(prefix, r.msg)
	}
	fmt.Printf("TOTAL %d/%d passed\n", len(results)-fail, len(results))
	if fail > 0 {
		os.Exit(1)
	}
}
