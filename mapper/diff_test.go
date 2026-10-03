package mapper

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"
)

const diffOperations = 2000
const diffTrials = 40

func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	var log strings.Builder
	fmt.Fprintf(&log, "# differential run: %d ops\n\n", diffOperations)

	for trial := 0; trial < diffTrials; trial++ {
		cfg := Config{
			MaxBytes:   16 + rng.Intn(40),
			MaxEntries: 1 + rng.Intn(40),
		}
		cfg.MaxPath = cfg.MaxBytes + rng.Intn(80)
		fmt.Fprintf(&log, "## config MaxBytes=%d MaxPath=%d MaxEntries=%d\n",
			cfg.MaxBytes, cfg.MaxPath, cfg.MaxEntries)

		real := New(cfg)
		oracle := newNaive(cfg)

		// Track existing dirs to pick valid parents from.
		type node struct {
			id   int
			src  string
			dir  bool
			kids map[string]bool
		}

		genName := func() string {
			if rng.Intn(10) == 0 {
				// Sometimes an invalid name: slash, NUL, empty, bad UTF-8.
				switch rng.Intn(4) {
				case 0:
					return ""
				case 1:
					return "a/b"
				case 2:
					return "a\x00b"
				case 3:
					return "\xff\xfe"
				}
			}
			pool := []rune("abcABCxyzXYZ._- ")
			// Sprinkle target-forbidden / reserved-triggering chars.
			pool = append(pool, []rune("<>?:\"\\|*%")...)
			if rng.Intn(3) == 0 {
				pool = append(pool, []rune("好é")...)
			}
			n := 1 + rng.Intn(24)
			var b strings.Builder
			for i := 0; i < n; i++ {
				b.WriteRune(pool[rng.Intn(len(pool))])
			}
			s := b.String()
			// Occasionally force a reserved stem.
			if rng.Intn(8) == 0 {
				res := []string{"CON", "nul", "lpt3", "Aux", "Prn.9", ".con"}
				s = res[rng.Intn(len(res))]
				if rng.Intn(2) == 0 {
					s += ".txt"
				}
			}
			if rng.Intn(12) == 0 {
				// Leading dot => ext is the whole t0 and cannot shrink:
				// a collision candidate then yields ErrCannotFit.
				s = "." + strings.Repeat("q", 5+rng.Intn(30))
			}
			return s
		}

		allDirs := []int{0}
		// src lookup per dir for realistic removes/renames:
		srcs := map[int][]string{0: {}}

		pickParent := func() int {
			return allDirs[rng.Intn(len(allDirs))]
		}

		opsThisTrial := diffOperations / diffTrials
		if trial == 0 {
			opsThisTrial += diffOperations % diffTrials
		}
		for op := 0; op < opsThisTrial; op++ {
			kind := rng.Intn(10)
			switch {
			case kind < 5: // Add
				parent := pickParent()
				src := genName()
				isDir := rng.Intn(2) == 0
				id, rerr := real.Add(parent, src, isDir)
				oid, oerr := oracle.add(parent, src, isDir)
				fmt.Fprintf(&log, "op=%d Add(parent=%d,src=%q,isDir=%v) -> real(%d,%v) oracle(%d,%v)\n",
					op, parent, src, isDir, id, errName(rerr), oid, errName(oerr))
				if !sameErr(rerr, oerr) {
					t.Fatalf("op %d Add %q error mismatch real=%v oracle=%v\n%s", op, src, rerr, oerr, log.String())
				}
				if rerr == nil {
					if id != oid {
						t.Fatalf("op %d id mismatch real=%d oracle=%d", op, id, oid)
					}
					srcs[parent] = append(srcs[parent], src)
					if isDir {
						allDirs = append(allDirs, id)
						srcs[id] = nil
					}
				}
			case kind < 8: // Rename
				parent := pickParent()
				var src string
				if len(srcs[parent]) > 0 && rng.Intn(5) != 0 {
					src = srcs[parent][rng.Intn(len(srcs[parent]))]
				} else {
					src = genName() // likely nonexistent -> ErrNotFound
				}
				newSrc := genName()
				rerr := real.Rename(parent, src, newSrc)
				oerr := oracle.rename(parent, src, newSrc)
				fmt.Fprintf(&log, "op=%d Rename(parent=%d,%q -> %q) -> real(%v) oracle(%v)\n",
					op, parent, src, newSrc, errName(rerr), errName(oerr))
				if !sameErr(rerr, oerr) {
					t.Fatalf("op %d Rename %q->%q error mismatch real=%v oracle=%v\n%s",
						op, src, newSrc, rerr, oerr, log.String())
				}
				if rerr == nil {
					for i, s := range srcs[parent] {
						if s == src {
							srcs[parent][i] = newSrc
						}
					}
				}
			default: // Remove
				parent := pickParent()
				var src string
				if len(srcs[parent]) > 0 && rng.Intn(5) != 0 {
					src = srcs[parent][rng.Intn(len(srcs[parent]))]
				} else {
					src = genName()
				}
				rerr := real.Remove(parent, src)
				oerr := oracle.remove(parent, src)
				fmt.Fprintf(&log, "op=%d Remove(parent=%d,src=%q) -> real(%v) oracle(%v)\n",
					op, parent, src, errName(rerr), errName(oerr))
				if !sameErr(rerr, oerr) {
					t.Fatalf("op %d Remove %q error mismatch real=%v oracle=%v\n%s",
						op, src, rerr, oerr, log.String())
				}
				if rerr == nil {
					out := srcs[parent][:0]
					for _, s := range srcs[parent] {
						if s != src {
							out = append(out, s)
						}
					}
					srcs[parent] = out
				}
			}

			// After every op, compare full mapped-tree snapshots and all
			// path strings; this is the judgment basis for equivalence.
			if diff := compareStates(real, oracle); diff != "" {
				t.Fatalf("op %d state divergence:\n%s\nLOG:\n%s", op, diff, log.String())
			}
		}
	}

	if err := os.WriteFile("diff_run.log", []byte(log.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("differential log written to mapper/diff_run.log (%d ops)", diffOperations)
}

func errName(err error) string {
	if err == nil {
		return "nil"
	}
	for _, e := range []error{
		ErrInvalidName, ErrNoParent, ErrExists, ErrNotFound,
		ErrFull, ErrCannotFit, ErrPathTooLong, ErrNotEmpty,
	} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	return err.Error()
}

func sameErr(a, b error) bool { return errName(a) == errName(b) }

func compareStates(real *Mapper, oracle *naiveMapper) string {
	// Compare via Path(id) for every oracle id, plus directory name listings.
	for id, oe := range oracle.entries {
		rp, rerr := real.Path(id)
		if rerr != nil {
			return fmt.Sprintf("id %d missing in real (%v)", id, rerr)
		}
		op := oraclePath(oracle, oe)
		if rp != op {
			return fmt.Sprintf("id %d path real=%q oracle=%q", id, rp, op)
		}
		rns, _ := real.Names(id)
		onames := []string{}
		if oe.dir {
			for _, e := range oe.kids {
				onames = append(onames, e.mapped)
			}
			sort.Strings(onames)
		}
		if strings.Join(rns, "|") != strings.Join(onames, "|") {
			return fmt.Sprintf("id %d names real=%v oracle=%v", id, rns, onames)
		}
	}
	// Real must not contain extra ids.
	for id := range real.entries {
		if _, ok := oracle.entries[id]; !ok {
			return fmt.Sprintf("real has extra id %d", id)
		}
	}
	return ""
}

func oraclePath(n *naiveMapper, e *naiveEntry) string {
	if e.id == 0 {
		return ""
	}
	var parts []string
	for cur := e; cur.id != 0; cur = n.entries[cur.parent] {
		parts = append(parts, cur.mapped)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "/")
}
