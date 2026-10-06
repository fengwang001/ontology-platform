package enrollment

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// universe is a randomly generated scenario applied identically to the
// engine and to the naive model.
type universe struct {
	cfg      Config
	courses  []CourseID
	credits  map[CourseID]int
	sections []Section
	byCourse map[CourseID][]SectionID
	prereq   [][2]CourseID
	coreq    [][2]CourseID
	mutex    [][2]CourseID
	students []StudentID
	records  map[StudentID]map[CourseID]int
}

func genUniverse(r *rand.Rand) *universe {
	u := &universe{
		cfg:      Config{PassLine: 60, MaxCredits: 6 + r.Intn(7)},
		credits:  make(map[CourseID]int),
		byCourse: make(map[CourseID][]SectionID),
		records:  make(map[StudentID]map[CourseID]int),
	}
	for i := 0; i < 8; i++ {
		c := CourseID(fmt.Sprintf("c%d", i))
		u.courses = append(u.courses, c)
		u.credits[c] = 1 + r.Intn(4)
		for j := 0; j < 1+r.Intn(2); j++ {
			sid := SectionID(fmt.Sprintf("sec_%s_%d", c, j))
			slots := make(map[Slot]bool)
			for k := 0; k < 1+r.Intn(3); k++ {
				slots[Slot(r.Intn(12))] = true
			}
			u.sections = append(u.sections, Section{
				ID: sid, Course: c, Capacity: r.Intn(4), Slots: slots,
			})
			u.byCourse[c] = append(u.byCourse[c], sid)
		}
	}
	for i := 0; i < len(u.courses); i++ {
		for j := i + 1; j < len(u.courses); j++ {
			if r.Intn(100) < 12 { // acyclic by construction: only j -> i
				u.prereq = append(u.prereq, [2]CourseID{u.courses[j], u.courses[i]})
			}
			if r.Intn(100) < 6 {
				u.coreq = append(u.coreq, [2]CourseID{u.courses[i], u.courses[j]})
			}
			if r.Intn(100) < 6 {
				u.mutex = append(u.mutex, [2]CourseID{u.courses[i], u.courses[j]})
			}
		}
	}
	for i := 0; i < 5; i++ {
		s := StudentID(fmt.Sprintf("s%d", i))
		u.students = append(u.students, s)
		u.records[s] = make(map[CourseID]int)
		for _, c := range u.courses {
			if r.Intn(100) < 25 {
				u.records[s][c] = r.Intn(101)
			}
		}
	}
	return u
}

func (u *universe) buildEngine() *Engine {
	e := NewEngine(u.cfg)
	for _, c := range u.courses {
		e.AddCourse(c, u.credits[c])
	}
	for _, sec := range u.sections {
		e.AddSection(sec.ID, sec.Course, sec.Capacity, slotList(sec.Slots)...)
	}
	for _, p := range u.prereq {
		e.AddPrereq(p[0], p[1])
	}
	for _, p := range u.coreq {
		e.AddCoreq(p[0], p[1])
	}
	for _, p := range u.mutex {
		e.AddMutex(p[0], p[1])
	}
	for s, recs := range u.records {
		for c, g := range recs {
			e.SetRecord(s, c, g)
		}
	}
	return e
}

func (u *universe) buildNaive() *naive {
	n := newNaive(u.cfg)
	for _, c := range u.courses {
		n.addCourse(c, u.credits[c])
	}
	for _, sec := range u.sections {
		n.addSection(sec.ID, sec.Course, sec.Capacity, slotList(sec.Slots)...)
	}
	for _, p := range u.prereq {
		n.addPrereq(p[0], p[1])
	}
	for _, p := range u.coreq {
		n.addCoreq(p[0], p[1])
	}
	for _, p := range u.mutex {
		n.addMutex(p[0], p[1])
	}
	for s, recs := range u.records {
		for c, g := range recs {
			n.setRecord(s, c, g)
		}
	}
	return n
}

