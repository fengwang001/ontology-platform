// Command demo 自检演示：退出码 0，不读参数、不联网；逐条打印 OK/FAIL（≤10 行）。
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"

	"ontology/api"
	"ontology/lock"
)

var fail bool

func ok(name string, pass bool) {
	if pass {
		fmt.Println("OK   " + name)
	} else {
		fail = true
		fmt.Println("FAIL " + name)
	}
}

// eightSteps 回放第三节八步，返回是否正确与逐步结果串。
func eightSteps() (bool, string) {
	l := lock.New()
	spec := []struct {
		wr, rel bool
		id      string
		g       bool
	}{
		{false, false, "R1", true}, {false, false, "R2", true},
		{true, false, "W1", false}, {false, false, "R3", false},
		{false, true, "R1", false}, {false, true, "R2", false},
		{false, false, "R4", false}, {true, true, "W1", false},
	}
	res := []string{"进入", "进入", "等待", "等待", "释放", "放行W1", "等待", "放行R3,R4"}
	var trace []string
	for i, s := range spec {
		var g bool
		var e error
		if s.rel {
			if s.wr {
				e = l.ReleaseWrite(s.id)
			} else {
				e = l.ReleaseRead(s.id)
			}
		} else {
			g, e = l.Try(s.id, s.wr)
		}
		if e != nil || (!s.rel && g != s.g) {
			return false, fmt.Sprintf("step%d出错", i+1)
		}
		trace = append(trace, fmt.Sprintf("%d%s", i+1, res[i]))
	}
	final := strings.Join(l.Readers(), ",")
	pass := final == "R3,R4" && l.Writer() == "" && len(l.Waiting()) == 0
	_ = l.ReleaseRead("R3")
	_ = l.ReleaseRead("R4")
	return pass, strings.Join(trace, " ") + " 终态读者=" + final
}

func writerPref() bool {
	l := lock.New()
	_, _ = l.Try("K", false)
	_, _ = l.Try("W1", true)
	g, _ := l.Try("R3", false) // 有写者等待：新读者必须排队
	return !g && strings.Join(l.Waiting(), ",") == "W1W,R3R"
}

func mutexHeld() bool {
	l := lock.New()
	_, _ = l.Try("W", true)
	g, _ := l.Try("R", false) // 写者持有时读者不得进入
	return !g
}

func sentinels() (distinct, notrace bool) {
	x := api.New()
	before := fmt.Sprint(x.Readers(), x.Writer())
	e1 := x.AcquireRead("")
	e2 := x.ReleaseRead("ghost")
	_ = x.AcquireRead("Z")
	_ = x.ReleaseRead("Z")
	e3 := x.ReleaseRead("Z")
	distinct = errors.Is(e1, api.ErrEmptyID) && errors.Is(e2, api.ErrNotHeld) &&
		errors.Is(e3, api.ErrDoubleRelease) && e1 != e2 && e2 != e3
	notrace = before == fmt.Sprint(x.Readers(), x.Writer())
	usable := x.AcquireRead("Q") == nil && x.ReleaseRead("Q") == nil
	return distinct && usable, notrace
}

// concurrent 用确定性并发场景核验互斥：主持有写锁时，读者/另一写者只能阻塞，
// 反复让出调度期间不变量恒成立；释放后等待者获得并释放，终态归空。
func concurrent() bool {
	x := api.New()
	if e := x.AcquireWrite("main"); e != nil {
		return false
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = x.AcquireRead("rc"); _ = x.ReleaseRead("rc") }()
	go func() { defer wg.Done(); _ = x.AcquireWrite("wc"); _ = x.ReleaseWrite("wc") }()
	for n := 0; n < 500; n++ { // 持写锁期间：无读者、写者唯一
		if x.Writer() != "main" || len(x.Readers()) != 0 {
			return false
		}
		runtime.Gosched()
	}
	if e := x.ReleaseWrite("main"); e != nil {
		return false
	}
	wg.Wait()
	return x.Writer() == "" && len(x.Readers()) == 0
}

func main() {
	pass, trace := eightSteps()
	ok("八步序列: "+trace, pass)
	ok("写者优先(有写者等待则新读者排队)", writerPref())
	ok("读写互斥(写者持有时拒读者)", mutexHeld())
	ok("与朴素参照一致(SelfCheck内置八步回放)", api.New().SelfCheck() == nil)
	distinct, notrace := sentinels()
	ok("三类可判定错误且互不相同/拒绝后仍可用", distinct)
	ok("被拒后状态不变", notrace)
	ok("大m下授予判定不随m增长(O(1)标志)", lock.FlagDecisionO1())
	ok("并发读写互斥/写者唯一", concurrent())
	if fail {
		os.Exit(1)
	}
}
