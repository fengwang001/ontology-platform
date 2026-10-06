package gradeaudit

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

type naiveProp struct {
	teacher ActorID
	score   int
	due     int64
	state   string
}

type naiveSpecial struct {
	actor   ActorID
	score   int
	expires int64
}

type naiveRec struct {
	key      RecordKey
	initial  int64
	versions []Version
	openAt   int64
	closeAt  int64
	appState string
	prop     *naiveProp
	special  *naiveSpecial
}

type naive struct {
	cfg     Config
	levels  map[ActorID]int
	clock   int64
	records map[RecordKey]*naiveRec
	locked  map[SemesterID]bool
	audit   []AuditEntry
	seq     int64
}

func newNaive() *naive {
	return &naive{
		cfg:     testConfig(),
		levels:  map[ActorID]int{"teacher": 1, "boss": 2, "locker": 5, "sp1": 4, "sp2": 4},
		records: map[RecordKey]*naiveRec{},
		locked:  map[SemesterID]bool{},
	}
}

func (n *naive) log(at int64, kind AuditKind, actor ActorID, key RecordKey, score int, reason, detail string) {
	n.seq++
	n.audit = append(n.audit, AuditEntry{Seq: n.seq, At: at, Kind: kind, Actor: actor, Record: key, Score: score, Reason: reason, Detail: detail})
}

func (n *naive) begin(at int64) error {
	if at < n.clock {
		return &Error{Code: ErrClock}
	}
	return nil
}

func (n *naive) commit(at int64) {
	if at > n.clock {
		n.clock = at
	}
}

func nabs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func (n *naive) sweep(rec *naiveRec, at int64) bool {
	changed := false
	if rec.prop != nil && rec.prop.state == "pending" && at > rec.prop.due {
		rec.prop.state = "expired"
		rec.appState = "open"
		n.log(at, AuditProposalExpired, "", rec.key, rec.prop.score, "naive deadline", "naive sweep")
		changed = true
	}
	if rec.special != nil && at > rec.special.expires {
		actor, score := rec.special.actor, rec.special.score
		rec.special = nil
		n.log(at, AuditProposalExpired, actor, rec.key, score, "naive special expiry", "naive sweep")
		changed = true
	}
	return changed
}

func invalidKey(key RecordKey, at int64, actor ActorID) bool {
	return at < 0 || actor == "" || key.Student == "" || key.Course == "" || key.Semester == ""
}

func (n *naive) enter(at int64, actor ActorID, key RecordKey, score int) error {
	if invalidKey(key, at, actor) {
		return &Error{Code: ErrInvalid}
	}
	if score < n.cfg.MinScore || score > n.cfg.MaxScore {
		return &Error{Code: ErrScore}
	}
	if err := n.begin(at); err != nil {
		return err
	}
	if n.cfg.Teachers[key.Course] != actor {
		return &Error{Code: ErrPermission}
	}
	if n.locked[key.Semester] {
		return &Error{Code: ErrLocked}
	}
	if n.records[key] != nil {
		return &Error{Code: ErrState}
	}
	n.records[key] = &naiveRec{key: key, initial: at, openAt: -1, versions: []Version{{Score: score, At: at, Source: SourceInitial}}}
	n.log(at, AuditInitial, actor, key, score, "naive", "")
	n.commit(at)
	return nil
}

func (n *naive) apply(at int64, actor ActorID, key RecordKey) error {
	if invalidKey(key, at, actor) {
		return &Error{Code: ErrInvalid}
	}
	if err := n.begin(at); err != nil {
		return err
	}
	rec := n.records[key]
	if rec == nil {
		return &Error{Code: ErrNotFound}
	}
	if n.locked[key.Semester] {
		return &Error{Code: ErrLocked}
	}
	if ActorID(key.Student) != actor {
		return &Error{Code: ErrPermission}
	}
	if n.sweep(rec, at) {
		n.commit(at)
		return &Error{Code: ErrTimeout}
	}
	if at > rec.initial+n.cfg.ReviewWindow {
		return &Error{Code: ErrTimeout}
	}
	if rec.appState == "open" || rec.appState == "pending" {
		return &Error{Code: ErrState}
	}
	rec.appState = "open"
	rec.openAt = at
	n.log(at, AuditApplication, actor, key, 0, "naive", "")
	n.commit(at)
	return nil
}

