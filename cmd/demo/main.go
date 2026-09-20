// Command demo exercises the config snapshot manager end to end and prints
// one OK/FAIL verdict line per scenario plus a final summary.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"ontology"
)

var passed, failed int

func check(ok bool, label string, detail ...any) {
	msg := strings.TrimRight(fmt.Sprintln(detail...), "\n")
	if ok {
		passed++
		fmt.Printf("OK   %s %s\n", label, msg)
	} else {
		failed++
		fmt.Printf("FAIL %s %s\n", label, msg)
	}
}

func newManager() *ontology.Manager {
	decls := []ontology.FieldDecl{
		{Name: "host", Type: ontology.StringField},
		{Name: "port", Type: ontology.IntField, Min: 1, Max: 65535},
		{Name: "retries", Type: ontology.IntField, Min: 0, Max: 10},
	}
	m, err := ontology.NewManager(decls, map[string]any{"host": "a", "port": 80, "retries": 3})
	if err != nil {
		fmt.Println("FAIL setup", err)
		os.Exit(1)
	}
	return m
}

func main() {
	m := newManager()

	// 1. A snapshot is isolated from all later updates.
	snap := m.Acquire()
	m.Update(map[string]any{"host": "b"})
	v3, _ := m.Update(map[string]any{"host": "c", "port": 8080})
	got, _ := snap.Get("host")
	check(got == "a" && snap.Version() == 1, "snapshot isolated from updates, host =", got)

	// 2. Field-level source version.
	src, err := snap.SourceVersion("host")
	check(err == nil && src == 1, "field host source version =", src)

	// 3. Switching to historical content yields a larger new version.
	snap.Release()
	nv, err := m.SwitchTo(1)
	cur, _ := m.ContentAt(nv)
	check(err == nil && nv > v3 && cur["host"] == "a", "switch to v1 produced version", nv, ">", v3)

	// 4. GC refuses versions still referenced by an outstanding snapshot.
	pinned := m.Acquire()
	m.Update(map[string]any{"host": "d"})
	reclaimed := m.GC()
	blocked := true
	for _, v := range reclaimed {
		if v == nv {
			blocked = false
		}
	}
	check(blocked, "GC rejected while snapshot outstanding, reclaimed =", reclaimed)

	// 5. After release, GC reclaims and reports the versions.
	pinned.Release()
	reclaimed = m.GC()
	check(len(reclaimed) > 0, "GC after release reclaimed versions", reclaimed)

	// 6. Reading a stale snapshot (version reclaimed) fails distinctly.
	stale := m.Acquire()
	m.Update(map[string]any{"host": "e"})
	stale.Release()
	m.GC()
	_, staleErr := stale.Get("host")
	check(errors.Is(staleErr, ontology.ErrVersionReclaimed), "stale snapshot read error:", staleErr)

	// 7. Double release is harmless.
	s := m.Acquire()
	s.Release()
	s.Release()
	check(m.Leaks().Total == 0, "double release harmless, leaks =", m.Leaks().Total)

	// 8. Leak report counts outstanding snapshots per version.
	leak1 := m.Acquire()
	leak2 := m.Acquire()
	rep := m.Leaks()
	check(rep.Total == 2, "leak report outstanding =", rep.Total, "by version", rep.ByVersion)
	leak1.Release()
	leak2.Release()

	// 9-11. The three validation failure kinds; version never advances.
	before := m.CurrentVersion()
	_, err = m.Update(map[string]any{"ghost": 1})
	var ve *ontology.ValidationError
	check(errors.As(err, &ve) && ve.Kind == ontology.KindUnknownField, "unknown field rejected:", err)
	_, err = m.Update(map[string]any{"port": "8080"})
	check(errors.As(err, &ve) && ve.Kind == ontology.KindTypeMismatch, "type mismatch rejected:", err)
	_, err = m.Update(map[string]any{"port": 70000})
	ok := errors.As(err, &ve) && ve.Kind == ontology.KindOutOfRange
	check(ok && m.CurrentVersion() == before, "out of range rejected, version still", before, ":", err)

	fmt.Printf("TOTAL %d passed, %d failed\n", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
