package runner

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"ontology/ledger"
	"ontology/script"
)

type oracleScript struct {
	ver     int64
	sum     uint64
	hasUndo bool
}

type oracleRow struct {
	rank   int64
	ver    int64
	sum    uint64
	status ledger.Status
}

type oracleOp struct {
	kind    string
	step    int
	role    int
	now     int64
	ver     int64
	sum     uint64
	hasUndo bool
	to      int64
}

type oracleWorld struct {
	now      int64
	nextRank int64
	scripts  map[int64]oracleScript
	rows     []oracleRow
}

type oracleOutcome struct {
	mr     MigrateResult
	ur     UndoResult
	repair int
	err    string
	errVer int64
	basis  string
}

func newOracleWorld() *oracleWorld {
	return &oracleWorld{
		nextRank: 1,
		scripts:  make(map[int64]oracleScript),
	}
}

func failureKey(step int, ver int64) string {
	return fmt.Sprintf("%d:%d", step, ver)
}

func outcomeError(err error) (string, int64) {
	if err == nil {
		return "", 0
	}
	var verr *VersionError
	if errors.As(err, &verr) {
		return verr.Err.Error(), verr.Ver
	}
	return err.Error(), 0
}

func scriptList(m map[int64]oracleScript) []oracleScript {
	list := make([]oracleScript, 0, len(m))
	for _, s := range m {
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ver < list[j].ver })
	return list
}

func (w *oracleWorld) rejected(op oracleOp, err error, basis string) oracleOutcome {
	text, ver := outcomeError(err)
	return oracleOutcome{err: text, errVer: ver, basis: basis}
}

func (w *oracleWorld) firstFailed() (oracleRow, bool) {
	for _, row := range w.rows {
		if row.status == ledger.StatusFailed {
			return row, true
		}
	}
	return oracleRow{}, false
}

func (w *oracleWorld) applied() (map[int64]oracleRow, int64) {
	applied := make(map[int64]oracleRow)
	var maxVer int64
	for _, row := range w.rows {
		if row.status == ledger.StatusSuccess {
			applied[row.ver] = row
			if row.ver > maxVer {
				maxVer = row.ver
			}
		}
	}
	return applied, maxVer
}

func (w *oracleWorld) appendRow(ver int64, status ledger.Status) {
	w.rows = append(w.rows, oracleRow{
		rank:   w.nextRank,
		ver:    ver,
		sum:    w.scripts[ver].sum,
		status: status,
	})
	w.nextRank++
}

