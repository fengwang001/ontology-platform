package ontology

import "testing"

func TestSkeleton(t *testing.T) {
	if _, err := NewSnapshotCommitManager(1, 1); err != nil {
		t.Fatal(err)
	}
}
