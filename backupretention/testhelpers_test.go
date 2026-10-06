package backupretention

import "testing"

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error kind=%d: %v", ErrorKindOf(err), err)
	}
}

func wantErr(t *testing.T, err error, kind ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error kind=%d, got nil", kind)
	}
	if got := ErrorKindOf(err); got != kind {
		t.Fatalf("error kind = %d, want %d (%v)", got, kind, err)
	}
}

func planIndex(p *Plan) map[string]PlannedBackup {
	m := make(map[string]PlannedBackup, len(p.Retained)+len(p.Deletable))
	for _, b := range p.Retained {
		m[b.ID] = b
	}
	for _, b := range p.Deletable {
		m[b.ID] = b
	}
	return m
}

func assertKept(t *testing.T, idx map[string]PlannedBackup, id string) PlannedBackup {
	t.Helper()
	b, ok := idx[id]
	if !ok {
		t.Fatalf("backup %q missing from plan", id)
	}
	if !b.Kept {
		t.Fatalf("backup %q expected kept, got deletable", id)
	}
	return b
}

func assertDeletable(t *testing.T, idx map[string]PlannedBackup, id string) {
	t.Helper()
	b, ok := idx[id]
	if !ok {
		t.Fatalf("backup %q missing from plan", id)
	}
	if b.Kept {
		t.Fatalf("backup %q expected deletable, got kept", id)
	}
}

func hasLayer(r RetentionReason, l Layer) bool {
	for _, x := range r.DirectLayers {
		if x == l {
			return true
		}
	}
	return false
}
