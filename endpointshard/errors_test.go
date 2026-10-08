package endpointshard

import (
	"testing"
)

func assertErrKind(t *testing.T, op string, err error, want ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s succeeded, want error kind %v", op, want)
	}
	kind, ok := KindOf(err)
	if !ok {
		t.Fatalf("%s returned non-package error: %v", op, err)
	}
	t.Logf("输入: %s 实际输出: %v 判定依据: kind(%v) == %v", op, err, kind, want)
	if kind != want {
		t.Fatalf("%s kind = %v, want %v", op, kind, want)
	}
}

// Error kinds are distinguishable and follow the priority
// invalid argument > service not found > service already exists.
func TestErrorPriority(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "s", 2)
	// Invalid argument beats not-found.
	_, err := m.Sync("missing", []Endpoint{live("a", "r"), live("a", "r")})
	assertErrKind(t, "Sync(missing, duplicate IDs)", err, ErrInvalidArgument)
	_, err = m.Sync("missing", []Endpoint{ep("a", "", true, false)})
	assertErrKind(t, "Sync(missing, empty region)", err, ErrInvalidArgument)
	_, err = m.Sync("missing", []Endpoint{ep("", "r", true, false)})
	assertErrKind(t, "Sync(missing, empty ID)", err, ErrInvalidArgument)
	_, err = m.Resize("missing", 0)
	assertErrKind(t, "Resize(missing, 0)", err, ErrInvalidArgument)
	// Invalid argument beats already-exists.
	assertErrKind(t, "CreateService(s, 0)", m.CreateService("s", 0), ErrInvalidArgument)
	assertErrKind(t, "CreateService(s, -3)", m.CreateService("s", -3), ErrInvalidArgument)
	// Plain not-found and already-exists.
	_, err = m.Sync("missing", []Endpoint{live("a", "r")})
	assertErrKind(t, "Sync(missing, valid)", err, ErrServiceNotFound)
	_, err = m.Query("missing", "r")
	assertErrKind(t, "Query(missing)", err, ErrServiceNotFound)
	_, err = m.Resize("missing", 3)
	assertErrKind(t, "Resize(missing, 3)", err, ErrServiceNotFound)
	assertErrKind(t, "DeleteService(missing)", m.DeleteService("missing"), ErrServiceNotFound)
	assertErrKind(t, "CreateService(s, 3)", m.CreateService("s", 3), ErrServiceAlreadyExists)
	// Empty service name is an invalid argument everywhere.
	assertErrKind(t, "CreateService('', 3)", m.CreateService("", 3), ErrInvalidArgument)
	assertErrKind(t, "DeleteService('')", m.DeleteService(""), ErrInvalidArgument)
}

// A rejected operation changes no state at all, including shard
// numbering and generations.
func TestRejectedOpsLeaveNoState(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "s", 2)
	mustSync(t, m, "s", []Endpoint{live("a", "r"), live("b", "r")})
	beforeShards := shardMap(t, m, "s")
	beforeGens := gens(t, m, "s")
	// Rejected syncs.
	if _, err := m.Sync("s", []Endpoint{live("x", "r"), live("x", "r")}); err == nil {
		t.Fatal("duplicate-ID sync succeeded")
	}
	if _, err := m.Sync("s", []Endpoint{live("x", "")}); err == nil {
		t.Fatal("empty-region sync succeeded")
	}
	// Rejected resize.
	if _, err := m.Resize("s", -1); err == nil {
		t.Fatal("negative resize succeeded")
	}
	assertShards(t, m, "s", beforeShards)
	assertGens(t, m, "s", beforeGens)
	// Shard numbering must be unaffected: the next created shard is
	// number 2, proving the rejected calls allocated nothing.
	rep := mustSync(t, m, "s", []Endpoint{live("a", "r"), live("b", "r"), live("c", "r")})
	t.Logf("实际输出: report=%v layout=%s", rep.Changed(), fmtShardMap(shardMap(t, m, "s")))
	assertShards(t, m, "s", map[int][]string{1: {"a", "b"}, 2: {"c"}})
	t.Logf("判定依据: 被拒绝的操作未消耗分片编号、未改变代次")
	// Delete then recreate: the service is gone in between.
	if err := m.DeleteService("s"); err != nil {
		t.Fatal(err)
	}
	_, err := m.Sync("s", []Endpoint{live("a", "r")})
	assertErrKind(t, "Sync after delete", err, ErrServiceNotFound)
	if err := m.CreateService("s", 1); err != nil {
		t.Fatalf("recreate after delete failed: %v", err)
	}
}
