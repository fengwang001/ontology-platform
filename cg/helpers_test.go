package cg

import "testing"

func kindOf(err error) Kind {
	if err == nil {
		return OK
	}
	if e, ok := err.(*Error); ok {
		return e.Kind
	}
	return -1
}

func newTestCoordinator(t *testing.T, hold int64, defCap int) *Coordinator {
	t.Helper()
	return New(Config{DefaultQueueCapacity: defCap, MaxFreezeHold: hold})
}

func mustCreate(t *testing.T, c *Coordinator, at int64, gid string, vols ...string) {
	t.Helper()
	if err := c.CreateGroup(at, GroupSpec{GroupID: gid, VolumeIDs: vols}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
}
