package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func randomOp(r *rand.Rand, seq int) testOp {
	role := func() string { return fmt.Sprintf("r%d", r.Intn(7)) }
	user := func() string { return fmt.Sprintf("u%d", r.Intn(5)) }
	session := func() string { return fmt.Sprintf("s%d", r.Intn(8)) }
	perm := func() string { return fmt.Sprintf("p%d", r.Intn(7)) }
	name := func(prefix string) string { return fmt.Sprintf("%s%d", prefix, r.Intn(5)) }
	switch r.Intn(16) {
	case 0:
		return testOp{name: "AddRole", a: role()}
	case 1:
		return testOp{name: "AddUser", a: user()}
	case 2:
		return testOp{name: "AddInherit", a: role(), b: role()}
	case 3:
		return testOp{name: "DeleteInherit", a: role(), b: role()}
	case 4:
		return testOp{name: "AssignUser", a: user(), b: role()}
	case 5:
		return testOp{name: "DeassignUser", a: user(), b: role()}
	case 6:
		roles := []string{role(), role()}
		for r.Intn(3) == 0 {
			roles = append(roles, role())
		}
		return testOp{name: "AddSSD", a: name("ssd"), roles: roles, n: 2 + r.Intn(len(roles)-1)}
	case 7:
		roles := []string{role(), role()}
		for r.Intn(3) == 0 {
			roles = append(roles, role())
		}
		return testOp{name: "AddDSD", a: name("dsd"), roles: roles, n: 2 + r.Intn(len(roles)-1)}
	case 8:
		return testOp{name: "CreateSession", a: session(), b: user()}
	case 9:
		return testOp{name: "DeleteSession", a: session()}
	case 10:
		return testOp{name: "Activate", a: session(), b: role()}
	case 11:
		return testOp{name: "Deactivate", a: session(), b: role()}
	case 12:
		return testOp{name: "AssignPerm", a: role(), b: perm()}
	default:
		return testOp{name: "Check", a: session(), b: perm()}
	}
}

func describeOp(op testOp) string {
	if len(op.roles) > 0 {
		return fmt.Sprintf("%s(%q,%v,n=%d)", op.name, op.a, op.roles, op.n)
	}
	if op.b != "" {
		return fmt.Sprintf("%s(%q,%q)", op.name, op.a, op.b)
	}
	return fmt.Sprintf("%s(%q)", op.name, op.a)
}

func describeResult(r opResult) string {
	if r.err != "" {
		return fmt.Sprintf("reject:%s:%s removed=%v", r.kind, r.err, r.removed)
	}
	if reflect.TypeOf(r.removed) != nil {
		return fmt.Sprintf("ok removed=%v check=%v", r.removed, r.ok)
	}
	return fmt.Sprintf("ok check=%v", r.ok)
}

func compareAuthEff(t *testing.T, m *Manager, n *naiveManager) string {
	for user := range n.users {
		want := n.auth(user)
		got := m.authForTest(user)
		if !reflect.DeepEqual(got, want) {
			return fmt.Sprintf("Auth(%s) = %v, want %v", user, got, want)
		}
	}
	for sid, sess := range n.sessions {
		want := n.eff(sess)
		got := m.effectiveForTest(sid)
		if !reflect.DeepEqual(got, want) {
			return fmt.Sprintf("Eff(%s) = %v, want %v", sid, got, want)
		}
	}
	return ""
}

func TestRandomDifferential2000(t *testing.T) {
	total := 0
	for iteration := 0; iteration < 2000; iteration++ {
		r := rand.New(rand.NewSource(int64(1000 + iteration)))
		real := newManager(3, 8)
		naive := newNaive(3, 8)
		ops := make([]testOp, 30+r.Intn(50))
		for i := range ops {
			ops[i] = randomOp(r, iteration)
		}
		var log []string
		log = append(log, fmt.Sprintf("sequence %d seed=%d operations=%d", iteration, 1000+iteration, len(ops)))
		for step, op := range ops {
			total++
			want := executeNaive(naive, op)
			got := executeReal(real, op)
			log = append(log, fmt.Sprintf("input=%d/%d %s output_real=%s output_naive=%s basis=recompute Auth/Eff and scan all current SSD/DSD rules",
				step+1, len(ops), describeOp(op), describeResult(got), describeResult(want)))
			if got.kind != want.kind || removedList(got.removed) != removedList(want.removed) || got.ok != want.ok {
				for _, line := range log {
					t.Log(line)
				}
				t.Fatalf("iteration=%d step=%d op=%s got=%s want=%s", iteration, step, describeOp(op), describeResult(got), describeResult(want))
			}
			rs, ns := realSnapshot(real), naiveSnapshot(naive)
			if !reflect.DeepEqual(rs, ns) {
				for _, line := range log {
					t.Log(line)
				}
				t.Fatalf("iteration=%d step=%d state mismatch after %s\ngot=%#v\nwant=%#v", iteration, step, describeOp(op), rs, ns)
			}
			if diff := compareAuthEff(t, real, naive); diff != "" {
				for _, line := range log {
					t.Log(line)
				}
				t.Fatalf("iteration=%d step=%d after %s %s", iteration, step, describeOp(op), diff)
			}
		}
		if testing.Verbose() {
			for _, line := range log {
				t.Log(line)
			}
		}
	}
	t.Logf("compared %d random operations", total)
}

func removedList(items []InactiveAssignment) string {
	return fmt.Sprint(items)
}