func (w *oracleWorld) run(op oracleOp, fail map[string]bool, ooo bool, lim int) oracleOutcome {
	if op.kind == "register" {
		if op.ver < 1 || op.ver > 1_000_000 {
			return w.rejected(op, ErrInvalid, "invalid ver")
		}
		if op.role < 0 || op.role > 2 {
			return w.rejected(op, ErrInvalid, "register role outside 0..2")
		}
		if op.role < 1 {
			return w.rejected(op, ErrPermission, "register permission")
		}
		w.scripts[op.ver] = oracleScript{ver: op.ver, sum: op.sum, hasUndo: op.hasUndo}
		return oracleOutcome{basis: "register overwrites by ver without ledger or now change"}
	}

	if op.role < 0 || op.role > 2 || op.now < 0 || op.now > 1_000_000_000_000 {
		return w.rejected(op, ErrInvalid, "invalid role/now")
	}
	if op.kind == "undo" && (op.to < 0 || op.to > 1_000_000) {
		return w.rejected(op, ErrInvalid, "invalid to before permission")
	}
	minRole := 1
	if op.kind != "migrate" {
		minRole = 2
	}
	if op.role < minRole {
		return w.rejected(op, ErrPermission, "permission after arguments")
	}
	if op.now < w.now {
		return w.rejected(op, ErrNow, "now regression after permission")
	}

	switch op.kind {
	case "migrate":
		if row, ok := w.firstFailed(); ok {
			return w.rejected(op, versionError(ErrFailed, row.ver), "first F blocks")
		}
		applied, maxVer := w.applied()
		for ver := int64(1); ver <= maxVer; ver++ {
			row, ok := applied[ver]
			if !ok {
				continue
			}
			if current, ok := w.scripts[ver]; ok && current.sum != row.sum {
				return w.rejected(op, versionError(ErrChecksum, ver), "checksum precedes out-of-order")
			}
		}
		pending := make([]oracleScript, 0)
		for _, s := range scriptList(w.scripts) {
			if _, ok := applied[s.ver]; !ok {
				pending = append(pending, s)
			}
		}
		if !ooo {
			for _, s := range pending {
				if s.ver < maxVer {
					return w.rejected(op, versionError(ErrOutOfOrder, s.ver), "smallest hole below max S")
				}
			}
		}
		w.now = op.now
		out := oracleOutcome{basis: "ver ascending, per-script commit, lim truncation", mr: MigrateResult{More: len(pending) > lim}}
		count := len(pending)
		if count > lim {
			count = lim
		}
		for _, s := range pending[:count] {
			if fail[failureKey(op.step, s.ver)] {
				w.appendRow(s.ver, ledger.StatusFailed)
				out.mr.Fail = s.ver
				out.mr.More = false
				out.basis = "callback/panic failure appends F and stops"
				return out
			}
			w.appendRow(s.ver, ledger.StatusSuccess)
			out.mr.Done = append(out.mr.Done, s.ver)
		}
		return out
	case "repair":
		if _, ok := w.firstFailed(); !ok {
			return w.rejected(op, ErrNoFailed, "repair requires F")
		}
		kept := w.rows[:0]
		removed := 0
		for _, row := range w.rows {
			if row.status == ledger.StatusFailed {
				removed++
				continue
			}
			kept = append(kept, row)
		}
		w.rows = kept
		w.now = op.now
		return oracleOutcome{repair: removed, basis: "remove every F; nextRank retained"}
	case "undo":
		if row, ok := w.firstFailed(); ok {
			return w.rejected(op, versionError(ErrFailed, row.ver), "F blocks undo")
		}
		selected := make([]oracleRow, 0)
		for _, row := range w.rows {
			if row.status == ledger.StatusSuccess && row.ver > op.to {
				selected = append(selected, row)
			}
		}
		var missingVer int64
		for _, row := range selected {
			if !w.scripts[row.ver].hasUndo && row.ver > missingVer {
				missingVer = row.ver
			}
		}
		if missingVer != 0 {
			return w.rejected(op, versionError(ErrNoUndo, missingVer), "largest selected ver without undo")
		}
		w.now = op.now
		out := oracleOutcome{basis: "S rows with ver>to undone by descending rank"}
		for i := len(selected) - 1; i >= 0; i-- {
			row := selected[i]
			if fail[failureKey(op.step, row.ver)] {
				w.appendRow(row.ver, ledger.StatusFailed)
				out.ur.Fail = row.ver
				out.basis = "undo failure keeps earlier U and appends F"
				return out
			}
			for i := range w.rows {
				if w.rows[i].rank == row.rank {
					w.rows[i].status = ledger.StatusUndone
				}
			}
			out.ur.Undone = append(out.ur.Undone, row.ver)
		}
		return out
	}
	return w.rejected(op, ErrInvalid, "unknown op")
}

func engineScripts(e *Engine) map[int64]oracleScript {
	result := make(map[int64]oracleScript)
	for _, s := range e.Registered() {
		result[s.Ver] = oracleScript{ver: s.Ver, sum: s.Sum, hasUndo: s.HasUndo}
	}
	return result
}

func engineRows(e *Engine) []oracleRow {
	result := make([]oracleRow, 0)
	for _, row := range e.Ledger() {
		result = append(result, oracleRow{rank: row.Rank, ver: row.Ver, sum: row.Sum, status: row.Status})
	}
	return result
}

func sameWorld(t *testing.T, e *Engine, w *oracleWorld, where string) {
	t.Helper()
	if got := engineScripts(e); !reflect.DeepEqual(got, w.scripts) {
		t.Fatalf("%s: scripts = %#v, want %#v", where, got, w.scripts)
	}
	if got := engineRows(e); !reflect.DeepEqual(got, w.rows) {
		if len(got) == 0 {
			got = nil
		}
		if len(w.rows) == 0 {
			w.rows = nil
		}
		if !reflect.DeepEqual(got, w.rows) {
			t.Fatalf("%s:\n rows  %#v\n want  %#v", where, got, w.rows)
		}
	}
}

func normalizeOutcomeSlices(o *oracleOutcome) {
	if len(o.mr.Done) == 0 {
		o.mr.Done = nil
	}
	if len(o.ur.Undone) == 0 {
		o.ur.Undone = nil
	}
}

