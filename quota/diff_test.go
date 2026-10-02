package quota

import (
	"fmt"
	"math/rand"
	"testing"
)

const diffSequences = 2000

type snapshot struct {
	usage  int64
	soft   int64
	hard   int64
	grace  int64 // -1 unset
	member int   // users only
}

func takeSnapshot(m *Manager) (map[int]snapshot, map[int]snapshot) {
	us := make(map[int]snapshot, len(m.users))
	gs := make(map[int]snapshot, len(m.groups))
	for id, e := range m.users {
		g := int64(-1)
		if e.graceAt >= 0 {
			g = e.graceAt
		}
		us[id] = snapshot{usage: e.usage, soft: e.soft, hard: e.hard,
			grace: g, member: e.g}
	}
	for id, e := range m.groups {
		g := int64(-1)
		if e.graceAt >= 0 {
			g = e.graceAt
		}
		gs[id] = snapshot{usage: e.usage, soft: e.soft, hard: e.hard, grace: g}
	}
	return us, gs
}

func takeNaive(s *naiveSim) (map[int]snapshot, map[int]snapshot) {
	us := make(map[int]snapshot, len(s.users))
	gs := make(map[int]snapshot, len(s.groups))
	for id, u := range s.users {
		us[id] = snapshot{usage: u.ent.usage, soft: u.ent.soft,
			hard: u.ent.hard, grace: u.ent.grace, member: u.group}
	}
	for id, g := range s.groups {
		gs[id] = snapshot{usage: g.usage, soft: g.soft, hard: g.hard,
			grace: g.grace}
	}
	return us, gs
}

func sameSnap(a, b map[int]snapshot) (string, bool) {
	if len(a) != len(b) {
		return fmt.Sprintf("entity set size %d != %d", len(a), len(b)), false
	}
	for id, x := range a {
		y, ok := b[id]
		if !ok {
			return fmt.Sprintf("entity %d missing on one side", id), false
		}
		if x != y {
			return fmt.Sprintf("entity %d: real=%+v naive=%+v", id, x, y), false
		}
	}
	return "", true
}

func checkInvariant(t *testing.T, m *Manager, stage string) {
	t.Helper()
	sums := make(map[int]int64)
	for id, e := range m.users {
		if e.usage < 0 {
			t.Fatalf("%s: user %d negative usage %d", stage, id, e.usage)
		}
		if e.usage <= e.soft && e.graceAt >= 0 {
			t.Fatalf("%s: user %d at/below soft but grace set %d",
				stage, id, e.graceAt)
		}
		if e.usage > e.soft && e.graceAt < 0 {
			t.Fatalf("%s: user %d over soft but grace unset", stage, id)
		}
		if e.graceAt > m.lastNow && m.lastNow >= 0 {
			t.Fatalf("%s: user %d grace %d after lastNow %d",
				stage, id, e.graceAt, m.lastNow)
		}
		sums[e.g] += e.usage
	}
	for id, e := range m.groups {
		if sums[id] != e.usage {
			t.Fatalf("%s: group %d usage %d != user sum %d",
				stage, id, e.usage, sums[id])
		}
		if e.usage <= e.soft && e.graceAt >= 0 {
			t.Fatalf("%s: group %d at/below soft but grace set %d",
				stage, id, e.graceAt)
		}
		if e.usage > e.soft && e.graceAt < 0 {
			t.Fatalf("%s: group %d over soft but grace unset", stage, id)
		}
		if e.graceAt > m.lastNow && m.lastNow >= 0 {
			t.Fatalf("%s: group %d grace %d after lastNow %d",
				stage, id, e.graceAt, m.lastNow)
		}
	}
}

func reasonName(r RejectReason) string {
	switch r {
	case -1:
		return "ACCEPTED"
	case ReasonInvalidArgument:
		return "INVALID_ARGUMENT"
	case ReasonClockRollback:
		return "CLOCK_ROLLBACK"
	case ReasonNotFound:
		return "NOT_FOUND"
	case ReasonIllegalState:
		return "ILLEGAL_STATE"
	case ReasonUserHardLimit:
		return "USER_HARD_LIMIT"
	case ReasonUserGraceExpired:
		return "USER_GRACE_EXPIRED"
	case ReasonGroupHardLimit:
		return "GROUP_HARD_LIMIT"
	case ReasonGroupGraceExpired:
		return "GROUP_GRACE_EXPIRED"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", r)
	}
}

