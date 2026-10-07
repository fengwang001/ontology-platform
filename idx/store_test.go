package idx

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func activeProps() map[string]PropDef {
	return map[string]PropDef{
		"color": {Status: PropActive},
		"hue":   {Status: PropActive},
		"other": {Status: PropActive},
	}
}

func newTestStore(t *testing.T, audit AuditSink) *Store {
	t.Helper()
	s, err := NewStore(NewTypeRegistry(activeProps()), "color", WithAudit(audit))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func mustQuery(t *testing.T, s *Store, value string) []string {
	t.Helper()
	got, err := s.Query(value)
	if err != nil {
		t.Fatalf("Query(%q): %v", value, err)
	}
	return got
}

// TestOutOfOrderDuplicateExhaustive 穷举乱序与重复到达的边界情形：
// 3 条事件的全排列 × 每条事件到达 1 或 2 次，共 6×8=48 种投递序列，
// 最终索引都必须等于按 (Version,ID) 串行应用后的最终取值。
func TestOutOfOrderDuplicateExhaustive(t *testing.T) {
	base := []Event{
		{ID: "e1", ObjectID: "o1", Property: "color", Value: "red", Version: 1},
		{ID: "e2", ObjectID: "o1", Property: "color", Value: "green", Version: 2},
		{ID: "e3", ObjectID: "o1", Property: "color", Value: "blue", Version: 3},
	}
	perms := [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	for mask := 0; mask < 8; mask++ {
		for _, perm := range perms {
			var seq []Event
			for _, i := range perm {
				seq = append(seq, base[i])
				if mask&(1<<i) != 0 {
					seq = append(seq, base[i]) // 重复投递
				}
			}
			name := fmt.Sprintf("mask=%d/perm=%v", mask, perm)
			t.Run(name, func(t *testing.T) {
				s := newTestStore(t, nil)
				for _, ev := range seq {
					if err := s.Ingest(ev); err != nil {
						t.Fatalf("Ingest(%+v): %v", ev, err)
					}
				}
				if got := mustQuery(t, s, "blue"); !reflect.DeepEqual(got, []string{"o1"}) {
					t.Fatalf("final value lost: Query(blue)=%v", got)
				}
				for _, stale := range []string{"red", "green"} {
					if got := mustQuery(t, s, stale); len(got) != 0 {
						t.Fatalf("stale value %q still indexed: %v", stale, got)
					}
				}
			})
		}
	}
}

// TestTombstone 置空事件同样遵循 LWW：乱序到达的旧取值不得复活。
func TestTombstone(t *testing.T) {
	s := newTestStore(t, nil)
	events := []Event{
		{ID: "e2", ObjectID: "o1", Property: "color", Null: true, Version: 2},
		{ID: "e1", ObjectID: "o1", Property: "color", Value: "red", Version: 1},
	}
	for _, ev := range events {
		if err := s.Ingest(ev); err != nil {
			t.Fatal(err)
		}
	}
	if got := mustQuery(t, s, "red"); len(got) != 0 {
		t.Fatalf("tombstoned value still indexed: %v", got)
	}
}

// TestSwitchAttribution 跨越切换点的事件归属：
// 切换期间到达的新字段事件归入新版本，旧字段事件归入旧版本，
// 与物理到达时序无关。
func TestSwitchAttribution(t *testing.T) {
	audit := &MemAudit{}
	s := newTestStore(t, audit)

	mustIngest := func(ev Event) {
		t.Helper()
		if err := s.Ingest(ev); err != nil {
			t.Fatalf("Ingest(%+v): %v", ev, err)
		}
	}
	mustIngest(Event{ID: "c1", ObjectID: "o1", Property: "color", Value: "red", Version: 1})
	mustIngest(Event{ID: "h1", ObjectID: "o1", Property: "hue", Value: "warm", Version: 1})

	if err := s.BeginSwitch("hue"); err != nil {
		t.Fatalf("BeginSwitch: %v", err)
	}
	// 切换进行中：新字段事件与旧字段事件交错到达。
	mustIngest(Event{ID: "h2", ObjectID: "o1", Property: "hue", Value: "cool", Version: 2})
	mustIngest(Event{ID: "c2", ObjectID: "o1", Property: "color", Value: "green", Version: 2})

	// 切换期间查询必须报 ErrSwitchInProgress。
	if _, err := s.Query("warm"); !errors.Is(err, ErrSwitchInProgress) {
		t.Fatalf("query during switch: got %v, want ErrSwitchInProgress", err)
	}

	if err := s.CommitSwitch(); err != nil {
		t.Fatalf("CommitSwitch: %v", err)
	}
	epochID, basis := s.Active()
	if basis != "hue" {
		t.Fatalf("active basis = %q, want hue", basis)
	}
	if got := mustQuery(t, s, "cool"); !reflect.DeepEqual(got, []string{"o1"}) {
		t.Fatalf("Query(cool) = %v", got)
	}
	// 旧字段事件不得泄漏进新索引。
	if got := mustQuery(t, s, "green"); len(got) != 0 {
		t.Fatalf("old-basis event leaked into new epoch: %v", got)
	}

	// 审计：两条缓冲事件被分别归入正确版本。
	var classifiedNew, classifiedOld bool
	for _, e := range audit.Entries() {
		if e.Event == nil {
			continue
		}
		switch e.Event.ID {
		case "h2":
			if e.Kind == AuditApplied && e.EpochID == epochID {
				classifiedNew = true
			}
		case "c2":
			if e.Kind == AuditApplied && e.EpochID == epochID-1 {
				classifiedOld = true
			}
		}
	}
	if !classifiedNew || !classifiedOld {
		t.Fatalf("attribution audit missing: new=%v old=%v", classifiedNew, classifiedOld)
	}
}

// TestSwitchRollback 校验失败时整体回滚：缓冲事件不丢失，
// 旧字段事件继续生效，新字段事件在未来成功切换时经回填生效。
func TestSwitchRollback(t *testing.T) {
	s := newTestStore(t, nil)
	mustIngest := func(ev Event) {
		t.Helper()
		if err := s.Ingest(ev); err != nil {
			t.Fatalf("Ingest(%+v): %v", ev, err)
		}
	}
	mustIngest(Event{ID: "c1", ObjectID: "o1", Property: "color", Value: "red", Version: 1})
	mustIngest(Event{ID: "c2", ObjectID: "o2", Property: "color", Value: "blue", Version: 1})

	if err := s.BeginSwitch("hue"); err != nil {
		t.Fatal(err)
	}
	// o1 有了 hue，o2 没有 → 默认校验器（全体非空）将失败。
	mustIngest(Event{ID: "h1", ObjectID: "o1", Property: "hue", Value: "warm", Version: 1})
	mustIngest(Event{ID: "c3", ObjectID: "o1", Property: "color", Value: "green", Version: 2})

	err := s.CommitSwitch()
	if !errors.Is(err, ErrSwitchValidationFailed) {
		t.Fatalf("CommitSwitch: got %v, want ErrSwitchValidationFailed", err)
	}
	// 回滚后旧索引立即可查，且缓冲的旧字段事件已生效。
	if _, basis := s.Active(); basis != "color" {
		t.Fatalf("basis after rollback = %q, want color", basis)
	}
	if got := mustQuery(t, s, "green"); !reflect.DeepEqual(got, []string{"o1"}) {
		t.Fatalf("buffered old-basis event lost after rollback: %v", got)
	}
	// 缓冲的新字段事件不得丢失：补齐 o2 的 hue 后再次切换应生效。
	mustIngest(Event{ID: "h2", ObjectID: "o2", Property: "hue", Value: "cold", Version: 1})
	if err := s.BeginSwitch("hue"); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitSwitch(); err != nil {
		t.Fatalf("second CommitSwitch: %v", err)
	}
	if got := mustQuery(t, s, "warm"); !reflect.DeepEqual(got, []string{"o1"}) {
		t.Fatalf("buffered new-basis event lost across rollback: %v", got)
	}
	if got := mustQuery(t, s, "cold"); !reflect.DeepEqual(got, []string{"o2"}) {
		t.Fatalf("Query(cold) = %v", got)
	}
}

// TestRapidChangesCrossingSwitch 同一对象同一属性连续多次变更跨越
// 切换瞬间：最终索引只反映合法顺序下的最终取值，中间取值不得固化。
func TestRapidChangesCrossingSwitch(t *testing.T) {
	s := newTestStore(t, nil)
	mustIngest := func(id string, v uint64, value string) {
		t.Helper()
		if err := s.Ingest(Event{ID: EventID(id), ObjectID: "o1", Property: "hue", Value: value, Version: v}); err != nil {
			t.Fatalf("Ingest(%s): %v", id, err)
		}
	}
	// 初始依据字段上给 o1 一个取值，保证切换前可查。
	if err := s.Ingest(Event{ID: "c0", ObjectID: "o1", Property: "color", Value: "red", Version: 1}); err != nil {
		t.Fatal(err)
	}
	mustIngest("h1", 1, "h1")
	mustIngest("h2", 2, "h2")
	if err := s.BeginSwitch("hue"); err != nil {
		t.Fatal(err)
	}
	mustIngest("h4", 4, "h4") // 缓冲
	mustIngest("h3", 3, "h3") // 乱序，缓冲
	if err := s.CommitSwitch(); err != nil {
		t.Fatal(err)
	}
	mustIngest("h6", 6, "h6")
	mustIngest("h5", 5, "h5") // 乱序到达

	if got := mustQuery(t, s, "h6"); !reflect.DeepEqual(got, []string{"o1"}) {
		t.Fatalf("final value: Query(h6) = %v", got)
	}
	for _, mid := range []string{"h1", "h2", "h3", "h4", "h5"} {
		if got := mustQuery(t, s, mid); len(got) != 0 {
			t.Fatalf("intermediate value %q frozen into index: %v", mid, got)
		}
	}
}

// TestErrorPriority 多类错误条件同时满足时只报告优先级最高的一类。
func TestErrorPriority(t *testing.T) {
	// 直接校验 pickError 的全序。
	all := []error{
		ErrSwitchInProgress,
		ErrPropertyNotDefined,
		ErrSwitchValidationFailed,
		ErrDeprecatedNoReplacement,
	}
	if got := pickError(all); !errors.Is(got, ErrDeprecatedNoReplacement) {
		t.Fatalf("pickError = %v", got)
	}

	// 构造同时触发 (a) 废弃无替代 与 (c) 当时未定义 的摄入：
	// legacy 自 v10 起定义，v20 起废弃且无替代；事件版本 5 当时未定义。
	reg := NewTypeRegistry(map[string]PropDef{"color": {Status: PropActive}})
	reg.Migrate(10, map[string]PropDef{
		"color":  {Status: PropActive},
		"legacy": {Status: PropActive},
	})
	s, err := NewStore(reg, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	reg.Migrate(20, map[string]PropDef{
		"color":  {Status: PropActive},
		"legacy": {Status: PropDeprecated, ReplacedBy: ""},
	})
	err = s.Ingest(Event{ID: "x", ObjectID: "o1", Property: "legacy", Value: "v", Version: 5})
	if !errors.Is(err, ErrDeprecatedNoReplacement) {
		t.Fatalf("got %v, want ErrDeprecatedNoReplacement", err)
	}
	// 单纯 (c)：从未定义的字段。
	err = s.Ingest(Event{ID: "y", ObjectID: "o1", Property: "ghost", Value: "v", Version: 30})
	if !errors.Is(err, ErrPropertyNotDefined) {
		t.Fatalf("got %v, want ErrPropertyNotDefined", err)
	}
}

// TestAuditTrail 每次判定均记录输入、所依据的索引版本与结论。
func TestAuditTrail(t *testing.T) {
	audit := &MemAudit{}
	s := newTestStore(t, audit)
	ev := Event{ID: "e1", ObjectID: "o1", Property: "color", Value: "red", Version: 1}
	if err := s.Ingest(ev); err != nil {
		t.Fatal(err)
	}
	if err := s.Ingest(ev); err != nil { // 重复
		t.Fatal(err)
	}
	if _, err := s.Query("red"); err != nil {
		t.Fatal(err)
	}
	var applied, dup, query bool
	for _, e := range audit.Entries() {
		if e.EpochID < 0 && e.Kind != AuditQuery {
			t.Fatalf("entry %+v missing epoch attribution", e)
		}
		switch e.Kind {
		case AuditApplied:
			applied = true
		case AuditDuplicate:
			dup = true
		case AuditQuery:
			query = true
		}
	}
	if !applied || !dup || !query {
		t.Fatalf("audit incomplete: applied=%v dup=%v query=%v", applied, dup, query)
	}
}
