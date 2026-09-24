package api

import (
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"testing"

	"ontology/jstate"
)

var errDiffer = errors.New("view differs between goroutines")

func refView(lm, rm map[string]string) []Out {
	byK := map[string][]string{}
	for id, k := range rm {
		byK[k] = append(byK[k], id)
	}
	ls := make([]string, 0, len(lm))
	for id := range lm {
		ls = append(ls, id)
	}
	sort.Strings(ls)
	out := []Out{}
	for _, l := range ls {
		rs := byK[lm[l]]
		if len(rs) == 0 {
			rs = []string{""}
		}
		sort.Strings(rs)
		for _, r := range rs {
			out = append(out, Out{LID: l, RID: r})
		}
	}
	return out
}
func walk(t *testing.T, seed, rounds int64) (*Joiner, map[string]string, map[string]string, []Out) {
	rng, j := rand.New(rand.NewSource(seed)), New(0)
	lm, rm := map[string]string{}, map[string]string{}
	ms := map[jstate.Side]map[string]string{jstate.SideL: lm, jstate.SideR: rm}
	sides, full := []jstate.Side{jstate.SideL, jstate.SideR}, []Out{}
	for r := int64(0); r < rounds; r++ {
		b := []Change{}
		for q := int64(0); q < 1+rng.Int63n(3); q++ {
			s := sides[rng.Intn(2)]
			id := string(s) + strconv.FormatInt(r*10+q, 10)
			k := "k" + strconv.Itoa(rng.Intn(5))
			ms[s][id], b = k, append(b, Change{Side: s, Op: '+', ID: id, Key: k})
		}
		if s := sides[rng.Intn(2)]; rng.Intn(2) == 0 {
			for id := range ms[s] {
				b = append(b, Change{Side: s, Op: '-', ID: id})
				delete(ms[s], id)
				break
			}
		}
		o, err := j.Apply(b)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		full = append(full, o...)
	}
	return j, lm, rm, full
}
func eachPrefix(t *testing.T, log []Out, fn func(map[[2]string]int)) {
	ms := map[[2]string]int{}
	for _, o := range log {
		k := [2]string{o.LID, o.RID}
		if (o.Op == '+' && ms[k] != 0) || (o.Op == '-' && ms[k] != 1) {
			t.Fatalf("bad count %v op %c", k, o.Op)
		}
		if o.Op == '+' {
			ms[k] = 1
		} else {
			delete(ms, k)
		}
		fn(ms)
	}
}
func TestViewMatchesBatchRecompute(t *testing.T) {
	for s := int64(0); s < 20; s++ {
		if j, lm, rm, _ := walk(t, s, 100); !reflect.DeepEqual(j.View(), refView(lm, rm)) {
			t.Fatalf("seed %d", s)
		}
	}
}
func TestChangeLogPrefixes(t *testing.T) {
	for s := int64(0); s < 10; s++ {
		_, _, _, log := walk(t, s+100, 60)
		eachPrefix(t, log, func(map[[2]string]int) {})
	}
}
func TestNullMutex(t *testing.T) {
	for s := int64(0); s < 10; s++ {
		_, _, _, log := walk(t, s+200, 60)
		eachPrefix(t, log, func(ms map[[2]string]int) {
			for k := range ms {
				if k[1] != "" && ms[[2]string{k[0], ""}] == 1 {
					t.Fatalf("seed %d %s NULL+%s", s, k[0], k[1])
				}
			}
		})
	}
}
func TestRejectedBatchLeavesNoTrace(t *testing.T) {
	j := New(2)
	base, _ := j.Apply([]Change{{Side: jstate.SideL, Op: '+', ID: "a", Key: "x"}})
	before := j.View()
	bads := [][]Change{{{Side: jstate.SideL, Op: '+', ID: "", Key: "x"}}, {{Side: jstate.SideL, Op: '+', ID: "a", Key: "x"}}, {{Side: jstate.SideL, Op: '-', ID: "ghost"}}, {{Side: jstate.SideL, Op: '+', ID: "b", Key: "x"}, {Side: jstate.SideR, Op: '+', ID: "c", Key: "x"}}}
	wants := []error{ErrInvalidChange, ErrDuplicateID, ErrMissingID, ErrTooManyRows}
	for i, b := range bads {
		_, err := j.Apply(b)
		if err != wants[i] || !reflect.DeepEqual(j.View(), before) || len(j.log) != len(base) {
			t.Fatalf("case %d", i)
		}
	}
	if _, err := j.Apply([]Change{{Side: jstate.SideR, Op: '+', ID: "r", Key: "z"}}); err != nil {
		t.Fatal(err)
	}
}
func TestConcurrentView(t *testing.T) {
	j, _, _, _ := walk(t, 999, 40)
	want := j.View()
	const n = 16
	var wg sync.WaitGroup
	start, errs := make(chan struct{}), make(chan error, n)
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := j.SelfCheck()
			if err == nil && !reflect.DeepEqual(j.View(), want) {
				err = errDiffer
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
