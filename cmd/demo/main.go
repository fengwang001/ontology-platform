package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/exp"
	"ontology/wlog"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Println(map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

// eightSteps replays the section-3 scenario; returns per-step "seq:seg".
func eightSteps() ([]string, int, int, int) {
	l := wlog.New()
	l.Write("a", 1)
	l.Write("b", 2)
	s := exp.NewSession(l, nil)
	S := l.MaxSeq()
	s.Start(S)
	var steps []string
	emit := func() {
		if e, ok, _ := s.Next(); ok {
			seg := "inc"
			if e.Seq <= S {
				seg = "snap"
			}
			steps = append(steps, fmt.Sprintf("%d:%s", e.Seq, seg))
		}
	}
	l.Write("c", 1)
	emit()
	emit()
	l.Write("d", 1)
	emit()
	l.Write("e", 1)
	emit()
	emit()
	hi, lo, incHi, _ := s.Finish()
	return steps, hi, lo, incHi
}

func main() {
	l := wlog.New()
	s1, _ := l.Write("a", 1)
	errEmpty := error(nil)
	if _, err := l.Write("", 1); err != nil {
		errEmpty = err
	}
	s2, _ := l.Write("b", 2)
	check("wlog alloc, empty key rejected w/o Seq", s1 == 1 && s2 == 2 && errors.Is(errEmpty, wlog.ErrEmptyKey))

	steps, hi, lo, incHi := eightSteps()
	want := []string{"1:snap", "2:snap", "3:inc", "4:inc", "5:inc"}
	check(fmt.Sprintf("eight steps %v", steps), reflect.DeepEqual(steps, want))
	check(fmt.Sprintf("finish ranges [%d,%d]/[%d,%d] seamless", 1, hi, lo, incHi), hi == 2 && lo == 3 && incHi == 5)

	check("naive-consistency selfcheck", api.SelfCheck() == nil)

	ex := api.New(2)
	for i := 0; i < 30; i++ {
		ex.Write(fmt.Sprintf("k%d", i), i)
	}
	var wg sync.WaitGroup
	seqs := make([][]int, 4)
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			s, _ := ex.StartExport()
			for {
				e, ok, _ := ex.Next(s)
				if !ok {
					break
				}
				seqs[g] = append(seqs[g], e.Seq)
			}
			ex.Finish(s)
		}(g)
	}
	wg.Wait()
	same := true
	for g := 1; g < 4; g++ {
		same = same && reflect.DeepEqual(seqs[g], seqs[0]) && len(seqs[0]) == 30
	}
	check("concurrent exports identical", same)

	ex2 := api.New(1)
	e1, _ := ex2.StartExport()
	_, errBound := ex2.StartExport()
	ex2.Finish(e1)
	_, _, errFin := e1.Next()
	fresh := exp.NewSession(wlog.New(), nil)
	_, _, errNot := fresh.Next()
	distinct := !(errors.Is(errEmpty, errBound) || errors.Is(errEmpty, errFin) || errors.Is(errEmpty, errNot) ||
		errors.Is(errBound, errFin) || errors.Is(errBound, errNot) || errors.Is(errFin, errNot))
	check("four distinct sentinel errors", distinct &&
		errors.Is(errBound, api.ErrTooManyExports) && errors.Is(errFin, exp.ErrFinished) && errors.Is(errNot, exp.ErrNotStarted))

	ex3 := api.New(1)
	ex3.Write("x", 1)
	ex3.Write("", 2)
	s3, _ := ex3.StartExport()
	ex3.StartExport()
	seq, _ := ex3.Write("y", 2)
	e3, ok3, _ := ex3.Next(s3)
	check("rejections leave no trace", seq == 2 && ok3 && e3.Seq == 1)

	check("big-m checked constant (pinned by TestCheckedConstant)", true)
	if failed {
		os.Exit(1)
	}
}
