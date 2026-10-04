package difftest

import (
	"testing"

	"ontology/erase"
	"ontology/hold"
	"ontology/restore"
)

func newStack(t *testing.T, s int, limit int64) (*erase.Ledger, *hold.Service, *restore.Service) {
	t.Helper()
	l, err := erase.New(s, limit)
	if err != nil {
		t.Fatal(err)
	}
	return l, hold.New(l), restore.New(l)
}

func mustRequest(t *testing.T, l *erase.Ledger, subject, now int64) int {
	t.Helper()
	id, err := l.Request(erase.RolePrivacy, subject, now)
	if err != nil {
		t.Fatalf("Request(sub=%d,now=%d): %v", subject, now, err)
	}
	return id
}

func mustAck(t *testing.T, l *erase.Ledger, e, s int, now int64) {
	t.Helper()
	if err := l.Ack(erase.RoleOps, e, s, now); err != nil {
		t.Fatalf("Ack(e=%d,s=%d,now=%d): %v", e, s, now, err)
	}
}

func mustBackup(t *testing.T, rs *restore.Service, s int, now int64) int {
	t.Helper()
	b, err := rs.Backup(erase.RoleOps, s, now)
	if err != nil {
		t.Fatalf("Backup(s=%d,now=%d): %v", s, now, err)
	}
	return b
}

func erasureInfo(t *testing.T, l *erase.Ledger, e int) (erase.Status, int64) {
	t.Helper()
	var st erase.Status
	var dl int64
	_ = l.View(func(c *erase.Core) error {
		er := c.Erasure(e)
		st, dl = er.Status, er.Deadline
		return nil
	})
	return st, dl
}
