package plan_test

import (
	"errors"
	"fmt"
	"strings"

	"ontology/diff"
	"ontology/plan"
)

func joinLog(l []string) string { return strings.Join(l, "\n") }

func dumpRows(rows []diff.ResultRow) string {
	parts := make([]string, len(rows))
	name := map[int]string{0: "Missing", 1: "Extra", 2: "Equal", 3: "Changed"}
	for i, r := range rows {
		cols := ""
		if r.Changed&diff.ColD != 0 {
			cols += "d"
		}
		if r.Changed&diff.ColC != 0 {
			cols += "c"
		}
		parts[i] = fmt.Sprintf("(%d,%s,%s)", r.ID, name[r.Class], cols)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func dumpSim(rows []simResult) string {
	drs := make([]diff.ResultRow, len(rows))
	for i, r := range rows {
		m := uint8(0)
		if r.dDiff {
			m |= diff.ColD
		}
		if r.cDiff {
			m |= diff.ColC
		}
		drs[i] = diff.ResultRow{ID: r.id, Class: r.cls, Changed: m}
	}
	return dumpRows(drs)
}

func dumpOps(ops []plan.Op) string {
	parts := make([]string, len(ops))
	kindName := map[int]string{0: "Insert", 1: "Update", 2: "Delete"}
	for i, op := range ops {
		ds := "NULL"
		if op.D != nil {
			ds = fmt.Sprintf("%d", *op.D)
		}
		cs := "NULL"
		if op.C != nil {
			cs = fmt.Sprintf("%q", op.C)
		}
		parts[i] = fmt.Sprintf("(%s id=%d d=%s c=%s mask=%d v=%d)",
			kindName[op.Kind], op.ID, ds, cs, op.Mask, op.Version)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func dumpSimOps(ops []simOp) string {
	parts := make([]string, len(ops))
	kindName := map[int]string{0: "Insert", 1: "Update", 2: "Delete"}
	for i, op := range ops {
		ds := "NULL"
		if op.d.hasD {
			ds = fmt.Sprintf("%d", op.d.d)
		}
		cs := "NULL"
		if op.d.hasC {
			cs = fmt.Sprintf("%q", op.d.c)
		}
		mask := 0
		if op.maskD {
			mask |= int(diff.ColD)
		}
		if op.maskC {
			mask |= int(diff.ColC)
		}
		parts[i] = fmt.Sprintf("(%s id=%d d=%s c=%s mask=%d v=%d)",
			kindName[op.kind], op.id, ds, cs, mask, op.version)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func containsDel(ops []simOp) bool {
	for _, op := range ops {
		if op.kind == 2 {
			return true
		}
	}
	return false
}

func opsMatch(real []plan.Op, sim []simOp) bool {
	if len(real) != len(sim) {
		return false
	}
	for i := range real {
		a, b := real[i], sim[i]
		if a.Kind != b.kind || a.ID != b.id || a.Version != b.version {
			return false
		}
		mask := uint8(0)
		if b.maskD {
			mask |= diff.ColD
		}
		if b.maskC {
			mask |= diff.ColC
		}
		if a.Mask != mask {
			return false
		}
		// Insert 携带全部列；Update 仅携带掩码内的列。
		checkD := b.kind == 0 || b.maskD
		checkC := b.kind == 0 || b.maskC
		if checkD {
			if (a.D == nil) != !b.d.hasD {
				return false
			}
			if a.D != nil && *a.D != b.d.d {
				return false
			}
		}
		if checkC {
			if (a.C == nil) != !b.d.hasC {
				return false
			}
			if a.C != nil && string(a.C) != string(b.d.c) {
				return false
			}
		}
	}
	return true
}

func errorIs(err error, target error) bool { return errors.Is(err, target) }

func applyReason(aerr error, ok, perm bool, stale int64, ni, nu, nd, sni, snu, snd int) string {
	if perm {
		return "权限拒绝：role 非法或含 Delete 而 role 非 2"
	}
	if !ok {
		return fmt.Sprintf("版本失配：最小失配 id=%d，整体拒绝零改动（real err=%v）", stale, aerr)
	}
	return fmt.Sprintf("校验全部通过后整体应用，计数一致(%d,%d,%d)", ni, nu, nd)
}
