package replay

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/code"
	"ontology/history"
)

// naiveSim 是按规则逐条写成的朴素模拟，与 replay.go 的实现相互独立，
// 用于随机对照。返回消费条数、续跑事件与错误。
func naiveSim(hist []history.Event, c code.Code) (int, []history.Event, error) {
	cursor := 0
	patched := map[string]bool{}
	var cont []history.Event
	var walk func(items []code.Item) error
	walk = func(items []code.Item) error {
		for _, it := range items {
			switch v := it.(type) {
			case code.Step:
				if cursor < len(hist) {
					ev := hist[cursor]
					if ev.Kind == history.Marker {
						return &UnexpectedMarkerError{Index: cursor, Pid: ev.Data}
					}
					if string(ev.Data) != string(v.Name) {
						return &MismatchError{Index: cursor, Have: ev.Data, Want: v.Name}
					}
					cursor++
				} else {
					cont = append(cont, history.S(v.Name))
				}
			case code.Branch:
				pid := string(v.Pid)
				switch {
				case patched[pid]:
					if err := walk(v.New); err != nil {
						return err
					}
				case cursor < len(hist) && hist[cursor].Kind == history.Marker:
					if string(hist[cursor].Data) != pid {
						return &UnexpectedMarkerError{Index: cursor, Pid: hist[cursor].Data}
					}
					cursor++
					patched[pid] = true
					if err := walk(v.New); err != nil {
						return err
					}
				case cursor < len(hist):
					if err := walk(v.Old); err != nil {
						return err
					}
				default:
					cont = append(cont, history.M(v.Pid))
					patched[pid] = true
					if err := walk(v.New); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	if err := walk([]code.Item(c)); err != nil {
		return 0, nil, err
	}
	if cursor < len(hist) {
		return 0, nil, &HistoryExtraError{Index: cursor}
	}
	return cursor, cont, nil
}

var (
	namePool = []string{"a", "b", "c"}
	pidPool  = []string{"p", "q"}
)

func randCode(r *rand.Rand) code.Code {
	n := 1 + r.Intn(7)
	c := make(code.Code, 0, n)
	for i := 0; i < n; i++ {
		if r.Intn(10) < 6 {
			c = append(c, st(namePool[r.Intn(len(namePool))]))
			continue
		}
		body := func() []code.Item {
			m := r.Intn(3)
			seq := make([]code.Item, m)
			for j := range seq {
				seq[j] = st(namePool[r.Intn(len(namePool))])
			}
			return seq
		}
		c = append(c, code.Branch{Pid: b(pidPool[r.Intn(len(pidPool))]),
			New: body(), Old: body()})
	}
	return c
}

// randHist 生成四类历史：空、本代码全史前缀、异代码全史前缀、随机事件流。
func randHist(r *rand.Rand, c code.Code) []history.Event {
	fullOf := func(cc code.Code) []history.Event {
		_, cont, err := naiveSim(nil, cc)
		if err != nil {
			panic(err)
		}
		return cont
	}
	switch r.Intn(4) {
	case 0:
		return nil
	case 1:
		full := fullOf(c)
		return full[:r.Intn(len(full)+1)]
	case 2:
		full := fullOf(randCode(r))
		return full[:r.Intn(len(full)+1)]
	default:
		m := r.Intn(7)
		hist := make([]history.Event, m)
		for i := range hist {
			if r.Intn(2) == 0 {
				hist[i] = history.S(b(namePool[r.Intn(len(namePool))]))
			} else {
				hist[i] = history.M(b(pidPool[r.Intn(len(pidPool))]))
			}
		}
		return hist
	}
}

func errIndex(err error) int {
	switch e := err.(type) {
	case *MismatchError:
		return e.Index
	case *UnexpectedMarkerError:
		return e.Index
	case *HistoryExtraError:
		return e.Index
	}
	return -1
}

func sameErrKind(got, want error) bool {
	return (errors.Is(got, ErrMismatch) && errors.Is(want, ErrMismatch)) ||
		(errors.Is(got, ErrUnexpectedMarker) && errors.Is(want, ErrUnexpectedMarker)) ||
		(errors.Is(got, ErrHistoryExtra) && errors.Is(want, ErrHistoryExtra))
}

// TestAgainstNaiveSim 随机生成代码与历史（含非法历史），
// 将 Run 的结果与朴素逐步模拟对照。
func TestAgainstNaiveSim(t *testing.T) {
	r := rand.New(rand.NewSource(20261003))
	for round := 0; round < 500; round++ {
		c := randCode(r)
		hist := randHist(r, c)
		wantConsumed, wantCont, wantErr := naiveSim(hist, c)

		store := history.NewStore()
		wf := b("wf")
		if len(hist) > 0 {
			if err := store.Append(wf, 0, hist); err != nil {
				t.Fatal(err)
			}
		}
		res, err := Run(store, wf, c)
		final := store.Snapshot(wf)
		if wantErr != nil {
			if err == nil || !sameErrKind(err, wantErr) || errIndex(err) != errIndex(wantErr) {
				t.Fatalf("round %d: 代码=%v 历史=%v\n got err=%v, want %v",
					round, c, hist, err, wantErr)
			}
			if !evsEqual(final, hist) {
				t.Fatalf("round %d: 出错的 Run 改了历史: %v", round, final)
			}
		} else {
			if err != nil {
				t.Fatalf("round %d: 代码=%v 历史=%v\n 意外错误 %v", round, c, hist, err)
			}
			if res.Consumed != wantConsumed || res.Continued != len(wantCont) {
				t.Fatalf("round %d: got (%d,%d), want (%d,%d)",
					round, res.Consumed, res.Continued, wantConsumed, len(wantCont))
			}
			if !evsEqual(final, append(hist, wantCont...)) {
				t.Fatalf("round %d: final=%v, want %v+%v", round, final, hist, wantCont)
			}
		}
		if round%100 == 0 {
			t.Logf("round %d: 代码=%v 历史=%v → res=%+v err=%v（依据：与朴素模拟一致）",
				round, c, hist, res, err)
		}
	}
}
