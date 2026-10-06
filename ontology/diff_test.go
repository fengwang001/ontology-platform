package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// Op is one operation applied identically to Engine and NaiveEngine.
type Op struct {
	kind     string // addReviewer, addSheet, submit, withdraw, deactivate, trigger
	id, grp  string
	quota    int
	avoids   []string
	sheet    string
	question string
	student  string
	task     string // reviewer whose open task to target
	score    int
	seed     int64
}

type refAPI interface {
	addReviewer(r Reviewer, avoids []string) error
	addSheet(s Sheet) error
	submit(taskID string, score int) error
	withdraw(taskID string) error
	deactivate(id string) error
	trigger()
	view() View
}

type fastRef struct{ e *Engine }

func (f fastRef) addReviewer(r Reviewer, a []string) error { return f.e.AddReviewer(r, a) }
func (f fastRef) addSheet(s Sheet) error                   { return f.e.AddSheet(s) }
func (f fastRef) submit(id string, sc int) error           { return f.e.Submit(id, sc) }
func (f fastRef) withdraw(id string) error                 { return f.e.Withdraw(id) }
func (f fastRef) deactivate(id string) error               { return f.e.Deactivate(id) }
func (f fastRef) trigger()                                 { f.e.TriggerAllocation() }
func (f fastRef) view() View                               { return f.e.Snapshot() }

type naiveRef struct{ n *NaiveEngine }

func (f naiveRef) addReviewer(r Reviewer, a []string) error { return f.n.AddReviewer(r, a) }
func (f naiveRef) addSheet(s Sheet) error                   { return f.n.AddSheet(s) }
func (f naiveRef) submit(id string, sc int) error           { return f.n.Submit(id, sc) }
func (f naiveRef) withdraw(id string) error                 { return f.n.Withdraw(id) }
func (f naiveRef) deactivate(id string) error               { return f.n.Deactivate(id) }
func (f naiveRef) trigger()                                 { f.n.TriggerAllocation() }
func (f naiveRef) view() View                               { return f.n.Snapshot() }

func newConfiguredWorld() (*Engine, *NaiveEngine) {
	e := New()
	n := NewNaive()
	for _, g := range []string{"g1", "g2", "g3", "g4"} {
		_ = e.AddGroup(Group{ID: g})
		_ = n.AddGroup(Group{ID: g})
	}
	for _, q := range []Question{
		{ID: "qa", MaxScore: 10, Step: 2, Threshold: 2},
		{ID: "qb", MaxScore: 12, Step: 3, Threshold: 3},
		{ID: "qc", MaxScore: 5, Step: 1, Threshold: 1},
	} {
		_ = e.AddQuestion(q)
		_ = n.AddQuestion(q)
	}
	return e, n
}

func applyOp(api refAPI, op Op) error {
	switch op.kind {
	case "addReviewer":
		return api.addReviewer(Reviewer{ID: op.id, GroupID: op.grp, Quota: op.quota, Active: true}, op.avoids)
	case "addSheet":
		return api.addSheet(Sheet{ID: op.sheet, Question: op.question, Student: op.student})
	case "submit":
		return api.submit(op.task, op.score)
	case "withdraw":
		return api.withdraw(op.task)
	case "deactivate":
		return api.deactivate(op.id)
	case "trigger":
		api.trigger()
		return nil
	}
	return mkErr(ErrInvalidArgument, "unknown op "+op.kind)
}

// anOpenTask returns a task id held by reviewer in a view, preferring one
// without a score.
func anOpenTask(v View, reviewer string, wantSubmitted bool) string {
	for _, s := range v.Sheets {
		for _, t := range s.Tasks {
			if t.ReviewerID != reviewer || t.Withdrawn {
				continue
			}
			if wantSubmitted == (t.Score != nil) {
				return t.ID
			}
		}
	}
	return ""
}

