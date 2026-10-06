package collab

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"
)

func osGetenvInt(key string) int {
	v := os.Getenv(key)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}

type diffLog struct{ lines []string }

func (l *diffLog) printf(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (w *diffWorld) dump(t *testing.T) { w.log.dump(t) }

func (l *diffLog) dump(t *testing.T) {
	t.Helper()
	if os.Getenv("VERBOSE_DIFF") != "" {
		if err := os.WriteFile("/tmp/collab_diff.log", []byte(strings.Join(l.lines, "\n")), 0o644); err == nil {
			t.Logf("full diff log -> /tmp/collab_diff.log (%d lines)", len(l.lines))
		}
	}
	for _, ln := range l.lines {
		t.Log(ln)
	}
}

type diffWorld struct {
	real   *Server
	naive  *naiveModel
	rng    *rand.Rand
	log    *diffLog
	fields []string
	kinds  map[string]Kind
	pend   map[string]int64
	hist   map[string]map[int64]Op // verbatim accepted ops per client
	now    int64
}

func newDiffWorld(seed int64) *diffWorld {
	rng := rand.New(rand.NewSource(seed))
	nf := 2 + rng.Intn(4)
	kinds := map[string]Kind{}
	fields := make([]string, 0, nf)
	for i := 0; i < nf; i++ {
		name := "f" + strconv.Itoa(i)
		if rng.Intn(2) == 0 {
			kinds[name] = KindSet
		} else {
			kinds[name] = KindAdd
		}
		fields = append(fields, name)
	}
	srv := NewServer()
	if err := srv.Create("d", kinds); err != nil {
		panic(err)
	}
	w := &diffWorld{
		real: srv, naive: newNaive(kinds), rng: rng,
		log: &diffLog{}, fields: fields, kinds: kinds,
		pend: map[string]int64{},
		hist: map[string]map[int64]Op{},
	}
	w.log.printf("seed=%d schema=%v", seed, kinds)
	return w
}

func rejectClass(err error) string {
	switch {
	case err == nil:
		return ""
	case isReject(err, ErrInvalidParam):
		return "invalid"
	case isReject(err, ErrClockRollback):
		return "clock"
	case isReject(err, ErrReplayExpired):
		return "expired"
	case isReject(err, ErrSeqGap):
		return "gap"
	case isReject(err, ErrUnknownDoc):
		return "unknown"
	default:
		return "other"
	}
}

func isReject(err error, target error) bool {
	var re *RejectError
	if !asReject(err, &re) {
		return false
	}
	return re.Reason == target.Error()
}

func asReject(err error, re **RejectError) bool {
	for err != nil {
		if r, ok := err.(*RejectError); ok {
			*re = r
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func (w *diffWorld) check(t *testing.T) {
	t.Helper()
	snap, err := w.real.Get("d")
	if err != nil {
		t.Fatal(err)
	}
	nd := w.naive.docs["d"]
	if snap.Revision != nd.revision {
		w.log.printf("MISMATCH revision real=%d naive=%d", snap.Revision, nd.revision)
		w.dump(t)
		t.FailNow()
	}
	for name, nf := range nd.fields {
		rf := snap.Fields[name]
		if rf.Present != nf.present || (rf.Present && rf.Value != nf.value) || rf.Version != nf.version {
			w.log.printf("MISMATCH field %s real={p:%t v:%d ver:%d} naive={p:%t v:%d ver:%d}",
				name, rf.Present, rf.Value, rf.Version, nf.present, nf.value, nf.version)
			w.dump(t)
			t.FailNow()
		}
	}
	for c, seq := range w.pend {
		if got := w.real.Pending(c, "d"); got != seq {
			w.log.printf("MISMATCH pending %s real=%d want=%d", c, got, seq)
			w.dump(t)
			t.FailNow()
		}
	}
}

func formatKinds(rs []OpResult) string {
	var b strings.Builder
	for i, r := range rs {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%d:%s(v=%d,ver=%d,p=%t)", r.Seq, r.Kind, r.Value, r.Version, r.Present)
	}
	return b.String()
}

func (w *diffWorld) logBatch(client string, ops []Op, now int64) {
	var b strings.Builder
	fmt.Fprintf(&b, "client=%s now=%d ops=[", client, now)
	for i, op := range ops {
		if i > 0 {
			b.WriteByte(';')
		}
		dep := ""
		if op.Depends {
			dep = ",dep"
		}
		if op.Type == OpSet {
			fmt.Fprintf(&b, "{%d Set %s=%d base=%d g=%s%s}",
				op.Seq, op.Field, op.Value, op.BaseVer, op.Group, dep)
		} else {
			fmt.Fprintf(&b, "{%d Add %s%+d g=%s%s}",
				op.Seq, op.Field, op.Delta, op.Group, dep)
		}
	}
	b.WriteString("]")
	w.log.printf("%s", b.String())
}
