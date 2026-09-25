package lock_test

import (
	"math/rand"
	"strings"
	"testing"

	"ontology/lock"
)

// ref 是按题面规则独立手推的朴素参照；队列条目以 R/W 后缀区分读写。
type ref struct {
	rs map[string]bool
	w  string
	ww int
	q  []string
}

func nref() *ref { return &ref{rs: map[string]bool{}} }
func (m *ref) pump() {
	for len(m.q) > 0 {
		if m.w == "" && len(m.rs) == 0 {
			for i, e := range m.q {
				if strings.HasSuffix(e, "W") {
					m.w, m.ww = e[:len(e)-1], m.ww-1
					m.q = append(m.q[:i], m.q[i+1:]...)
					return
				}
			}
		}
		if m.w == "" && m.ww == 0 && strings.HasSuffix(m.q[0], "R") {
			m.rs[strings.TrimSuffix(m.q[0], "R")] = true
			m.q = m.q[1:]
			continue
		}
		return
	}
}
func (m *ref) ask(id string, wr bool) bool {
	grant := (!wr && m.w == "" && m.ww == 0) || (wr && m.w == "" && len(m.rs) == 0)
	if grant {
		if wr {
			m.w = id
		} else {
			m.rs[id] = true
		}
		return true
	}
	if wr {
		m.ww++
	}
	m.q = append(m.q, id+map[bool]string{true: "W", false: "R"}[wr])
	return false
}
func (m *ref) drop(id string, wr bool) {
	if wr {
		m.w = ""
	} else {
		delete(m.rs, id)
	}
	m.pump()
}
func eq(l *lock.L, m *ref) bool {
	if l.Writer() != m.w || strings.Join(l.Waiting(), ",") != strings.Join(m.q, ",") ||
		len(l.Readers()) != len(m.rs) {
		return false
	}
	for _, r := range l.Readers() {
		if !m.rs[r] {
			return false
		}
	}
	return true
}

func release(t *testing.T, l *lock.L, m *ref, id string, wr bool) {
	t.Helper()
	var e error
	if wr {
		e = l.ReleaseWrite(id)
	} else {
		e = l.ReleaseRead(id)
	}
	if e != nil {
		t.Fatal(e)
	}
	m.drop(id, wr)
}

func TestEightSteps(t *testing.T) {
	l, m := lock.New(), nref()
	steps := []struct {
		wr, rel bool
		id      string
		grant   bool
	}{
		{false, false, "R1", true}, {false, false, "R2", true},
		{true, false, "W1", false}, {false, false, "R3", false},
		{false, true, "R1", false}, {false, true, "R2", false},
		{false, false, "R4", false}, {true, true, "W1", false},
	}
	for i, s := range steps {
		if s.rel {
			release(t, l, m, s.id, s.wr)
		} else {
			g, e := l.Try(s.id, s.wr)
			if e != nil || g != s.grant || g != m.ask(s.id, s.wr) {
				t.Fatalf("step %d g=%v want %v err=%v", i+1, g, s.grant, e)
			}
		}
		if !eq(l, m) {
			t.Fatalf("step %d 偏离参照: %q %q %q", i+1, l.Readers(), l.Writer(), l.Waiting())
		}
	}
	if rs := strings.Join(l.Readers(), ","); rs != "R3,R4" || l.Writer() != "" || len(l.Waiting()) != 0 {
		t.Fatalf("终态 %q %q %q", rs, l.Writer(), l.Waiting())
	}
}

func TestRandomMatchesReference(t *testing.T) {
	for seed := int64(0); seed < 50; seed++ {
		rnd := rand.New(rand.NewSource(seed))
		l, m := lock.New(), nref()
		for n := 0; n < 400; n++ {
			if rnd.Intn(3) == 2 {
				id, wr := m.w, m.w != ""
				if !wr {
					for r := range m.rs {
						id = r
						break
					}
				}
				if id == "" {
					continue
				}
				release(t, l, m, id, wr)
			} else {
				wr := rnd.Intn(2) == 0
				id := string(rune('a'+rnd.Intn(4))) + map[bool]string{true: "W", false: "R"}[wr]
				g, e := l.Try(id, wr)
				if e != nil || g != m.ask(id, wr) {
					t.Fatalf("seed=%d n=%d g=%v err=%v", seed, n, g, e)
				}
			}
			if !eq(l, m) {
				t.Fatalf("seed=%d n=%d 偏离参照: %q %q %q", seed, n, l.Readers(), l.Writer(), l.Waiting())
			}
		}
	}
}
