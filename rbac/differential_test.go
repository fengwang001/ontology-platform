package rbac

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// testOp is one randomized operation invocation.
type testOp struct {
	name string
	a, b string
	rs   []string
	n    int
}

func (op testOp) String() string {
	if op.rs != nil {
		return fmt.Sprintf("%s(%q, %v, %d)", op.name, op.a, op.rs, op.n)
	}
	if op.b != "" || op.name == "AddInherit" || op.name == "DeleteInherit" ||
		op.name == "AssignUser" || op.name == "DeassignUser" ||
		op.name == "CreateSession" || op.name == "Activate" ||
		op.name == "Deactivate" || op.name == "AssignPerm" || op.name == "Check" {
		return fmt.Sprintf("%s(%q, %q)", op.name, op.a, op.b)
	}
	return fmt.Sprintf("%s(%q)", op.name, op.a)
}

var (
	poolRoles = []string{"r0", "r1", "r2", "r3", "r4", "r5"}
	poolUsers = []string{"u0", "u1", "u2", "u3"}
	poolSids  = []string{"s0", "s1", "s2", "s3"}
	poolPerms = []string{"p0", "p1", "p2"}
	poolCons  = []string{"c0", "c1", "c2", "c3"}
)

// pick chooses from the pool, with a small chance of an empty or unknown
// name to exercise InvalidParam and NotExist paths.
func pick(rnd *rand.Rand, pool []string) string {
	switch rnd.Intn(40) {
	case 0:
		return ""
	case 1:
		return "ghost"
	}
	return pool[rnd.Intn(len(pool))]
}

func randomRS(rnd *rand.Rand) ([]string, int) {
	k := 2 + rnd.Intn(2) // 2 or 3 entries
	rs := make([]string, 0, k)
	for i := 0; i < k; i++ {
		if rnd.Intn(25) == 0 && len(rs) > 0 {
			rs = append(rs, rs[0]) // occasional duplicate
			continue
		}
		rs = append(rs, pick(rnd, poolRoles))
	}
	n := 2
	if len(rs) > 2 && rnd.Intn(2) == 0 {
		n = 3
	}
	switch rnd.Intn(20) {
	case 0:
		n = 1 // out of range low
	case 1:
		n = len(rs) + 1 // out of range high
	}
	return rs, n
}

func randomOp(rnd *rand.Rand) testOp {
	weighted := []string{
		"AddRole", "AddRole",
		"AddUser",
		"AddInherit", "AddInherit", "AddInherit",
		"DeleteInherit", "DeleteInherit",
		"AssignUser", "AssignUser", "AssignUser",
		"DeassignUser", "DeassignUser",
		"AddSSD", "AddDSD",
		"CreateSession", "CreateSession",
		"DeleteSession",
		"Activate", "Activate", "Activate",
		"Deactivate", "Deactivate",
		"AssignPerm",
		"Check",
	}
	switch name := weighted[rnd.Intn(len(weighted))]; name {
	case "AddRole":
		return testOp{name: name, a: pick(rnd, poolRoles)}
	case "AddUser":
		return testOp{name: name, a: pick(rnd, poolUsers)}
	case "AddInherit", "DeleteInherit":
		return testOp{name: name, a: pick(rnd, poolRoles), b: pick(rnd, poolRoles)}
	case "AssignUser", "DeassignUser":
		return testOp{name: name, a: pick(rnd, poolUsers), b: pick(rnd, poolRoles)}
	case "AddSSD", "AddDSD":
		rs, n := randomRS(rnd)
		return testOp{name: name, a: pick(rnd, poolCons), rs: rs, n: n}
	case "CreateSession":
		return testOp{name: name, a: pick(rnd, poolSids), b: pick(rnd, poolUsers)}
	case "DeleteSession":
		return testOp{name: name, a: pick(rnd, poolSids)}
	case "Activate", "Deactivate":
		return testOp{name: name, a: pick(rnd, poolSids), b: pick(rnd, poolRoles)}
	case "AssignPerm":
		return testOp{name: name, a: pick(rnd, poolRoles), b: pick(rnd, poolPerms)}
	case "Check":
		return testOp{name: name, a: pick(rnd, poolSids), b: pick(rnd, poolPerms)}
	}
	panic("unreachable")
}

func errString(err error) string {
	if err == nil {
		return "OK"
	}
	e := err.(*Error)
	return fmt.Sprintf("ERR|%s|%s|%s|%s|%s|%s",
		e.Cat, e.Kind, e.Object, e.Constraint, e.Subject, strings.Join(e.Impliers, ","))
}

func pairsString(ps []Pair) string {
	parts := make([]string, 0, len(ps))
	for _, p := range ps {
		parts = append(parts, p.Session+"/"+p.Role)
	}
	return "PAIRS|" + strings.Join(parts, ",")
}

// applyRBAC runs one operation against the optimized implementation and
// returns a canonical result string.
func applyRBAC(r *RBAC, op testOp) string {
	switch op.name {
	case "AddRole":
		return errString(r.AddRole(op.a))
	case "AddUser":
		return errString(r.AddUser(op.a))
	case "AddInherit":
		return errString(r.AddInherit(op.a, op.b))
	case "DeleteInherit":
		ps, err := r.DeleteInherit(op.a, op.b)
		if err != nil {
			return errString(err)
		}
		return pairsString(ps)
	case "AssignUser":
		return errString(r.AssignUser(op.a, op.b))
	case "DeassignUser":
		ps, err := r.DeassignUser(op.a, op.b)
		if err != nil {
			return errString(err)
		}
		return pairsString(ps)
	case "AddSSD":
		return errString(r.AddSSD(op.a, op.rs, op.n))
	case "AddDSD":
		return errString(r.AddDSD(op.a, op.rs, op.n))
	case "CreateSession":
		return errString(r.CreateSession(op.a, op.b))
	case "DeleteSession":
		return errString(r.DeleteSession(op.a))
	case "Activate":
		return errString(r.Activate(op.a, op.b))
	case "Deactivate":
		return errString(r.Deactivate(op.a, op.b))
	case "AssignPerm":
		return errString(r.AssignPerm(op.a, op.b))
	case "Check":
		return fmt.Sprintf("%v", r.Check(op.a, op.b))
	}
	panic("unknown op " + op.name)
}

