package main

import (
	"errors"
	"os"
	"os/exec"
)

// runCrashChild executes the demo binary in crash-harness mode. The
// child exits 77 at the requested phase boundary; that is expected.
func runCrashChild(dir, phase string) {
	cmd := exec.Command(os.Args[0], "-crash-child")
	cmd.Env = append(os.Environ(), "ESORT_CHILD=1")
	// Re-exec self with mode flags via env; child branch above is used
	// when ESORT_CHILD is set, so pass dir/phase through temp env vars.
	cmd.Env = append(cmd.Env, "ESORT_CHILD_DIR="+dir, "ESORT_CHILD_PHASE="+phase)
	err := cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 77 {
		must(err)
	}
}
