package kvlog

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const crashEnv = "KVLOG_CRASH_POINT"

// TestMergeCrashPoints is driven through a child process: at the named
// crash point the merge calls os.Exit(42) simulating immediate power loss.
// Before the sealed marker exists the output must be discarded and old
// segments retained; after it exists the merge is committed and old
// segments removed.
func TestMergeCrashPoints(t *testing.T) {
	if os.Getenv(crashEnv) != "" {
		runMergeCrashChild()
		return
	}

	points := []struct {
		point     string
		committed bool
	}{
		{"merge-data-written", false},
		{"merge-data-renamed", false},
		{"merge-manifest", false},
		{"merge-hint", false},
		{"merge-sealed", true},
	}
	for _, pc := range points {
		t.Run(pc.point, func(t *testing.T) {
			dir := t.TempDir()
			setupMergeCrashStore(t, dir)
			cmd := exec.Command(os.Args[0], "-test.run=TestMergeCrashPoints")
			cmd.Env = append(os.Environ(),
				crashEnv+"="+pc.point,
				"KVLOG_CRASH_DIR="+dir)
			err := cmd.Run()
			if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 42 {
				t.Fatalf("child err=%v want exit 42", err)
			}

			e, err := Open(dir, Config{MaxSegmentBytes: 1 << 20, WriteHints: true})
			if err != nil {
				t.Fatalf("reopen after crash: %v", err)
			}
			expectGet(t, e, "a", modelPresent, "1")
			// Before sealing: old segments retained, b is still deleted.
			// After sealing: merge committed; its tombstone was legally
			// dropped (no older record anywhere), so b is now missing.
			if pc.committed {
				expectGet(t, e, "b", modelMissing, "")
			} else {
				expectGet(t, e, "b", modelDeleted, "")
			}
			expectGet(t, e, "c", modelPresent, "three")
			states, _ := listSegments(dir)
			nMergeOutputs := 0
			for _, st := range states {
				if len(st.replaces) > 0 {
					nMergeOutputs++
					if !pc.committed {
						t.Fatalf("uncommitted output seg=%d survived", st.id)
					}
				}
			}
			if pc.committed && nMergeOutputs == 0 {
				t.Fatal("committed merge output missing after reopen")
			}
			if pc.committed {
				for _, st := range states {
					for _, rep := range st.replaces {
						for _, x := range states {
							if x.id == rep {
								t.Fatalf("replaced segment %d still present", rep)
							}
						}
					}
				}
			}
			e.Close()

			// Determinism: reopen again and compare.
			e2, err := Open(dir, Config{MaxSegmentBytes: 1 << 20, WriteHints: true})
			if err != nil {
				t.Fatal(err)
			}
			s1 := dumpEngine(e)
			s2 := dumpEngine(e2)
			if !snapshotsEqual(s1, s2) {
				t.Fatal("nondeterministic reopen")
			}
			e2.Close()
		})
	}
}

func setupMergeCrashStore(t *testing.T, dir string) {
	t.Helper()
	e, err := Open(dir, Config{MaxSegmentBytes: 1 << 20, WriteHints: true})
	if err != nil {
		t.Fatal(err)
	}
	e.Put([]byte("a"), []byte("1"))
	e.Put([]byte("b"), []byte("two"))
	e.Put([]byte("c"), []byte("three"))
	e.Delete([]byte("b"))
	if err := e.forceSeal(); err != nil {
		t.Fatal(err)
	}
	ids := sealedIDs(t, dir)
	if len(ids) != 1 {
		t.Fatalf("setup sealed=%v", ids)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
}

func runMergeCrashChild() {
	point := os.Getenv(crashEnv)
	dir := os.Getenv("KVLOG_CRASH_DIR")
	e, err := Open(dir, Config{MaxSegmentBytes: 1 << 20, WriteHints: true})
	if err != nil {
		os.Exit(1)
	}
	e.SetCrashHook(func(p string) bool { return p == point })
	ids := []int{}
	entries, _ := os.ReadDir(dir)
	for _, ent := range entries {
		if id, ok := parseSegID(ent.Name(), ".sealed"); ok {
			ids = append(ids, id)
		}
	}
	if _, err := e.Merge(ids); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

// TestRolloverCrashBeforeSeal crashes between hint write and seal marker
// during active-segment rollover: the old segment stays active (no sealed
// marker), its hint is orphaned and ignored, and recovery truncates
// nothing.
func TestRolloverCrashBeforeSeal(t *testing.T) {
	if os.Getenv(crashEnv) == "rollover-child" {
		dir := os.Getenv("KVLOG_CRASH_DIR")
		e, _ := Open(dir, Config{MaxSegmentBytes: 1 << 20, WriteHints: true})
		e.SetCrashHook(func(p string) bool { return p == "seal-hint:rollover" })
		e.Put([]byte("x"), []byte("y"))
		e.forceSeal()
		os.Exit(0)
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=TestRolloverCrashBeforeSeal")
	cmd.Env = append(os.Environ(), crashEnv+"=rollover-child",
		"KVLOG_CRASH_DIR="+dir)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	e, err := Open(dir, Config{MaxSegmentBytes: 1 << 20, WriteHints: true})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	expectGet(t, e, "x", modelPresent, "y")
	// Orphaned hint for unsealed segment must be present but unused; no
	// crash and no torn bytes.
	if e.TornBytes() != 0 {
		t.Fatalf("torn=%d", e.TornBytes())
	}
	if _, err := os.Stat(filepath.Join(dir, segHintName(1))); err != nil {
		t.Fatal("orphan hint removed unexpectedly")
	}
}
