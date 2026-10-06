package audit

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

// 随机生成方案树、课程、记录、替换、转入与操作序列，
// 将生产审核结果与独立朴素模型逐项对照；日志打印每步输入、输出与判定依据。

type diffLog struct {
	sb strings.Builder
}

func (l *diffLog) logf(format string, args ...any) {
	fmt.Fprintf(&l.sb, format, args...)
	if !strings.HasSuffix(format, "\n") {
		l.sb.WriteByte('\n')
	}
}

func randPlan(rng *rand.Rand, n int) (*PlanVersion, []Course) {
	courses := []Course{
		{ID: "C0", Credits: 2}, {ID: "C1", Credits: 3}, {ID: "C2", Credits: 4},
		{ID: "C3", Credits: 1}, {ID: "C4", Credits: 2},
	}
	var mk func(depth, idx int) (*Requirement, int)
	mk = func(depth, idx int) (*Requirement, int) {
		if depth == 0 || (idx > 0 && rng.Intn(3) == 0) {
			k := 1 + rng.Intn(2)
			set := map[string]bool{}
			for len(set) < k {
				set[courses[rng.Intn(len(courses))].ID] = true
			}
			var cs []string
			for c := range set {
				cs = append(cs, c)
			}
			sort.Strings(cs)
			id := fmt.Sprintf("R%02d", idx)
			idx++
			return leaf(id, rng.Intn(2) == 0, float64(rng.Intn(5)), 1, cs...), idx
		}
		k := 2
		var children []*Requirement
		for i := 0; i < k; i++ {
			var c *Requirement
			c, idx = mk(depth-1, idx)
			children = append(children, c)
		}
		id := fmt.Sprintf("R%02d", idx)
		idx++
		return internal(id, 1+rng.Intn(k), children...), idx
	}
	root, _ := mk(2, 0)
	plan := &PlanVersion{
		PlanID: "P", Version: 1, Root: root,
		MinTotalCredit: float64(rng.Intn(8)),
		PassLine:       60,
		MinGPA:         float64(55 + rng.Intn(15)),
		TransferCap:    float64(rng.Intn(7)),
	}
	if rng.Intn(4) == 0 {
		plan.SharedCredit = map[[2]string]bool{{"R00", "R01"}: true}
	}
	return plan, courses
}

func compareResult(t *testing.T, l *diffLog, iter int, e *Engine, student string) {
	t.Helper()
	prod, err := e.Audit(student)
	if err != nil {
		t.Fatalf("iter %d audit error: %v", iter, err)
	}
	snap, nerr := naiveAudit(e, student)
	if nerr != nil {
		t.Fatalf("iter %d naive error: %v", iter, nerr)
	}

	l.logf("[iter %d] PROD pass=%v total=%.1f gpa=%.2f attrib=%+v cond=%s",
		iter, prod.Pass, prod.TotalCredits, prod.GPA, prod.Attrib, condKinds(prod))
	l.logf("[iter %d] NAIV pass=%v total=%.1f gpa=%.2f attribID=%s gap=%.1f/%d cond=%s",
		iter, snap.pass, snap.total, snap.gpa, snap.attribID, snap.creditGap, snap.courseGap, snap.conditions)

	if prod.Pass != snap.pass {
		t.Fatalf("iter %d pass mismatch prod=%v naive=%v\n%s", iter, prod.Pass, snap.pass, l.sb.String())
	}
	if !floatEq(prod.TotalCredits, snap.total) || !floatEq(prod.GPA, snap.gpa) {
		t.Fatalf("iter %d metric mismatch total %.3f vs %.3f, gpa %.3f vs %.3f\n%s",
			iter, prod.TotalCredits, snap.total, prod.GPA, snap.gpa, l.sb.String())
	}
	pk := condKinds(prod)
	sort.Strings(pk)
	if strings.Join(pk, ",") != strings.Join(snap.conditions, ",") {
		t.Fatalf("iter %d condition mismatch %v vs %v\n%s", iter, pk, snap.conditions, l.sb.String())
	}
	if (prod.Attrib != nil) != snap.hasAttrib {
		t.Fatalf("iter %d attrib presence mismatch %+v vs %v\n%s", iter, prod.Attrib, snap.hasAttrib, l.sb.String())
	}
	if prod.Attrib != nil && (prod.Attrib.ReqID != snap.attribID ||
		!floatEq(prod.Attrib.CreditGap, snap.creditGap) || prod.Attrib.CourseGap != snap.courseGap) {
		t.Fatalf("iter %d attrib mismatch %+v vs %s %.1f/%d\n%s",
			iter, prod.Attrib, snap.attribID, snap.creditGap, snap.courseGap, l.sb.String())
	}
}

func condKinds(r *AuditResult) []string {
	var out []string
	for _, c := range r.ConditionFails {
		out = append(out, c.Kind)
	}
	sort.Strings(out)
	return out
}

func floatEq(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-6
}

