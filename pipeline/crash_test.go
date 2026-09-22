package pipeline

import (
	"bytes"
	"crypto/sha256"
	"os"
	"os/exec"
	"testing"
)

const crashRows = 500

func buildBaseline(t *testing.T, dir string) []byte {
	t.Helper()
	p, err := Open(Config{Dir: dir, MemoryByte: 4096})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < crashRows; i++ {
		if _, err := p.Ingest(keyFixed(crashRows-1-i), []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p.OutputPath())
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCrashRecoveryByteIdentical(t *testing.T) {
	if os.Getenv("ESORT_CRASH_CHILD") == "1" {
		crashChild()
		return
	}
	baseDir := t.TempDir()
	want := buildBaseline(t, baseDir)
	sum := sha256.Sum256(want)

	for _, phase := range []string{"spill", "merge", "finalize"} {
		dir := t.TempDir()
		cmd := exec.Command(os.Args[0], "-test.run=TestCrashRecoveryByteIdentical")
		cmd.Env = append(os.Environ(),
			"ESORT_CRASH_CHILD=1", "ESORT_CRASH="+phase, "ESORT_DIR="+dir)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		err := cmd.Run()
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 77 {
			t.Fatalf("phase %s: expected crash exit 77, got %v: %s", phase, err, out.String())
		}
		// Recover in-process and verify byte identity.
		p, err := Open(Config{Dir: dir, MemoryByte: 4096})
		if err != nil {
			t.Fatalf("recover %s: %v", phase, err)
		}
		if err := p.Close(); err != nil {
			t.Fatalf("close after recover %s: %v", phase, err)
		}
		got, err := os.ReadFile(p.OutputPath())
		if err != nil {
			t.Fatal(err)
		}
		gotSum := sha256.Sum256(got)
		if gotSum != sum {
			t.Fatalf("phase %s: output differs after recovery", phase)
		}
	}
}

func crashChild() {
	dir := os.Getenv("ESORT_DIR")
	p, err := Open(Config{Dir: dir, MemoryByte: 4096, Faults: CrashPointFromEnv()})
	if err != nil {
		os.Exit(2)
	}
	for i := 0; i < crashRows; i++ {
		if _, err := p.Ingest(keyFixed(crashRows-1-i), []byte{byte(i)}); err != nil {
			os.Exit(3)
		}
	}
	if err := p.Close(); err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}
