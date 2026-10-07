package temporal

import "testing"

func mustCommit(t *testing.T, tx *Tx) Instant {
	t.Helper()
	at, err := tx.Commit()
	if err != nil {
		t.Fatalf("unexpected commit error: %v", err)
	}
	return at
}

func errCode(err error) TraversalErrorCode {
	if err == nil {
		return 0
	}
	if te, ok := err.(*TraversalError); ok {
		return te.Code
	}
	return -1
}