func (n *naive) propose(at int64, actor ActorID, key RecordKey, score int) error {
	if invalidKey(key, at, actor) {
		return &Error{Code: ErrInvalid}
	}
	if err := n.begin(at); err != nil {
		return err
	}
	rec := n.records[key]
	if rec == nil {
		return &Error{Code: ErrNotFound}
	}
	if n.locked[key.Semester] {
		return &Error{Code: ErrLocked}
	}
	if n.cfg.Teachers[key.Course] != actor {
		return &Error{Code: ErrPermission}
	}
	if n.sweep(rec, at) {
		n.commit(at)
		return &Error{Code: ErrTimeout}
	}
	if rec.prop != nil && rec.prop.state == "pending" || rec.appState != "open" {
		return &Error{Code: ErrState}
	}
	current := rec.versions[len(rec.versions)-1]
	if score < n.cfg.MinScore || score > n.cfg.MaxScore || nabs(score-current.Score) > n.cfg.MaxScoreDelta {
		return &Error{Code: ErrScore}
	}
	rec.appState = "pending"
	rec.prop = &naiveProp{teacher: actor, score: score, due: at + n.cfg.ApprovalLimit, state: "pending"}
	n.log(at, AuditProposal, actor, key, score, "naive", "")
	n.commit(at)
	return nil
}

func (n *naive) decide(at int64, actor ActorID, key RecordKey, approve bool) error {
	if invalidKey(key, at, actor) {
		return &Error{Code: ErrInvalid}
	}
	if err := n.begin(at); err != nil {
		return err
	}
	rec := n.records[key]
	if rec == nil {
		return &Error{Code: ErrNotFound}
	}
	if n.locked[key.Semester] {
		return &Error{Code: ErrLocked}
	}
	if n.levels[actor] < n.cfg.RequiredApproverLevel {
		return &Error{Code: ErrPermission}
	}
	if rec.prop != nil && rec.prop.state == "pending" && rec.prop.teacher == actor {
		return &Error{Code: ErrPermission}
	}
	if rec.prop != nil && rec.prop.state == "pending" && at > rec.prop.due {
		n.sweep(rec, at)
		n.commit(at)
		return &Error{Code: ErrTimeout}
	}
	if n.sweep(rec, at) {
		n.commit(at)
		return &Error{Code: ErrTimeout}
	}
	if rec.prop == nil || rec.prop.state != "pending" {
		return &Error{Code: ErrState}
	}
	if approve {
		rec.versions = append(rec.versions, Version{Score: rec.prop.score, At: at, Source: SourceReview})
		rec.prop.state = "approved"
		rec.appState = "approved"
		rec.closeAt = at
		n.log(at, AuditApproval, actor, key, rec.prop.score, "naive", "")
	} else {
		rec.prop.state = "rejected"
		rec.appState = "open"
		n.log(at, AuditProposalReject, actor, key, rec.prop.score, "naive", "")
	}
	n.commit(at)
	return nil
}

func (n *naive) reject(at int64, actor ActorID, key RecordKey) error {
	if invalidKey(key, at, actor) {
		return &Error{Code: ErrInvalid}
	}
	if err := n.begin(at); err != nil {
		return err
	}
	rec := n.records[key]
	if rec == nil {
		return &Error{Code: ErrNotFound}
	}
	if n.locked[key.Semester] {
		return &Error{Code: ErrLocked}
	}
	if n.cfg.Teachers[key.Course] != actor {
		return &Error{Code: ErrPermission}
	}
	if n.sweep(rec, at) {
		n.commit(at)
		return &Error{Code: ErrTimeout}
	}
	if rec.appState != "open" {
		return &Error{Code: ErrState}
	}
	rec.appState = "rejected"
	rec.closeAt = at
	n.log(at, AuditApplicationReject, actor, key, 0, "naive", "")
	n.commit(at)
	return nil
}

