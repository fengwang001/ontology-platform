package gate_test

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/compat"
	"ontology/gate"
	"ontology/schema"
)

var fieldNames = []string{"f0", "f1", "f2", "f3", "f4", "f5"}
var types = []schema.Type{schema.Int32, schema.Int64, schema.Float64, schema.String, schema.Bytes}
var subjects = []string{"s1", "s2"}
var consumers = []string{"c1", "c2", "c3", "c4"}

func genFields(rng *rand.Rand) []schema.Field {
	n := 1 + rng.Intn(6)
	perm := rng.Perm(6)
	out := make([]schema.Field, 0, n)
	for i := 0; i < n; i++ {
		name := fieldNames[perm[i]]
		out = append(out, schema.Field{
			Name:       name,
			Type:       types[rng.Intn(len(types))],
			Required:   rng.Intn(2) == 0,
			HasDefault: rng.Intn(2) == 0,
		})
	}
	switch rng.Intn(12) {
	case 0:
		return nil
	case 1:
		out = append(out, out[0])
	case 2:
		out[0].Type = schema.Type(99)
	case 3:
		out[0].Name = ""
	}
	return out
}

func genNames(rng *rand.Rand) []string {
	n := 1 + rng.Intn(4)
	perm := rng.Perm(len(fieldNames))
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, fieldNames[perm[i]])
	}
	if rng.Intn(10) == 0 {
		out = append(out, out[0])
	}
	return out
}

func classify(err error) (string, string) {
	switch {
	case err == nil:
		return "ok", ""
	case errors.Is(err, gate.ErrInvalidArgument):
		return "invalid", ""
	case errors.Is(err, gate.ErrClockSkew):
		return "skew", ""
	case errors.Is(err, gate.ErrExists):
		return "exists", ""
	case errors.Is(err, gate.ErrNoChange):
		return "nochange", ""
	case errors.Is(err, gate.ErrStillBroken):
		return "broken", ""
	}
	var be *gate.BlockedError
	if errors.As(err, &be) {
		parts := make([]string, len(be.Blockers))
		for i, b := range be.Blockers {
			parts[i] = fmt.Sprintf("%s:%s:%d", b.Consumer, b.Violation.Field, b.Violation.Reason)
		}
		return "blocked", strings.Join(parts, ",")
	}
	var ie *gate.IncompatibleError
	if errors.As(err, &ie) {
		dir := "FORWARD"
		if ie.Direction == compat.Backward {
			dir = "BACKWARD"
		}
		return "incompatible", fmt.Sprintf("%s:%s:%d", dir, ie.Violation.Field, ie.Violation.Reason)
	}
	if errors.Is(err, gate.ErrNotFound) {
		return "notfound", ""
	}
	return "OTHER", err.Error()
}

type seqState struct {
	latest    map[string]int
	knownCons map[string]map[string]bool
}

