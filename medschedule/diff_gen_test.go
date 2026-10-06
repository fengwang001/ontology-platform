package medschedule

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"testing"
)

type simState struct {
	drugs     []string
	patients  []string
	activeOrd []string
	allIDs    []string
	idCounter int
}

func genSpec(r *rand.Rand, st *simState, now, w int64, id string) Spec {
	drug := st.drugs[r.Intn(len(st.drugs))]
	patient := st.patients[r.Intn(len(st.patients))]
	sp := Spec{ID: id, Patient: patient, Drug: drug}
	switch r.Intn(3) {
	case 0:
		sp.Kind = "interval"
		sp.H = 2*w + 1 + r.Int63n(4*w+10)
		sp.FirstTime = now + r.Int63n(3)
	case 1:
		sp.Kind = "daily"
		n := 1 + r.Intn(3)
		step := dayLen / int64(n)
		off := r.Int63n(step - 2*w - 1)
		for i := 0; i < n; i++ {
			sp.TimesOfDay = append(sp.TimesOfDay, int64(i)*step+off)
		}
	default:
		sp.Kind = "prn"
		sp.PRNMinGap = 1 + r.Int63n(50)
		sp.PRNMax24 = 1 + r.Int63n(4)
	}
	return sp
}

func generateSequence(seed int64) ([]op, int64) {
	r := rand.New(rand.NewSource(seed))
	w := int64(2 + r.Intn(8))
	var ops []op
	st := &simState{patients: []string{"P1", "P2", "P3"}}
	now := int64(0)
	for i := 0; i < 4; i++ {
		name := fmt.Sprintf("D%d", i+1)
		cat := []string{"CAT1", "CAT2"}[i%2]
		minInt := 1 + r.Int63n(2*w)
		ops = append(ops, op{kind: opDrug, now: now, a: name, b: cat, n1: minInt})
		st.drugs = append(st.drugs, name)
	}
	nSteps := 30 + r.Intn(60)
	advance := func() int64 {
		switch r.Intn(10) {
		case 0:
		case 1:
			now += 1
		case 2:
			now += w
		default:
			now += 1 + r.Int63n(200)
		}
		return now
	}
	newID := func() string {
		st.idCounter++
		id := fmt.Sprintf("O%d", st.idCounter)
		st.allIDs = append(st.allIDs, id)
		return id
	}
	for i := 0; i < nSteps; i++ {
		now = advance()
		roll := r.Intn(100)
		switch {
		case roll < 6:
			who := st.patients[r.Intn(len(st.patients))]
			item := st.drugs[r.Intn(len(st.drugs))]
			if r.Intn(2) == 0 {
				item = []string{"CAT1", "CAT2"}[r.Intn(2)]
			}
			ops = append(ops, op{kind: opAllergy, now: now, who: who, a: item, flag: r.Intn(5) != 0})
		case roll < 24:
			id := newID()
			ops = append(ops, op{kind: opCreate, now: now, spec: genSpec(r, st, now, w, id)})
		case roll < 30 && len(st.activeOrd) > 0:
			id := st.activeOrd[r.Intn(len(st.activeOrd))]
			ops = append(ops, op{kind: opStop, now: now, a: id})
		case roll < 36 && len(st.activeOrd) > 0:
			idx := r.Intn(len(st.activeOrd))
			old := st.activeOrd[idx]
			id := newID()
			ops = append(ops, op{kind: opReplace, now: now, oldID: old, spec: genSpec(r, st, now, w, id)})
			st.activeOrd = append(st.activeOrd[:idx], st.activeOrd[idx+1:]...)
			st.activeOrd = append(st.activeOrd, id)
		case roll < 72 && len(st.allIDs) > 0:
			id := st.allIDs[r.Intn(len(st.allIDs))]
			k := []opKind{opAdmin, opRefuse, opMakeup, opPRN}[r.Intn(4)]
			ops = append(ops, op{kind: k, now: now, a: id})
		default:
			who := st.patients[r.Intn(len(st.patients))]
			lo := int64(0)
			if now > 50 && r.Intn(2) == 0 {
				lo = now - r.Int63n(50)
			}
			hi := now + r.Int63n(300)
			ops = append(ops, op{kind: opQuery, now: now, who: who, lo: lo, hi: hi})
		}
	}
	return ops, w
}

func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping differential in short mode")
	}
	logPath := os.Getenv("MEDSCHED_DIFF_LOG")
	if logPath == "" {
		logPath = "diff_trace.log"
	}
	lf, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	defer lf.Close()
	bw := bufio.NewWriter(lf)
	defer bw.Flush()

	const sequences = 1500
	for seq := 0; seq < sequences; seq++ {
		seed := int64(seq*7919 + 1)
		ops, w := generateSequence(seed)
		sys, _ := NewSystem(w)
		nv := NewNaiveModel(w)
		fmt.Fprintf(bw, "==== SEQUENCE %d seed=%d W=%d steps=%d ====\n", seq, seed, w, len(ops))
		for i, o := range ops {
			e1, pts1, prn1 := applyOne(sys, o)
			e2, pts2, prn2 := applyNaive(nv, o)
			fmt.Fprintf(bw, "[%03d] IN  %s\n", i, describeOp(o))
			fmt.Fprintf(bw, "      OUT efficient=%s\n", explainDecision(e1))
			fmt.Fprintf(bw, "      OUT naive    =%s\n", explainDecision(e2))
			if errCodeOf(e1) != errCodeOf(e2) {
				bw.Flush()
				t.Fatalf("seq=%d step=%d op=%s error mismatch: %v vs %v (log %s)",
					seq, i, describeOp(o), e1, e2, logPath)
			}
			if e1 == nil && o.kind == opQuery {
				if !reflect.DeepEqual(pts1, pts2) {
					bw.Flush()
					t.Fatalf("seq=%d step=%d points mismatch\n eff=%+v\n naive=%+v\n op=%s (log %s)",
						seq, i, pts1, pts2, describeOp(o), logPath)
				}
				if !reflect.DeepEqual(prn1, prn2) {
					bw.Flush()
					t.Fatalf("seq=%d step=%d prn mismatch\n eff=%+v\n naive=%+v (log %s)",
						seq, i, prn1, prn2, logPath)
				}
			}
		}
	}
	t.Logf("differential done: %d sequences, trace in %s", sequences, logPath)
}