func (n *naive) lock(at int64, actor ActorID, semester SemesterID) error {
	if at < 0 || actor == "" || semester == "" {
		return &Error{Code: ErrInvalid}
	}
	if err := n.begin(at); err != nil {
		return err
	}
	if n.locked[semester] {
		return &Error{Code: ErrLocked}
	}
	if n.levels[actor] < n.cfg.RequiredLockLevel {
		return &Error{Code: ErrPermission}
	}
	keys := make([]RecordKey, 0, len(n.records))
	byKey := map[RecordKey]*naiveRec{}
	for key, rec := range n.records {
		byKey[key] = rec
		keys = append(keys, key)
	}
	sortRecordsForNaive(keys)
	for _, key := range keys {
		rec := byKey[key]
		if rec.key.Semester != semester {
			continue
		}
		n.sweep(rec, at)
		if rec.prop != nil && rec.prop.state == "pending" {
			rec.prop.state = "expired"
			rec.appState = "locked"
			rec.closeAt = at
			n.log(at, AuditProposalReject, actor, rec.key, rec.prop.score, "naive lock", "")
		} else if rec.appState == "open" || rec.appState == "pending" {
			rec.appState = "locked"
			rec.closeAt = at
			n.log(at, AuditApplicationReject, actor, rec.key, 0, "naive lock", "")
		}
	}
	n.locked[semester] = true
	n.log(at, AuditLock, actor, RecordKey{Semester: semester}, 0, "naive", "")
	n.commit(at)
	return nil
}

func sortRecordsForNaive(keys []RecordKey) {
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			a, b := keys[i], keys[j]
			if a.Semester > b.Semester || (a.Semester == b.Semester && a.Course > b.Course) || (a.Semester == b.Semester && a.Course == b.Course && a.Student > b.Student) {
				keys[i], keys[j] = b, a
			}
		}
	}
}

func (n *naive) special(at int64, actor ActorID, key RecordKey, score int) (bool, error) {
	if invalidKey(key, at, actor) {
		return false, &Error{Code: ErrInvalid}
	}
	if err := n.begin(at); err != nil {
		return false, err
	}
	rec := n.records[key]
	if rec == nil {
		return false, &Error{Code: ErrNotFound}
	}
	if !n.locked[key.Semester] {
		return false, &Error{Code: ErrLocked}
	}
	if n.levels[actor] < n.cfg.SpecialApproverLevel {
		return false, &Error{Code: ErrPermission}
	}
	if rec.special != nil {
		session := rec.special
		if session.score != score {
			return false, &Error{Code: ErrInvalid}
		}
		if actor == session.actor {
			return false, &Error{Code: ErrPermission}
		}
		if at > session.expires {
			n.sweep(rec, at)
			n.commit(at)
			return false, &Error{Code: ErrTimeout}
		}
		rec.versions = append(rec.versions, Version{Score: score, At: at, Source: SourceSpecial})
		rec.special = nil
		n.log(at, AuditSpecialSecond, actor, key, score, "naive", "")
		n.commit(at)
		return true, nil
	}
	if n.sweep(rec, at) {
		n.commit(at)
		return false, &Error{Code: ErrTimeout}
	}
	if score < n.cfg.MinScore || score > n.cfg.MaxScore {
		return false, &Error{Code: ErrScore}
	}
	rec.special = &naiveSpecial{actor: actor, score: score, expires: at + n.cfg.SpecialConfirmTTL}
	n.log(at, AuditSpecialFirst, actor, key, score, "naive", "")
	n.commit(at)
	return false, nil
}