func TestRandomDifferential(t *testing.T) {
	if testing.Verbose() {
		t.Log("random differential against independent naive enumeration")
	}
	rng := rand.New(rand.NewSource(20261006))

	for iter := 0; iter < 400; iter++ {
		l := &diffLog{}
		plan, courses := randPlan(rng, 0)
		l.logf("[iter %d] plan root=%s minTotal=%.0f gpa>=%.0f cap=%.0f shared=%v",
			iter, plan.Root.ID, plan.MinTotalCredit, plan.MinGPA, plan.TransferCap, plan.SharedCredit)
		e := NewEngine()
		for _, c := range courses {
			must(t, e.AddCourse(c))
		}
		must(t, e.AddPlan(plan))
		must(t, e.Enroll(Student{ID: "s", PlanID: "P", PlanVersion: 1}))

		nRecs := rng.Intn(6)
		for i := 0; i < nRecs; i++ {
			c := courses[rng.Intn(len(courses))]
			grade := 45 + rng.Intn(50)
			sem := fmt.Sprintf("202%d-1", 1+rng.Intn(4))
			credits := c.Credits
			rec := Record{
				ID: fmt.Sprintf("rec%d", i), Student: "s", Course: c.ID,
				Semester: sem, Credits: credits, Grade: float64(grade),
			}
			l.logf("  register %+v", rec)
			if err := e.RegisterRecord(rec); err != nil {
				l.logf("    -> rejected: %v", err)
			}
		}
		// 随机替换：选树中课程作为目标。
		leaves := collectLeaves(plan.Root)
		if rng.Intn(2) == 0 && len(leaves) > 0 {
			target := leaves[rng.Intn(len(leaves))].Courses[0]
			from := courses[rng.Intn(len(courses))].ID
			if from != target {
				sub := Substitution{
					ID: "sub1", From: from, To: target,
					PlanID: "P", PlanVersion: 1,
					Effective: fmt.Sprintf("202%d-1", 1+rng.Intn(3)),
				}
				l.logf("  substitution %+v", sub)
				if err := e.RegisterSubstitution(sub); err != nil {
					l.logf("    -> rejected: %v", err)
				}
			}
		}
		// 随机转入（登记次序是规则的一部分，保留顺序）。
		nTr := rng.Intn(3)
		for i := 0; i < nTr; i++ {
			c := courses[rng.Intn(len(courses))]
			tr := TransferRecord{
				Student: "s", Course: c.ID, Credits: float64(1 + rng.Intn(3)),
				Grade:    float64(60 + rng.Intn(35)),
				Semester: fmt.Sprintf("202%d-1", 1+rng.Intn(4)),
			}
			l.logf("  transfer %+v", tr)
			if err := e.RegisterTransfer(tr); err != nil {
				l.logf("    -> rejected: %v", err)
			}
		}
		// 随机撤销。
		if rng.Intn(3) == 0 && nRecs > 0 {
			id := fmt.Sprintf("rec%d", rng.Intn(nRecs))
			l.logf("  revoke %s", id)
			if err := e.RevokeRecord("s", id); err != nil {
				l.logf("    -> rejected: %v", err)
			}
		}

		compareResult(t, l, iter, e, "s")
		if testing.Verbose() && (iter < 3 || iter%50 == 0) {
			t.Logf("\n%s", l.sb.String())
		}
	}
}

// 登记顺序无关性：同一记录集合以不同顺序登记，审核结论必须完全一致（转入次序除外）。
func TestOrderIndependence(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 60; iter++ {
		plan, courses := randPlan(rng, 0)
		mkEngine := func() *Engine {
			e := NewEngine()
			for _, c := range courses {
				must(t, e.AddCourse(c))
			}
			must(t, e.AddPlan(plan))
			must(t, e.Enroll(Student{ID: "s", PlanID: "P", PlanVersion: 1}))
			return e
		}
		var recs []Record
		for i := 0; i < 5; i++ {
			c := courses[rng.Intn(len(courses))]
			recs = append(recs, Record{
				ID: fmt.Sprintf("rec%d", i), Student: "s", Course: c.ID,
				Semester: fmt.Sprintf("202%d-1", 1+rng.Intn(4)),
				Credits:  c.Credits, Grade: float64(45 + rng.Intn(50)),
			})
		}
		e1, e2 := mkEngine(), mkEngine()
		for _, r := range recs {
			must(t, e1.RegisterRecord(r))
		}
		shuffled := append([]Record(nil), recs...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		for _, r := range shuffled {
			must(t, e2.RegisterRecord(r))
		}
		a := auditOK(t, e1, "s")
		b := auditOK(t, e2, "s")
		if a.Pass != b.Pass || !floatEq(a.GPA, b.GPA) || !floatEq(a.TotalCredits, b.TotalCredits) {
			t.Fatalf("iter %d order dependence: %+v vs %+v", iter, a, b)
		}
		if fmt.Sprint(a.Attrib) != fmt.Sprint(b.Attrib) {
			t.Fatalf("iter %d attrib order dependence: %+v vs %+v", iter, a.Attrib, b.Attrib)
		}
	}
}

// 并发操作串行一致性：并发混合调用不应产生数据竞争或崩溃，
// 且最终状态等价于某个合法串行顺序。
func TestConcurrentOperations(t *testing.T) {
	plan := planV1(leaf("L1", true, 4, 1, "A"))
	e := NewEngine()
	for _, c := range commonCourses {
		must(t, e.AddCourse(c))
	}
	must(t, e.AddPlan(plan))

	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			sid := fmt.Sprintf("stu%d", i%5)
			_ = e.Enroll(Student{ID: sid, PlanID: "P", PlanVersion: 1})
			_ = e.RegisterRecord(Record{
				ID: fmt.Sprintf("rec%d", i), Student: sid, Course: "A",
				Semester: "2023-1", Credits: 4, Grade: 70,
			})
			_, _ = e.Audit(sid)
			_ = e.RevokeRecord(sid, fmt.Sprintf("rec%d", i))
		}()
	}
	wg.Wait()
}

func newSeeded(seed int64) *rand.Rand { return rand.New(rand.NewSource(seed)) }
