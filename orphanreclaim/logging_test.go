package orphanreclaim

import "testing"

// 每次判定与每次代际推进都记录输入、输出与据以判定的入边组合。
func TestDecisionAndAdvanceLogging(t *testing.T) {
	r, c, lg := newTestEngine(t, testConfig(), 0)
	r.CreateObject("a")
	r.CreateObject("s1")
	r.CreateObject("s2")

	mustAdd(t, r, "s1", "a", "J1") // 判定 #1：孤儿，入队第一代
	d0 := lg.decisions[0]
	if d0.Object != "a" || d0.Trigger != "add_link" || !d0.Orphan ||
		d0.Outcome != "queued_gen1" || d0.PreviousGen != GenNone {
		t.Fatalf("decision#1 = %+v", d0)
	}
	if d0.InCounts["J1"] != 1 || d0.Basis.Layer != "none" {
		t.Fatalf("decision#1 basis/input wrong: %+v", d0)
	}

	c.t = 10
	r.Advance() // 推进：a -> gen2
	if len(lg.advances) != 1 {
		t.Fatalf("want 1 advance record, got %d", len(lg.advances))
	}
	a0 := lg.advances[0]
	if len(a0.Promoted) != 1 || a0.Promoted[0] != "a" || len(a0.Purged) != 0 {
		t.Fatalf("advance#0 = %+v", a0)
	}

	mustAdd(t, r, "s2", "a", "I") // 二代内救回：判定日志记录独立保留依据
	last := lg.decisions[len(lg.decisions)-1]
	if last.PreviousGen != Gen2 || last.Outcome != "rescued" ||
		last.Basis.Layer != "independent" {
		t.Fatalf("gen2 rescue decision = %+v", last)
	}

	// 空推进（无到期对象）不产生推进日志，避免噪声。
	c.t = 11
	r.Advance()
	if len(lg.advances) != 1 {
		t.Fatalf("no-op advance must not be logged, got %d records", len(lg.advances))
	}
}
