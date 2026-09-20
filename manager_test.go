package ontology

import "testing"

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	schema, err := NewSchema(
		Field{Name: "host", Kind: StringKind},
		Field{Name: "port", Kind: IntKind, Min: 1, Max: 65535},
		Field{Name: "retries", Kind: IntKind, Min: 0, Max: 10},
	)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	m, err := NewManager(schema, map[string]any{
		"host": "a", "port": int64(80), "retries": int64(3),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

func mustUpdate(t *testing.T, m *Manager, changes map[string]any) uint64 {
	t.Helper()
	v, err := m.Update(changes)
	if err != nil {
		t.Fatalf("Update(%v): %v", changes, err)
	}
	return v
}

// 快照与后续更新完全隔离：Acquire 之后无论更新多少次，
// 快照读到的字段值都不变，且版本号停留在获取时。
func TestSnapshotIsolation(t *testing.T) {
	m := newTestManager(t)
	s := m.Acquire()
	defer s.Release()
	if s.Version() != 1 {
		t.Fatalf("snapshot version = %d, want 1", s.Version())
	}
	mustUpdate(t, m, map[string]any{"host": "b"})
	mustUpdate(t, m, map[string]any{"port": int64(8080), "host": "c"})

	host, err := s.Get("host")
	if err != nil {
		t.Fatalf("Get(host): %v", err)
	}
	if host != "a" {
		t.Fatalf("snapshot host = %v, want a", host)
	}
	port, err := s.Get("port")
	if err != nil {
		t.Fatalf("Get(port): %v", err)
	}
	if port != int64(80) {
		t.Fatalf("snapshot port = %v, want 80", port)
	}
	if got := m.Current(); got != 3 {
		t.Fatalf("current = %d, want 3", got)
	}
}

// 字段级来源版本：只有值真正变化的字段来源版本才前进；
// 同一字段连续两次更新成相同的值，来源版本不变（既定规则）。
func TestFieldSourceVersion(t *testing.T) {
	m := newTestManager(t)
	v2 := mustUpdate(t, m, map[string]any{"host": "b"})
	v3 := mustUpdate(t, m, map[string]any{"host": "b"}) // 相同的值
	v4 := mustUpdate(t, m, map[string]any{"port": int64(8080)})

	if v2 != 2 || v3 != 3 || v4 != 4 {
		t.Fatalf("versions = %d,%d,%d, want 2,3,4", v2, v3, v4)
	}
	cases := []struct {
		field string
		want  uint64
	}{
		{"host", 2},    // v3 写入相同值，来源版本不前进
		{"port", 4},    // v4 才变化
		{"retries", 1}, // 从未变化，仍是初始版本
	}
	for _, c := range cases {
		got, err := m.SourceAt(m.Current(), c.field)
		if err != nil {
			t.Fatalf("SourceAt(%s): %v", c.field, err)
		}
		if got != c.want {
			t.Errorf("source(%s) = %d, want %d", c.field, got, c.want)
		}
	}
	// 快照上也能读到字段级来源版本。
	s := m.Acquire()
	defer s.Release()
	got, err := s.SourceVersion("host")
	if err != nil {
		t.Fatalf("SourceVersion(host): %v", err)
	}
	if got != 2 {
		t.Errorf("snapshot source(host) = %d, want 2", got)
	}
}

// 切换到历史版本的内容产生更大的新版本号，历史内容保持原样可查。
func TestRestoreProducesLargerVersion(t *testing.T) {
	m := newTestManager(t)
	mustUpdate(t, m, map[string]any{"host": "b"})
	mustUpdate(t, m, map[string]any{"host": "c", "port": int64(8080)})

	before := m.Current()
	nv, err := m.Restore(1)
	if err != nil {
		t.Fatalf("Restore(1): %v", err)
	}
	if nv <= before {
		t.Fatalf("restore version = %d, want > %d", nv, before)
	}
	if got := m.Current(); got != nv {
		t.Fatalf("current = %d, want %d", got, nv)
	}
	// 新当前版本的内容等于历史版本 1 的内容。
	for name, want := range map[string]any{"host": "a", "port": int64(80), "retries": int64(3)} {
		got, err := m.GetAt(nv, name)
		if err != nil {
			t.Fatalf("GetAt(%d, %s): %v", nv, name, err)
		}
		if got != want {
			t.Errorf("restored %s = %v, want %v", name, got, want)
		}
	}
	// 历史版本 1 的内容仍能取到原样。
	host, err := m.GetAt(1, "host")
	if err != nil {
		t.Fatalf("GetAt(1, host): %v", err)
	}
	if host != "a" {
		t.Errorf("history v1 host = %v, want a", host)
	}
}

// 版本号严格递增且永不复用：回收之后也不会重新分配旧号码。
func TestVersionNeverReused(t *testing.T) {
	m := newTestManager(t)
	mustUpdate(t, m, map[string]any{"host": "b"})
	mustUpdate(t, m, map[string]any{"host": "c"})
	if gone := m.Collect(); len(gone) == 0 {
		t.Fatal("Collect reclaimed nothing")
	}
	v := mustUpdate(t, m, map[string]any{"host": "d"})
	if v != 4 {
		t.Fatalf("version after collect = %d, want 4 (never reuse)", v)
	}
	// 已回收的版本号不可再查询。
	if _, err := m.GetAt(1, "host"); err == nil {
		t.Fatal("GetAt on reclaimed version: want error, got nil")
	}
}