func (n *naive) snapshot(key RecordKey, at, now int64) (Snapshot, error) {
	if at < 0 || now < at || !validKey(key) {
		return Snapshot{}, &Error{Code: ErrInvalid}
	}
	if now < n.clock {
		return Snapshot{}, &Error{Code: ErrClock}
	}
	rec := n.records[key]
	if rec == nil {
		return Snapshot{}, &Error{Code: ErrNotFound}
	}
	n.sweep(rec, now)
	n.commit(now)
	out := Snapshot{Record: key, At: at, UnderReview: rec.openAt >= 0 && rec.openAt <= at && (rec.closeAt == 0 || rec.closeAt > at)}
	for _, version := range rec.versions {
		if version.At <= at {
			out.HasScore = true
			out.Score = version.Score
			out.Source = version.Source
		}
	}
	return out, nil
}

func codeName(err error) string {
	if err == nil {
		return "ok"
	}
	if ge, ok := err.(*Error); ok {
		return string(ge.Code)
	}
	return err.Error()
}

func auditShape(entries []AuditEntry) []AuditKind {
	out := make([]AuditKind, len(entries))
	for i, entry := range entries {
		out[i] = entry.Kind
	}
	return out
}

func TestRandomModelComparison(t *testing.T) {
	rng := rand.New(rand.NewPCG(1473, 20261006))
	keys := []RecordKey{
		{Student: "alice", Course: "math", Semester: "s1"},
		{Student: "bob", Course: "math", Semester: "s1"},
		{Student: "carol", Course: "english", Semester: "s1"},
	}
	scores := []int{70, 80, 85, 90, 95, 100, 60, 101, 59}
	actors := []ActorID{"teacher", "boss", "locker", "sp1", "sp2", "intruder", "alice", "bob", "carol"}

	for iteration := 0; iteration < 300; iteration++ {
		engine := testEngine(t)
		model := newNaive()
		var log strings.Builder
		fmt.Fprintf(&log, "iteration %d\n", iteration)

		for _, key := range keys {
			at := int64(iteration%7) + 1
			score := 80
			fmt.Fprintf(&log, "input enter at=%d actor=teacher key=%+v score=%d\n", at, key, score)
			gotErr := engine.EnterInitialScore(at, "teacher", key, score)
			wantErr := model.enter(at, "teacher", key, score)
			fmt.Fprintf(&log, "output production=%s naive=%s basis=deterministic-seed\n", codeName(gotErr), codeName(wantErr))
			if codeName(gotErr) != codeName(wantErr) {
				t.Fatalf("enter mismatch\n%s", log.String())
			}
		}

		for step := 0; step < 60; step++ {
			at := engine.Clock() + int64(rng.IntN(3))
			key := keys[rng.IntN(len(keys))]
			score := scores[rng.IntN(len(scores))]
			actor := actors[rng.IntN(len(actors))]
			op := rng.IntN(9)
			var got interface{}
			var want interface{}
			switch op {
			case 0:
				fmt.Fprintf(&log, "input apply at=%d actor=%s key=%+v\n", at, actor, key)
				got = codeName(engine.ApplyReview(at, actor, key))
				want = codeName(model.apply(at, actor, key))
			case 1:
				fmt.Fprintf(&log, "input propose at=%d actor=%s score=%d key=%+v\n", at, actor, score, key)
				got = codeName(engine.ProposeChange(at, actor, key, score))
				want = codeName(model.propose(at, actor, key, score))
			case 2:
				fmt.Fprintf(&log, "input decide at=%d actor=%s approve=true key=%+v\n", at, actor, key)
				got = codeName(engine.DecideProposal(at, actor, key, true, "random"))
				want = codeName(model.decide(at, actor, key, true))
			case 3:
				fmt.Fprintf(&log, "input decide at=%d actor=%s approve=false key=%+v\n", at, actor, key)
				got = codeName(engine.DecideProposal(at, actor, key, false, "random"))
				want = codeName(model.decide(at, actor, key, false))
			case 4:
				lockActor := []ActorID{"locker", actor}[rng.IntN(2)]
				fmt.Fprintf(&log, "input lock at=%d actor=%s semester=s1\n", at, lockActor)
				got = codeName(engine.LockSemester(at, lockActor, "s1"))
				want = codeName(model.lock(at, lockActor, "s1"))
			case 5:
				fmt.Fprintf(&log, "input special at=%d actor=%s score=%d key=%+v\n", at, actor, score, key)
				gd, ge := engine.SpecialConfirm(at, actor, key, score)
				wd, we := model.special(at, actor, key, score)
				got = fmt.Sprintf("%s:%v", codeName(ge), gd)
				want = fmt.Sprintf("%s:%v", codeName(we), wd)
			case 6:
				queryAt := at
				if queryAt > engine.Clock() {
					queryAt = engine.Clock()
				}
				if queryAt < 0 {
					queryAt = 0
				}
				fmt.Fprintf(&log, "input snapshot at=%d now=%d key=%+v\n", queryAt, at, key)
				gs, ge := engine.SnapshotAt(key, queryAt, at)
				ws, we := model.snapshot(key, queryAt, at)
				got = fmt.Sprintf("%s:%+v", codeName(ge), gs)
				want = fmt.Sprintf("%s:%+v", codeName(we), ws)
				if codeName(ge) == codeName(we) && ge == nil && !reflect.DeepEqual(gs, ws) {
					t.Fatalf("snapshot mismatch got=%+v want=%+v\n%s", gs, ws, log.String())
				}
			case 7:
				fmt.Fprintf(&log, "input reject at=%d actor=%s key=%+v\n", at, actor, key)
				got = codeName(engine.RejectApplication(at, actor, key, "random"))
				want = codeName(model.reject(at, actor, key))
			default:
				queryAt := engine.Clock()
				fmt.Fprintf(&log, "input average at=%d now=%d student=%s\n", queryAt, at, key.Student)
				gavg, gok, gerr := engine.SemesterAverageAt(key.Student, "s1", queryAt, at)
				wavg, wok, werr := averageNaive(model, key.Student, "s1", queryAt, at)
				got = fmt.Sprintf("%s:%v:%.3f", codeName(gerr), gok, gavg)
				want = fmt.Sprintf("%s:%v:%.3f", codeName(werr), wok, wavg)
				if codeName(gerr) != codeName(werr) || gok != wok || gavg != wavg {
					t.Fatalf("average mismatch got=%v/%v/%v want=%v/%v/%v\n%s", gerr, gok, gavg, werr, wok, wavg, log.String())
				}
			}

			var gotCode, wantCode string
			switch value := got.(type) {
			case string:
				gotCode = value
			}
			switch value := want.(type) {
			case string:
				wantCode = value
			}
			fmt.Fprintf(&log, "output production=%s naive=%s basis=random-operation-%d\n", gotCode, wantCode, op)
			if gotCode != wantCode {
				t.Fatalf("operation %d mismatch\n%s", op, log.String())
			}
			ga, wa := auditShape(engine.AuditLog()), auditShape(model.audit)
			if !reflect.DeepEqual(ga, wa) {
				t.Fatalf("audit kind mismatch\ngot=%v\nwant=%v\n%s", ga, wa, log.String())
			}
		}

		if iteration < 3 {
			t.Logf("\n%s", log.String())
		}
	}
}

func averageNaive(n *naive, student StudentID, semester SemesterID, at, now int64) (float64, bool, error) {
	if at < 0 || now < at {
		return 0, false, &Error{Code: ErrInvalid}
	}
	if now < n.clock {
		return 0, false, &Error{Code: ErrClock}
	}
	sum, count := 0, 0
	for _, rec := range n.records {
		if rec.key.Student != student || rec.key.Semester != semester {
			continue
		}
		n.sweep(rec, now)
		last := Version{}
		found := false
		for _, version := range rec.versions {
			if version.At <= at {
				last, found = version, true
			}
		}
		if found {
			sum += last.Score
			count++
		}
	}
	n.commit(now)
	if count == 0 {
		return 0, false, nil
	}
	return float64(sum) / float64(count), true, nil
}

var _ = fmt.Sprintf
var _ = rand.IntN
var _ = reflect.DeepEqual
var _ = strings.Builder{}