func TestRandomAgainstNaive(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	for seq := 0; seq < 1500; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 1))
		reg := gate.NewRegistry()
		m := newNaive()
		st := &seqState{
			latest:    map[string]int{},
			knownCons: map[string]map[string]bool{},
		}
		var log strings.Builder
		var clock int64
		steps := 24
		if seq%7 == 0 {
			steps = 40
		}
		failed := false
		for step := 0; step < steps; step++ {
			sn := subjects[rng.Intn(len(subjects))]
			now := clock + int64(rng.Intn(3))
			if rng.Intn(8) == 0 && clock > 2 {
				now = clock - 1
			}
			op := rng.Intn(5)
			var in, basis, real, naive string
			var realVer, naiveVer int
			switch op {
			case 0:
				mode := []schema.Mode{schema.Backward, schema.Forward, schema.Full}[rng.Intn(3)]
				fs := genFields(rng)
				in = fmt.Sprintf("CREATE %s mode=%v fields=%v now=%d", sn, mode, fs, now)
				v1, e1 := reg.CreateSubject(sn, mode, fs, now)
				o2 := m.create(sn, mode, fs, now)
				c1, d1 := classify(e1)
				real, realVer = fmt.Sprintf("%s %s", c1, d1), v1
				naive, naiveVer = fmt.Sprintf("%s %s", o2.class, o2.detail), o2.ver
				if e1 == nil {
					st.latest[sn] = 1
					st.knownCons[sn] = map[string]bool{}
					clock = now
				}
				basis = "parameter validation then subject existence"
			case 1:
				fs := genFields(rng)
				in = fmt.Sprintf("PUBLISH %s fields=%v now=%d", sn, fs, now)
				v1, e1 := reg.Publish(sn, fs, now)
				o2 := m.publish(sn, fs, now)
				c1, d1 := classify(e1)
				real, realVer = fmt.Sprintf("%s %s", c1, d1), v1
				naive, naiveVer = fmt.Sprintf("%s %s", o2.class, o2.detail), o2.ver
				if e1 == nil {
					st.latest[sn] = v1
					clock = now
				}
				basis = "nochange -> mode CanRead -> non-lagging subscriber CanRead -> accept/lag"
			case 2:
				cn := consumers[rng.Intn(len(consumers))]
				pinned := 1 + rng.Intn(5)
				names := genNames(rng)
				in = fmt.Sprintf("SUBSCRIBE %s %s pinned=%d fields=%v now=%d", sn, cn, pinned, names, now)
				e1 := reg.Subscribe(sn, cn, pinned, names, now)
				o2 := m.subscribe(sn, cn, pinned, names, now)
				c1, d1 := classify(e1)
				real = fmt.Sprintf("%s %s", c1, d1)
				naive = fmt.Sprintf("%s %s", o2.class, o2.detail)
				if e1 == nil {
					st.knownCons[sn][cn] = true
					clock = now
				}
				basis = "project pinned fields, then CanRead(view, latest)"
			case 3:
				cn := consumers[rng.Intn(len(consumers))]
				until := now + int64(rng.Intn(7)-2)
				in = fmt.Sprintf("WAIVE %s %s until=%d now=%d", sn, cn, until, now)
				e1 := reg.Waive(sn, cn, until, now)
				o2 := m.waive(sn, cn, until, now)
				c1, d1 := classify(e1)
				real = fmt.Sprintf("%s %s", c1, d1)
				naive = fmt.Sprintf("%s %s", o2.class, o2.detail)
				if e1 == nil {
					clock = now
				}
				basis = "valid iff now < until at publish time"
			default:
				cn := consumers[rng.Intn(len(consumers))]
				to := 1 + rng.Intn(5)
				names := genNames(rng)
				in = fmt.Sprintf("ADVANCE %s %s to=%d fields=%v now=%d", sn, cn, to, names, now)
				e1 := reg.Advance(sn, cn, to, names, now)
				o2 := m.advance(sn, cn, to, names, now)
				c1, d1 := classify(e1)
				real = fmt.Sprintf("%s %s", c1, d1)
				naive = fmt.Sprintf("%s %s", o2.class, o2.detail)
				if e1 == nil {
					clock = now
				}
				basis = "to>pinned and fields in version to and CanRead(newView, latest)"
			}
			cmpKey := func(s string) string {
				class, _, _ := strings.Cut(s, " ")
				if class == "incompatible" || class == "blocked" {
					return s
				}
				return class
			}
			gotCmp := cmpKey(real)
			refCmp := cmpKey(naive)
			fmt.Fprintf(&log, "seq=%d step=%d\n  IN : %s\n  WHY: %s\n  GOT: %s (ver=%d)\n  REF: %s (ver=%d)\n",
				seq, step, in, basis, real, realVer, naive, naiveVer)
			t.Logf("seq=%d step=%d | %s | got=%s(v=%d) ref=%s(v=%d) [%s]",
				seq, step, in, real, realVer, naive, naiveVer, basis)
			if gotCmp != refCmp || realVer != naiveVer {
				failed = true
				break
			}
			for sn2, cs := range st.knownCons {
				for cn2 := range cs {
					g, e1 := reg.Status(sn2, cn2)
					mc := m.subjects[sn2].consumers[cn2]
					if e1 != nil || mc == nil {
						continue
					}
					if g.Pinned != mc.pinned || g.Lagging != mc.lagging || g.LaggedAtVer != mc.lagged {
						fmt.Fprintf(&log, "STATE DIVERGE %s/%s got=%+v ref={pinned:%d lagging:%v lagged:%d}\n",
							sn2, cn2, g, mc.pinned, mc.lagging, mc.lagged)
						failed = true
					}
				}
			}
			if failed {
				break
			}
		}
		if failed {
			t.Fatalf("sequence %d mismatch\n%s", seq, log.String())
		}
	}
}