// TestRandomDifferential replays 2000 randomly generated operation
// sequences against both Manager and the naive simulator, comparing the
// rejection reason and every entity's usage/limits/grace after each step.
// Each step logs the input, both outputs and the judged reason.
func TestRandomDifferential(t *testing.T) {
	for seq := 0; seq < diffSequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*1_000_003 + 7))
		gu := int64(1 + rng.Intn(10))
		gg := int64(1 + rng.Intn(20))
		m, err := New(gu, gg)
		if err != nil {
			t.Fatalf("seq %d: New: %v", seq, err)
		}
		sim := newNaive(gu, gg)

		steps := 30 + rng.Intn(60)
		log := []string{
			fmt.Sprintf("seq=%d Gu=%d Gg=%d steps=%d", seq, gu, gg, steps),
		}

		ids := []int{0, 1, 2, 3, 4}

		for step := 0; step < steps; step++ {
			var (
				input    string
				got      RejectReason
				want     RejectReason
				basis    string
				badParam bool
			)

			op := rng.Intn(10)
			switch {
			case op < 2: // AddGroup
				g, soft, hard := genReg(rng, ids)
				if rng.Intn(10) == 0 {
					g, soft, hard, badParam = genBadReg(rng, ids)
				}
				input = fmt.Sprintf("AddGroup(g=%d soft=%d hard=%d)", g, soft, hard)
				got = reasonOf(m.AddGroup(g, soft, hard))
				want = sim.addGroup(g, soft, hard)
				basis = "invalid args before existence; duplicate = ILLEGAL_STATE"

			case op < 4: // AddUser
				u, g, soft, hard := genAddUser(rng, ids)
				if rng.Intn(10) == 0 {
					u, g, soft, hard, badParam = genBadAddUser(rng, ids)
				}
				input = fmt.Sprintf("AddUser(u=%d g=%d soft=%d hard=%d)", u, g, soft, hard)
				got = reasonOf(m.AddUser(u, g, soft, hard))
				want = sim.addUser(u, g, soft, hard)
				basis = "duplicate user = ILLEGAL_STATE; missing group = NOT_FOUND"

			case op < 7: // Alloc / Free
				u := ids[rng.Intn(len(ids))]
				x := int64(1 + rng.Intn(40))
				now := genNow(rng, sim, rng.Intn(8) == 0)
				if rng.Intn(2) == 0 {
					input = fmt.Sprintf("Alloc(u=%d x=%d now=%d)", u, x, now)
					got = reasonOf(m.Alloc(u, x, now))
					want = sim.alloc(u, x, now)
					basis = "user hard -> user grace -> group hard -> group grace"
				} else {
					input = fmt.Sprintf("Free(u=%d x=%d now=%d)", u, x, now)
					got = reasonOf(m.Free(u, x, now))
					want = sim.free(u, x, now)
					basis = "over-free = ILLEGAL_STATE; free ignores limits/grace"
				}

			case op < 9: // SetLimits
				kind := Kind(rng.Intn(2))
				id := ids[rng.Intn(len(ids))]
				soft := int64(rng.Intn(60))
				hard := soft + int64(rng.Intn(60))
				now := genNow(rng, sim, rng.Intn(8) == 0)
				if rng.Intn(10) == 0 {
					hard = soft - 1
					badParam = true
				}
				input = fmt.Sprintf("SetLimits(kind=%s id=%d soft=%d hard=%d now=%d)",
					kindName(kind), id, soft, hard, now)
				got = reasonOf(m.SetLimits(kind, id, soft, hard, now))
				want = sim.setLimits(kind, id, soft, hard, now)
				basis = "tidy once with new limits at now"

			default: // Move
				u := ids[rng.Intn(len(ids))]
				g2 := ids[rng.Intn(len(ids))]
				now := genNow(rng, sim, rng.Intn(8) == 0)
				input = fmt.Sprintf("Move(u=%d g2=%d now=%d)", u, g2, now)
				got = reasonOf(m.Move(u, g2, now))
				want = sim.move(u, g2, now)
				basis = "target hard; target grace only when U>0; same group = ILLEGAL_STATE"
			}

			verdict := "MATCH"
			if got != want {
				verdict = "MISMATCH"
			}
			_ = badParam
			log = append(log, fmt.Sprintf("  step %2d: %-52s real=%-20s naive=%-20s %s | %s",
				step, input, reasonName(got), reasonName(want), verdict, basis))

			if got != want {
				t.Fatalf("seq %d step %d %s: real %s, naive %s\n%s",
					seq, step, input, reasonName(got), reasonName(want),
					joinLog(log))
			}

			ru, rg := takeSnapshot(m)
			nu, ng := takeNaive(sim)
			if msg, ok := sameSnap(ru, nu); !ok {
				t.Fatalf("seq %d step %d user snapshots differ: %s\n%s",
					seq, step, msg, joinLog(log))
			}
			if msg, ok := sameSnap(rg, ng); !ok {
				t.Fatalf("seq %d step %d group snapshots differ: %s\n%s",
					seq, step, msg, joinLog(log))
			}
			checkInvariant(t, m, fmt.Sprintf("seq %d step %d", seq, step))
		}

		// Log the full replay for reproducibility of usage/timers.
		if testing.Verbose() {
			t.Log("\n" + joinLog(log))
		}
	}
}

func joinLog(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}

func genReg(rng *rand.Rand, ids []int) (int, int64, int64) {
	g := ids[rng.Intn(len(ids))]
	soft := int64(rng.Intn(60))
	hard := soft + int64(rng.Intn(60))
	return g, soft, hard
}

func genBadReg(rng *rand.Rand, ids []int) (int, int64, int64, bool) {
	g := ids[rng.Intn(len(ids))]
	switch rng.Intn(3) {
	case 0:
		g = -1
	case 1:
		g = 1_000_001
	default:
		return g, 50, 40, true
	}
	return g, 0, 0, true
}

func genAddUser(rng *rand.Rand, ids []int) (int, int, int64, int64) {
	u := ids[rng.Intn(len(ids))]
	g := ids[rng.Intn(len(ids))]
	soft := int64(rng.Intn(60))
	hard := soft + int64(rng.Intn(60))
	return u, g, soft, hard
}

func genBadAddUser(rng *rand.Rand, ids []int) (int, int, int64, int64, bool) {
	u := ids[rng.Intn(len(ids))]
	g := ids[rng.Intn(len(ids))]
	switch rng.Intn(4) {
	case 0:
		u = -7
	case 1:
		g = 1_000_001
	case 2:
		return u, g, 30, 10, true
	default:
		return u, g, -1, 5, true
	}
	return u, g, 0, 0, true
}

func genNow(rng *rand.Rand, sim *naiveSim, rollback bool) int64 {
	if rollback && sim.last > 0 {
		delta := int64(1 + rng.Intn(5))
		if delta > sim.last {
			delta = sim.last
		}
		return sim.last - delta
	}
	base := sim.last
	if base < 0 {
		base = 0
	}
	return base + int64(rng.Intn(6))
}
