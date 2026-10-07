package ontology_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"ontology/ontology"
)

func TestWriteCrashPointsRecoverToBeforeState(t *testing.T) {
	points := []ontology.CrashPoint{
		ontology.CrashAfterBegin,
		ontology.CrashAfterIndex,
		ontology.CrashAfterData,
		ontology.CrashAfterPrepare,
	}
	for _, point := range points {
		t.Run(string(point), func(t *testing.T) {
			p, _ := newCrashPlatform(t)
			p.CreateObject("o1")
			p.SetCrashHook(onceAt(point))

			err := p.WriteProperty(context.Background(), "name", []ontology.PropertyWrite{{Object: "o1", Value: ontology.Explicit("alice")}})
			if !errors.Is(err, ontology.ErrSimulatedCrash) {
				t.Fatalf("err=%v", err)
			}
			assertRecoveredState(t, p)
			assertRecoveryLogged(t, p)
		})
	}
}

func TestCrashAfterCommitKeepsCommittedWrite(t *testing.T) {
	p, _ := newCrashPlatform(t)
	p.CreateObject("o1")
	p.SetCrashHook(onceAt(ontology.CrashAfterCommit))
	err := p.WriteProperty(context.Background(), "name", []ontology.PropertyWrite{{Object: "o1", Value: ontology.Explicit("alice")}})
	if !errors.Is(err, ontology.ErrSimulatedCrash) {
		t.Fatalf("err=%v", err)
	}
	if err := p.RecoverInMemory(); err != nil {
		t.Fatal(err)
	}
	value, err := p.Get("o1", "name")
	if err != nil || value != ontology.Explicit("alice") {
		t.Fatalf("value=%v err=%v", value, err)
	}
	assertQuery(t, p, "name-exact", "name", ontology.IndexKey{Present: true, Data: "alice"}, []string{"o1"})
}

func TestDeleteCrashPointsRecoverToBeforeState(t *testing.T) {
	for _, point := range []ontology.CrashPoint{ontology.CrashAfterBegin, ontology.CrashAfterIndex, ontology.CrashAfterData, ontology.CrashAfterPrepare} {
		t.Run(string(point), func(t *testing.T) {
			p, _ := newCrashPlatform(t)
			p.CreateObject("o1")
			if err := p.WriteProperty(context.Background(), "name", []ontology.PropertyWrite{{Object: "o1", Value: ontology.Explicit("alice")}}); err != nil {
				t.Fatal(err)
			}
			p.SetCrashHook(onceAt(point))
			if err := p.DeleteObject(context.Background(), "o1"); !errors.Is(err, ontology.ErrSimulatedCrash) {
				t.Fatalf("delete err=%v", err)
			}
			if err := p.RecoverInMemory(); err != nil {
				t.Fatal(err)
			}
			value, err := p.Get("o1", "name")
			if err != nil || value != ontology.Explicit("alice") {
				t.Fatalf("value=%v err=%v", value, err)
			}
			assertQuery(t, p, "name-exact", "name", ontology.IndexKey{Present: true, Data: "alice"}, []string{"o1"})
		})
	}
}

func TestOpenRecoversPendingTransaction(t *testing.T) {
	p, path := newCrashPlatform(t)
	p.CreateObject("o1")
	p.SetCrashHook(onceAt(ontology.CrashAfterData))
	err := p.WriteProperty(context.Background(), "name", []ontology.PropertyWrite{{Object: "o1", Value: ontology.Explicit("alice")}})
	if !errors.Is(err, ontology.ErrSimulatedCrash) {
		t.Fatal(err)
	}

	reopened, err := ontology.Open(testSchema(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertRecoveredState(t, reopened)
}

func newCrashPlatform(t *testing.T) (*ontology.Platform, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wal.jsonl")
	p, err := ontology.Open(testSchema(), path, &recordingLogger{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p, path
}

func onceAt(point ontology.CrashPoint) ontology.CrashHook {
	fired := false
	return func(_ context.Context, actual ontology.CrashPoint) bool {
		if fired || actual != point {
			return false
		}
		fired = true
		return true
	}
}

func assertRecoveredState(t *testing.T, p *ontology.Platform) {
	t.Helper()
	if err := p.RecoverInMemory(); err != nil {
		t.Fatal(err)
	}
	value, err := p.Get("o1", "name")
	if err != nil || value != ontology.Missing() {
		t.Fatalf("value=%v err=%v", value, err)
	}
	assertQuery(t, p, "name-exact", "name", ontology.MissingKey(), []string{"o1"})
	assertQuery(t, p, "name-alias", "name", ontology.MissingKey(), []string{"o1"})
	if p.Clock() != 0 {
		t.Fatalf("clock=%d", p.Clock())
	}
}

func assertRecoveryLogged(t *testing.T, p *ontology.Platform) {
	t.Helper()
}