func slotList(set map[Slot]bool) []Slot {
	out := make([]Slot, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func engineDigest(e *Engine, u *universe) string {
	var b strings.Builder
	for _, s := range u.students {
		en := e.Enrolled(s)
		keys := make([]CourseID, 0, len(en))
		for c := range en {
			keys = append(keys, c)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		fmt.Fprintf(&b, "%s[", s)
		for _, c := range keys {
			fmt.Fprintf(&b, "%s=%s,", c, en[c])
		}
		b.WriteString("]")
	}
	for _, sec := range u.sections {
		fmt.Fprintf(&b, "%s:%d;", sec.ID, e.Count(sec.ID))
	}
	return b.String()
}

func naiveDigest(n *naive, u *universe) string {
	var b strings.Builder
	for _, s := range u.students {
		list := append([]naiveEnroll(nil), n.enroll[s]...)
		sort.Slice(list, func(i, j int) bool { return list[i].course < list[j].course })
		fmt.Fprintf(&b, "%s[", s)
		for _, en := range list {
			fmt.Fprintf(&b, "%s=%s,", en.course, en.section)
		}
		b.WriteString("]")
	}
	for _, sec := range u.sections {
		fmt.Fprintf(&b, "%s:%d;", sec.ID, n.count(sec.ID))
	}
	return b.String()
}

func codeOf(err *Error) string {
	if err == nil {
		return "ok"
	}
	return err.Code.String()
}

func courseOf(err *Error) CourseID {
	if err == nil {
		return ""
	}
	return err.Course
}

func TestDifferentialRandom(t *testing.T) {
	for seed := int64(0); seed < 30; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runDifferential(t, seed)
		})
	}
}

func runDifferential(t *testing.T, seed int64) {
	r := rand.New(rand.NewSource(seed))
	u := genUniverse(r)
	e := u.buildEngine()
	nv := u.buildNaive()
	t.Logf("universe: cfg=%+v courses=%d sections=%d prereq=%v coreq=%v mutex=%v",
		u.cfg, len(u.courses), len(u.sections), u.prereq, u.coreq, u.mutex)

	for step := 0; step < 300; step++ {
		stu := u.students[r.Intn(len(u.students))]
		var desc string
		var errE, errN *Error
		switch x := r.Intn(100); {
		case x < 45: // batch enrollment
			picks := make([]Pick, 0, 1+r.Intn(3))
			for i := 0; i < cap(picks); i++ {
				c := u.courses[r.Intn(len(u.courses))]
				var sid SectionID
				if r.Intn(100) < 80 {
					secs := u.byCourse[c]
					sid = secs[r.Intn(len(secs))]
				} else {
					sid = u.sections[r.Intn(len(u.sections))].ID
					if r.Intn(100) < 10 {
						sid = "sec_ghost"
					}
				}
				picks = append(picks, Pick{Course: c, Section: sid})
			}
			desc = fmt.Sprintf("batch(%s, %v)", stu, picks)
			errE = e.BatchEnroll(stu, picks)
			errN = nv.batchEnroll(stu, picks)
		case x < 65: // drop
			c := u.courses[r.Intn(len(u.courses))]
			if r.Intn(100) < 10 {
				c = "c_ghost"
			}
			desc = fmt.Sprintf("drop(%s, %s)", stu, c)
			errE = e.Drop(stu, c)
			errN = nv.drop(stu, c)
		case x < 85: // switch
			var oldSec SectionID
			en := e.Enrolled(stu)
			if r.Intn(100) < 70 && len(en) > 0 {
				secs := make([]SectionID, 0, len(en))
				for _, v := range en {
					secs = append(secs, v)
				}
				sort.Slice(secs, func(i, j int) bool { return secs[i] < secs[j] })
				oldSec = secs[r.Intn(len(secs))]
			} else {
				oldSec = u.sections[r.Intn(len(u.sections))].ID
			}
			newSec := u.sections[r.Intn(len(u.sections))].ID
			if r.Intn(100) < 5 {
				newSec = oldSec
			}
			desc = fmt.Sprintf("switch(%s, %s -> %s)", stu, oldSec, newSec)
			errE = e.Switch(stu, oldSec, newSec)
			errN = nv.switchSec(stu, oldSec, newSec)
		default: // capacity adjustment
			sec := u.sections[r.Intn(len(u.sections))].ID
			capacity := r.Intn(5)
			if r.Intn(100) < 5 {
				capacity = -1
			}
			desc = fmt.Sprintf("setCapacity(%s, %d)", sec, capacity)
			errE = e.SetCapacity(sec, capacity)
			errN = nv.setCapacity(sec, capacity)
		}

		t.Logf("step=%03d op=%s => engine=%s(%s) naive=%s(%s) basis=%q",
			step, desc, codeOf(errE), courseOf(errE), codeOf(errN), courseOf(errN),
			detailOf(errE))

		if codeOf(errE) != codeOf(errN) || courseOf(errE) != courseOf(errN) {
			t.Fatalf("step %d %s: outcome mismatch: engine=%s(%s) naive=%s(%s)",
				step, desc, codeOf(errE), courseOf(errE), codeOf(errN), courseOf(errN))
		}
		if dE, dN := engineDigest(e, u), naiveDigest(nv, u); dE != dN {
			t.Fatalf("step %d %s: state divergence:\nengine: %s\nnaive:  %s",
				step, desc, dE, dN)
		}
	}
}

func detailOf(err *Error) string {
	if err == nil {
		return ""
	}
	return err.Detail
}