func TestRandomOracle(t *testing.T) {
	const sequences = 2000
	rng := rand.New(rand.NewSource(1330))
	for seq := 0; seq < sequences; seq++ {
		ooo := rng.Intn(2) == 0
		lim := 1 + rng.Intn(6)
		w := newOracleWorld()
		ops := make([]oracleOp, 0, 32)
		fail := make(map[string]bool)
		now := int64(0)

		for step := 0; step < 32; step++ {
			if rng.Intn(5) != 0 {
				now += int64(rng.Intn(3))
			} else if now > 0 && rng.Intn(2) == 0 {
				now--
			}
			if now > 1_000_000_000_000 {
				now = 1_000_000_000_000
			}
			op := oracleOp{
				kind:    []string{"register", "migrate", "repair", "undo"}[rng.Intn(4)],
				step:    step,
				role:    rng.Intn(4) - 1,
				now:     now,
				ver:     int64(rng.Intn(10)),
				sum:     rng.Uint64(),
				hasUndo: rng.Intn(5) != 0,
				to:      int64(rng.Intn(11)) - 1,
			}
			if rng.Intn(15) == 0 {
				op.now = -1
			}
			if rng.Intn(15) == 0 {
				op.role = 3
			}
			if op.kind == "register" && rng.Intn(3) == 0 {
				if existing, ok := w.scripts[op.ver]; ok {
					op.sum = existing.sum + 1
				}
			}
			if (op.kind == "migrate" || op.kind == "undo") && op.role >= 1 && op.now >= 0 {
				for _, s := range scriptList(w.scripts) {
					if rng.Intn(3) == 0 {
						fail[failureKey(step, s.ver)] = true
					}
				}
			}
			ops = append(ops, op)
		}

		currentStep := -1
		callFailure := func(ver int64) bool {
			return fail[failureKey(currentStep, ver)]
		}
		e, err := New(ooo, lim, func(ver int64) error {
			if callFailure(ver) {
				if rng.Intn(2) == 0 {
					panic("injected exec panic")
				}
				return errors.New("injected exec error")
			}
			return nil
		}, func(ver int64) error {
			if callFailure(ver) {
				return errors.New("injected undo error")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}

		var log strings.Builder
		fmt.Fprintf(&log, "\nsequence=%d ooo=%v lim=%d", seq, ooo, lim)
		for i, op := range ops {
			currentStep = i
			want := w.run(op, fail, ooo, lim)
			normalizeOutcomeSlices(&want)
			var got oracleOutcome

			input := fmt.Sprintf("step=%d kind=%s role=%d now=%d", i, op.kind, op.role, op.now)
			switch op.kind {
			case "register":
				err = e.Register(op.role, script.Script{Ver: op.ver, Sum: op.sum, HasUndo: op.hasUndo})
				input += fmt.Sprintf(" ver=%d sum=%d undo=%v", op.ver, op.sum, op.hasUndo)
			case "migrate":
				got.mr, err = e.Migrate(op.role, op.now)
			case "repair":
				got.repair, err = e.Repair(op.role, op.now)
			case "undo":
				got.ur, err = e.Undo(op.role, op.now, op.to)
				input += fmt.Sprintf(" to=%d", op.to)
			}
			got.err, got.errVer = outcomeError(err)
			got.basis = want.basis
			normalizeOutcomeSlices(&got)

			fmt.Fprintf(&log, "\n input: %s", input)
			if fail[failureKey(i, op.ver)] {
				fmt.Fprintf(&log, " injected-ver=%d", op.ver)
			}
			fmt.Fprintf(&log, "\n output: mr=%+v ur=%+v repair=%d err=%q errVer=%d", got.mr, got.ur, got.repair, got.err, got.errVer)
			fmt.Fprintf(&log, "\n basis: %s", want.basis)

			if got.err != want.err || got.errVer != want.errVer ||
				!reflect.DeepEqual(got.mr, want.mr) ||
				!reflect.DeepEqual(got.ur, want.ur) ||
				got.repair != want.repair {
				t.Fatalf("sequence %d step %d mismatch%s\n got=%+v\nwant=%+v", seq, i, log.String(), got, want)
			}
			sameWorld(t, e, w, fmt.Sprintf("sequence %d step %d%s", seq, i, log.String()))
		}
		t.Log(log.String())
		t.Logf("sequence %d: 32 operations matched; basis uses fixed rejection order, per-script commits, rank-descending undo, and post-check now acceptance", seq)
	}
}
