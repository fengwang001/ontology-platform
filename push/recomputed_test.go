package push

import (
	"testing"

	"ontology/group"
)

// TestRecomputedScaling 证明重算量只取决于分组成员数而非设备总数：
// 100 与 10000 两档、目标组成员同为 5 时，SetPolicy/SetPriority 的 recomputed 恒为 5；
// 成员变更恒为 1；对 * 设策略则为全部设备数。
func TestRecomputedScaling(t *testing.T) {
	for _, total := range []int{100, 10000} {
		name := nameTotal(total)
		t.Run(name, func(t *testing.T) {
			s := New(16)
			must(t, s.AddGroup("g", 10), "add g")
			must(t, s.AddGroup("other", 5), "add other")

			// 批量添加 total 台设备（空星策略，空 E == 空 A，不产生任何任务）。
			const batchCap = 256
			added := 0
			for added < total {
				n := total - added
				if n > batchCap {
					n = batchCap
				}
				ops := make([]group.Op, n)
				for i := 0; i < n; i++ {
					ops[i] = group.Op{Kind: group.AddDevice, Dev: devName(added + i)}
				}
				must(t, s.Apply(ops), "bulk add devices")
				added += n
			}
			if len(s.Store().Snapshot().Devices) != total {
				t.Fatalf("want %d devices, got %d", total, len(s.Store().Snapshot().Devices))
			}
			if s.NextPushID() != 0 {
				t.Fatalf("empty policies must produce no tasks, next=%d", s.NextPushID())
			}

			// 5 台加入 g，2 台加入 other。
			join := func(g group.Name, ids ...int) {
				for _, id := range ids {
					must(t, s.AddMember(g, devName(id)), "join")
				}
			}
			join("g", 0, 1, 2, 3, 4)
			join("other", 5, 6)

			s.ResetRecomputed()
			must(t, s.SetPolicy("g", pol("k", "v1")), "setpolicy g")
			if got := s.Recomputed(); got != 5 {
				t.Fatalf("total=%d SetPolicy(g): recomputed=%d, want 5 (member count only)", total, got)
			}

			s.ResetRecomputed()
			must(t, s.SetPriority("g", 11), "setpriority g")
			if got := s.Recomputed(); got != 5 {
				t.Fatalf("total=%d SetPriority(g): recomputed=%d, want 5", total, got)
			}

			s.ResetRecomputed()
			must(t, s.SetPolicy("other", pol("k", "v2")), "setpolicy other (2 members)")
			if got := s.Recomputed(); got != 2 {
				t.Fatalf("total=%d SetPolicy(other): recomputed=%d, want 2", total, got)
			}

			s.ResetRecomputed()
			must(t, s.AddMember("g", devName(7)), "member change recomputes one device")
			if got := s.Recomputed(); got != 1 {
				t.Fatalf("total=%d AddMember: recomputed=%d, want 1", total, got)
			}

			s.ResetRecomputed()
			must(t, s.RemoveMember("g", devName(7)), "remove member recomputes one device")
			if got := s.Recomputed(); got != 1 {
				t.Fatalf("total=%d RemoveMember: recomputed=%d, want 1", total, got)
			}

			s.ResetRecomputed()
			must(t, s.SetPolicy(group.Star, pol("k", "v3")), "setpolicy star recomputes all")
			if got := s.Recomputed(); got != total {
				t.Fatalf("total=%d SetPolicy(*): recomputed=%d, want %d", total, got, total)
			}

			s.ResetRecomputed()
			must(t, s.SetPolicy("g", pol("k", "v1")), "g still 5 members after star change")
			if got := s.Recomputed(); got != 5 {
				t.Fatalf("total=%d second SetPolicy(g): recomputed=%d, want 5", total, got)
			}
			if msg := s.CheckInvariant(); msg != "" {
				t.Fatalf("invariant: %s", msg)
			}
		})
	}
}

func nameTotal(n int) string {
	if n == 100 {
		return "100_devices_5_members"
	}
	return "10000_devices_5_members"
}

func devName(i int) group.Name {
	return group.Name("dev" + pad(i))
}

func pad(i int) string {
	switch {
	case i < 10:
		return "000" + itoa(i)
	case i < 100:
		return "00" + itoa(i)
	case i < 1000:
		return "0" + itoa(i)
	default:
		return itoa(i)
	}
}