// apply runs one operation against the naive implementation.
func (n *naive) apply(op testOp) string {
	switch op.name {
	case "AddRole":
		return errString(n.AddRole(op.a))
	case "AddUser":
		return errString(n.AddUser(op.a))
	case "AddInherit":
		return errString(n.AddInherit(op.a, op.b))
	case "DeleteInherit":
		ps, err := n.DeleteInherit(op.a, op.b)
		if err != nil {
			return errString(err)
		}
		return pairsString(ps)
	case "AssignUser":
		return errString(n.AssignUser(op.a, op.b))
	case "DeassignUser":
		ps, err := n.DeassignUser(op.a, op.b)
		if err != nil {
			return errString(err)
		}
		return pairsString(ps)
	case "AddSSD":
		return errString(n.AddSSD(op.a, op.rs, op.n))
	case "AddDSD":
		return errString(n.AddDSD(op.a, op.rs, op.n))
	case "CreateSession":
		return errString(n.CreateSession(op.a, op.b))
	case "DeleteSession":
		return errString(n.DeleteSession(op.a))
	case "Activate":
		return errString(n.Activate(op.a, op.b))
	case "Deactivate":
		return errString(n.Deactivate(op.a, op.b))
	case "AssignPerm":
		return errString(n.AssignPerm(op.a, op.b))
	case "Check":
		return fmt.Sprintf("%v", n.Check(op.a, op.b))
	}
	panic("unknown op " + op.name)
}

// digest renders the naive state in exactly the same format as digest(r).
func (n *naive) digest() string {
	var b strings.Builder
	var roles []string
	for role := range n.roles {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		fmt.Fprintf(&b, "role %s perms=%v\n", role, sortedOf(n.roles[role]))
	}
	var seniors []string
	for s := range n.edges {
		seniors = append(seniors, s)
	}
	sort.Strings(seniors)
	for _, s := range seniors {
		fmt.Fprintf(&b, "edge %s -> %v\n", s, sortedOf(n.edges[s]))
	}
	var users []string
	for u := range n.users {
		users = append(users, u)
	}
	sort.Strings(users)
	for _, u := range users {
		var sids []string
		for sid, s := range n.sessions {
			if s.owner == u {
				sids = append(sids, sid)
			}
		}
		sort.Strings(sids)
		fmt.Fprintf(&b, "user %s assigned=%v auth=%v sessions=%v\n",
			u, sortedOf(n.users[u]), sortedOf(n.auth(u)), sids)
	}
	var sids []string
	for s := range n.sessions {
		sids = append(sids, s)
	}
	sort.Strings(sids)
	for _, s := range sids {
		rec := n.sessions[s]
		fmt.Fprintf(&b, "session %s owner=%s active=%v eff=%v\n",
			s, rec.owner, sortedOf(rec.active), sortedOf(n.eff(s)))
	}
	var cs []string
	for c := range n.ssd {
		cs = append(cs, c)
	}
	sort.Strings(cs)
	for _, c := range cs {
		fmt.Fprintf(&b, "ssd %s rs=%v n=%d\n", c, sortedOf(n.ssd[c].rs), n.ssd[c].n)
	}
	cs = cs[:0]
	for c := range n.dsd {
		cs = append(cs, c)
	}
	sort.Strings(cs)
	for _, c := range cs {
		fmt.Fprintf(&b, "dsd %s rs=%v n=%d\n", c, sortedOf(n.dsd[c].rs), n.dsd[c].n)
	}
	return b.String()
}

// TestDifferentialRandom replays 2000 random operation sequences against
// both the optimized RBAC and the naive from-scratch simulator, comparing
// every result, the check counters and the full state after every step.
// Each step is logged with input, output and the deciding reason.
func TestDifferentialRandom(t *testing.T) {
	const sequences = 2000
	const opsPerSeq = 50
	for seq := 0; seq < sequences; seq++ {
		rnd := rand.New(rand.NewSource(int64(seq)))
		r := newRBAC(t, 3, 6)
		n := newNaive(3, 6)
		for i := 0; i < opsPerSeq; i++ {
			op := randomOp(rnd)
			gotR := applyRBAC(r, op)
			gotN := n.apply(op)
			// Log input, output and the deciding reason (error
			// category/kind/constraint encoded in the result).
			t.Logf("seq=%d op=%d in=%s out=%s", seq, i, op, gotR)
			if gotR != gotN {
				t.Fatalf("seq=%d op=%d %s: rbac=%s naive=%s", seq, i, op, gotR, gotN)
			}
			if op.name == "AddInherit" || op.name == "DeleteInherit" {
				cu, cs := r.LastCheckCounts()
				if cu != n.lastUsers || cs != n.lastSessions {
					t.Fatalf("seq=%d op=%d %s: counts=(%d,%d), naive=(%d,%d)",
						seq, i, op, cu, cs, n.lastUsers, n.lastSessions)
				}
			}
			if dr, dn := digest(r), n.digest(); dr != dn {
				t.Fatalf("seq=%d op=%d %s: state divergence\nrbac:\n%s\nnaive:\n%s",
					seq, i, op, dr, dn)
			}
		}
		checkInvariants(t, r)
	}
}
