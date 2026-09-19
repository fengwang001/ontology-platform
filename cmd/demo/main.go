package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology"
)

type report struct {
	pass   bool
	name   string
	detail string
}

func (r report) line() string {
	verdict := "FAIL"
	if r.pass {
		verdict = "OK"
	}
	return fmt.Sprintf("%s %s: %s", verdict, r.name, r.detail)
}

func demoSuccess() report {
	store := ontology.NewStore()
	u := ontology.NewUnit(store)
	for i := 0; i < 3; i++ {
		u.Add(func() error { return nil }, func() error { return nil })
	}
	err := u.Run()
	pass := err == nil && len(u.Trace()) == 0
	return report{pass, "全部成功不补偿", fmt.Sprintf("runErr=%v 补偿轨迹=%v", err, u.Trace())}
}

func demoMidFailure() report {
	store := ontology.NewStore()
	_ = store.Put(ontology.Object{ID: "o", Count: 0})
	before, _ := store.Get("o")

	u := ontology.NewUnit(store)
	boom := errors.New("step3 failed")
	for i := 1; i <= 4; i++ {
		i := i
		u.Add(func() error {
			if i == 3 {
				return boom
			}
			_, _ = store.Add("o", 10)
			return nil
		}, func() error { _, _ = store.Add("o", -10); return nil })
	}
	_ = u.Run()
	after, _ := store.Get("o")
	trace := u.Trace()
	pass := len(trace) == 2 && trace[0] == 2 && trace[1] == 1 &&
		before.Count == 0 && after.Count == before.Count
	return report{pass, "中途失败逆序补偿",
		fmt.Sprintf("轨迹=%v 计数 %d->%d->%d", trace, before.Count, 20, after.Count)}
}

func demoCompFailure() report {
	store := ontology.NewStore()
	u := ontology.NewUnit(store)
	for i := 1; i <= 3; i++ {
		i := i
		u.Add(func() error { return nil }, func() error {
			if i == 2 {
				return errors.New("comp2 broken")
			}
			return nil
		})
	}
	err := u.Rollback()
	var agg *ontology.AggregateError
	aggOK := errors.As(err, &agg)
	step2, has2 := func() (error, bool) {
		if agg == nil {
			return nil, false
		}
		return agg.FailureFor(2)
	}()
	writeErr := store.Put(ontology.Object{ID: "x", Count: 1})
	var te *ontology.TaintedError
	taintOK := errors.As(writeErr, &te)
	trace := u.Trace()
	pass := aggOK && has2 && taintOK && te.FirstTaintedStep == 2 &&
		len(trace) == 3
	return report{pass, "补偿失败继续执行并污染",
		fmt.Sprintf("轨迹=%v 聚合步数=%v step2=%v 写入被拒(污染点=%d)",
			trace, func() int {
				if agg == nil {
					return 0
				}
				return len(agg.Failures())
			}(), step2 != nil, func() int {
				if te == nil {
					return -1
				}
				return te.FirstTaintedStep
			}())}
}

func demoPanic() report {
	store := ontology.NewStore()
	u := ontology.NewUnit(store)
	step1Ran := false
	for i := 1; i <= 3; i++ {
		i := i
		u.Add(func() error { return nil }, func() error {
			if i == 2 {
				panic("comp2 panic")
			}
			if i == 1 {
				step1Ran = true
			}
			return nil
		})
	}
	err := u.Rollback()
	var agg *ontology.AggregateError
	errors.As(err, &agg)
	var pe *ontology.PanicError
	if agg != nil {
		if perr, ok := agg.FailureFor(2); ok {
			errors.As(perr, &pe)
		}
	}
	trace := u.Trace()
	pass := pe != nil && pe.Value == "comp2 panic" && step1Ran && len(trace) == 3
	return report{pass, "补偿 panic 被捕获且不中断",
		fmt.Sprintf("轨迹=%v panic值=%v 后续补偿执行=%v", trace,
			func() any {
				if pe == nil {
					return nil
				}
				return pe.Value
			}(), step1Ran)}
}

func demoConcurrent() report {
	store := ontology.NewStore()
	u := ontology.NewUnit(store)
	for i := 1; i <= 10; i++ {
		u.Add(func() error { return nil }, func() error { return nil })
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = u.Rollback()
		}()
	}
	close(start)
	wg.Wait()

	trace := u.Trace()
	seen := map[int]bool{}
	duplicate := false
	for _, s := range trace {
		if seen[s] {
			duplicate = true
		}
		seen[s] = true
	}
	pass := len(trace) == 10 && !duplicate
	return report{pass, "并发回滚单次执行",
		fmt.Sprintf("轨迹=%v 无重复=%v", trace, !duplicate)}
}

func main() {
	reports := []report{
		demoSuccess(),
		demoMidFailure(),
		demoCompFailure(),
		demoPanic(),
		demoConcurrent(),
	}
	passed := 0
	for _, r := range reports {
		fmt.Println(r.line())
		if r.pass {
			passed++
		}
	}
	fmt.Printf("总计: %d/%d 场景通过\n", passed, len(reports))
}
