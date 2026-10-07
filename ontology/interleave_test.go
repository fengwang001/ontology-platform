package ontology

import (
	"fmt"
	"testing"
)

// 交织场景：类型 T 的默认时区从 v1(UTC) 迁移到 v2(UTC+08:00)，生效序号 S=10。
// 事件写入序号跨越边界：8、9（锚定 v1）、10、11（锚定 v2，10 为边界值）。
// 这些事件可能滞后到达，与迁移记录以任意顺序交织。
// 无论到达顺序如何，最终视图必须相同——分组归属只取决于写入时刻锚定的版本。

const migEffectiveSeq = 10

func interleaveEvents() []ChangeEvent {
	mk := func(obj string, seq uint64, anchored int, zone string, h int) ChangeEvent {
		return ChangeEvent{
			ObjectID:          obj,
			ObjectTypeID:      "T",
			WriteSeq:          seq,
			LocalValue:        local(2024, 1, 1, h, 0),
			AnchoredTzVersion: anchored,
			AnchoredZoneID:    zone,
		}
	}
	return []ChangeEvent{
		mk("a", 8, 1, "UTC", 1),        // 迁移前写入，锚定 v1
		mk("b", 9, 1, "UTC", 2),        // 迁移前写入（生效序号前一刻），锚定 v1
		mk("c", 10, 2, "UTC+08:00", 3), // 边界：写入序号 == 生效序号，锚定 v2
		mk("d", 11, 2, "UTC+08:00", 4), // 迁移后写入，锚定 v2
	}
}

func newInterleavePlatform() *Platform {
	p := NewPlatform()
	p.AddObjectType("T", "at", &TzDefVersion{Version: 1, ZoneID: "UTC", EffectiveSeq: 1})
	p.AddObjectType("U", "at", &TzDefVersion{Version: 1, ZoneID: "UTC", EffectiveSeq: 1})
	p.AddLink("t-u", "T", "U")
	p.CreateView("v", "t-u")
	return p
}

// permutations 生成下标序列的全部排列。
func permutations(n int) [][]int {
	if n == 1 {
		return [][]int{{0}}
	}
	var out [][]int
	for _, sub := range permutations(n - 1) {
		for i := 0; i <= len(sub); i++ {
			perm := make([]int, 0, n)
			perm = append(perm, sub[:i]...)
			perm = append(perm, n-1)
			perm = append(perm, sub[i:]...)
			out = append(out, perm)
		}
	}
	return out
}

// TestMigrationLateEventInterleavings 穷举「4 条事件的到达排列 × 迁移插入位置
// × 维护粒度」的全部交织情形，断言最终视图完全一致。
func TestMigrationLateEventInterleavings(t *testing.T) {
	events := interleaveEvents()
	var reference string
	cases := 0
	for _, perm := range permutations(len(events)) {
		// 迁移在到达序列中的插入位置：0..len(events)。
		for migPos := 0; migPos <= len(events); migPos++ {
			// 维护粒度：每条到达后立即维护，或全部到达后一次性维护。
			for _, eager := range []bool{true, false} {
				p := newInterleavePlatform()
				migrated := false
				migrate := func() {
					if err := p.MigrateDefaultTz("T", TzDefVersion{
						Version: 2, ZoneID: "UTC+08:00", EffectiveSeq: migEffectiveSeq,
					}); err != nil {
						t.Fatalf("migration rejected: %v", err)
					}
					migrated = true
				}
				for i, idx := range perm {
					if i == migPos {
						migrate()
					}
					p.InjectEvent(events[idx])
					if eager {
						p.Maintain("v")
					}
				}
				if !migrated { // migPos == len(events) 时迁移在最后
					migrate()
				}
				p.Maintain("v")
				groups, _ := p.Query("v")
				got := fmt.Sprint(groups)
				if reference == "" {
					reference = got
				} else if got != reference {
					t.Fatalf("interleaving perm=%v migPos=%d eager=%v diverged:\n%s\nwant:\n%s",
						perm, migPos, eager, got, reference)
				}
				cases++
			}
		}
	}
	t.Logf("verified %d interleavings, all identical", cases)
}

// TestWriteSeqBoundaryAnchoring 验证写入序号在迁移生效序号边界上的锚定归属：
// seq < S 锚定旧版本，seq >= S 锚定新版本。
func TestWriteSeqBoundaryAnchoring(t *testing.T) {
	p := newInterleavePlatform()
	if err := p.MigrateDefaultTz("T", TzDefVersion{Version: 2, ZoneID: "UTC+08:00", EffectiveSeq: migEffectiveSeq}); err != nil {
		t.Fatalf("migration rejected: %v", err)
	}
	for _, evt := range interleaveEvents() {
		p.InjectEvent(evt)
	}
	p.Maintain("v")
	groups, _ := p.Query("v")
	keyOf := func(id string) int64 {
		for _, g := range groups {
			for _, m := range g.Members {
				if m.ObjectID == id {
					return g.Key
				}
			}
		}
		t.Fatalf("object %s not in view", id)
		return -1
	}
	// a: 01:00 @UTC → UTC 01:00；b: 02:00 @UTC → UTC 02:00（同一日桶）
	// c: 03:00 @UTC+8 → UTC 前一日 19:00；d: 04:00 @UTC+8 → UTC 前一日 20:00（同一日桶）
	if keyOf("a") != keyOf("b") {
		t.Fatalf("a,b should share group (both anchored v1)")
	}
	if keyOf("c") != keyOf("d") {
		t.Fatalf("c,d should share group (both anchored v2)")
	}
	if keyOf("a") == keyOf("c") {
		t.Fatalf("a,c should be in different groups")
	}
	if keyOf("c") != keyOf("a")-1 {
		t.Fatalf("c should be exactly one day-bucket before a: a=%d c=%d", keyOf("a"), keyOf("c"))
	}
}

// TestLateEventAfterMigrationUsesAnchoredVersion 验证规约核心：
// 迁移之后到达的滞后事件，仍按写入时刻锚定的（迁移前）版本分组。
func TestLateEventAfterMigrationUsesAnchoredVersion(t *testing.T) {
	p := newInterleavePlatform()
	// 第一批对象先处理。
	p.InjectEvent(ChangeEvent{ObjectID: "early", ObjectTypeID: "T", WriteSeq: 5,
		LocalValue: local(2024, 1, 1, 12, 0), AnchoredTzVersion: 1, AnchoredZoneID: "UTC"})
	p.Maintain("v")
	// 迁移发生。
	if err := p.MigrateDefaultTz("T", TzDefVersion{Version: 2, ZoneID: "UTC+08:00", EffectiveSeq: 6}); err != nil {
		t.Fatalf("migration rejected: %v", err)
	}
	// 滞后到达的事件：写入序号 4（迁移生效前），锚定 v1。
	p.InjectEvent(ChangeEvent{ObjectID: "late", ObjectTypeID: "T", WriteSeq: 4,
		LocalValue: local(2024, 1, 1, 12, 0), AnchoredTzVersion: 1, AnchoredZoneID: "UTC"})
	p.Maintain("v")
	groups, _ := p.Query("v")
	// early 与 late 本地值相同、锚定版本相同，必须同组。
	if len(groups) != 1 || len(groups[0].Members) != 2 {
		t.Fatalf("late event not grouped by anchored version: %v", groups)
	}
}