func activeReviewer(v View, rng *rand.Rand) (string, bool) {
	var ids []string
	for id, r := range v.Reviewers {
		if r.Active {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return "", false
	}
	return ids[rng.Intn(len(ids))], true
}

// TestRandomDifferential runs many generated sequences against both engines
// and asserts identical state, error kinds and audit (incl. basis strings).
func TestRandomDifferential(t *testing.T) {
	const runs, steps = 60, 400
	for run := 0; run < runs; run++ {
		rng := rand.New(rand.NewSource(int64(1000 + run)))
		e, n := newConfiguredWorld()
		fast, naive := fastRef{e}, naiveRef{n}

		nRev, nSheet := 0, 0
		t.Logf("=== run %d seed=%d ===", run, 1000+run)
		for step := 0; step < steps; step++ {
			op := Op{}
			switch rng.Intn(10) {
			case 0, 1, 2: // add reviewer (keeps candidate pool growing)
				nRev++
				op.kind = "addReviewer"
				op.id = fmt.Sprintf("rev%02d", nRev)
				op.grp = []string{"g1", "g2", "g3", "g4"}[rng.Intn(4)]
				op.quota = rng.Intn(4) + 1
				if rng.Intn(3) == 0 {
					op.avoids = []string{fmt.Sprintf("stu%02d", rng.Intn(6)+1)}
				}
			case 3, 4: // add sheet
				nSheet++
				op.kind = "addSheet"
				op.sheet = fmt.Sprintf("sh%03d", nSheet)
				op.question = []string{"qa", "qb", "qc"}[rng.Intn(3)]
				op.student = fmt.Sprintf("stu%02d", rng.Intn(8)+1)
			default: // operate on an existing reviewer
				v := fast.view()
				id, ok := activeReviewer(v, rng)
				if !ok {
					step--
					continue
				}
				op.id = id
				switch rng.Intn(10) {
				case 0, 1, 2, 3, 4: // submit a valid grid score
					task := anOpenTask(v, id, false)
					if task == "" {
						step--
						continue
					}
					op.kind, op.task = "submit", task
					var q Question
					for _, s := range v.Sheets {
						for _, tk := range s.Tasks {
							if tk.ID == task {
								q = v.Questions[s.Question]
							}
						}
					}
					op.score = rng.Intn(q.MaxScore/q.Step+1) * q.Step
				case 5: // submit an invalid score sometimes
					task := anOpenTask(v, id, false)
					if task == "" {
						step--
						continue
					}
					op.kind, op.task, op.score = "submit", task, []int{-1, 1, 99}[rng.Intn(3)]
				case 6, 7: // withdraw
					task := anOpenTask(v, id, false)
					if task == "" {
						step--
						continue
					}
					op.kind, op.task = "withdraw", task
				case 8: // duplicate submit
					task := anOpenTask(v, id, true)
					if task == "" {
						step--
						continue
					}
					op.kind, op.task, op.score = "submit", task, 0
				default:
					op.kind = "deactivate"
				}
			}
			errF := applyOp(fast, op)
			errN := applyOp(naive, op)
			t.Logf("step %d in=%+v -> fast=%v naive=%v", step, op, errF, errN)
			if KindOf(errF) != KindOf(errN) {
				t.Fatalf("run %d step %d error kind mismatch: fast=%v naive=%v op=%+v",
					run, step, errF, errN, op)
			}
			vF, vN := canonicalView(fast.view()), canonicalView(naive.view())
			if !reflect.DeepEqual(vF, vN) {
				t.Fatalf("run %d step %d state divergence op=%+v\nrevF=%#v\nrevN=%#v\nsheetsF=%#v\nsheetsN=%#v\nevF=%#v\nevN=%#v",
					run, step, op, vF.Reviewers, vN.Reviewers, vF.Sheets, vN.Sheets, vF.Events, vN.Events)
			}
		}
		fast.trigger()
		naive.trigger()
		if !reflect.DeepEqual(canonicalView(fast.view()), canonicalView(naive.view())) {
			t.Fatalf("run %d post-trigger divergence", run)
		}
	}
}

// canonicalView turns nil slices into empty ones so comparison reflects
// logical state rather than allocation style.
func canonicalView(v View) View {
	if v.Events == nil {
		v.Events = []Event{}
	}
	for i := range v.Sheets {
		if v.Sheets[i].Tasks == nil {
			v.Sheets[i].Tasks = []TaskView{}
		}
	}
	return v
}

func dumpView(v View) string {
	return fmt.Sprintf("sheets=%d events=%d", len(v.Sheets), len(v.Events))
}
