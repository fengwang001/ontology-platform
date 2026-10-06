package toollife_test

import (
	"testing"

	"ontology/toollife"
)

func TestConfigValidation(t *testing.T) {
	s := toollife.NewService()
	mustCode(t, s.Magazine().AddGroup("", strictCfg(10)), toollife.ErrInvalid)

	bad := strictCfg(10)
	bad.Basis = toollife.LifeBasis(99)
	mustCode(t, s.Magazine().AddGroup("g", bad), toollife.ErrInvalid)

	bad = strictCfg(10)
	bad.Mode = toollife.SelectMode(99)
	mustCode(t, s.Magazine().AddGroup("g", bad), toollife.ErrInvalid)

	bad = strictCfg(10)
	bad.WarnPermille = 1001
	mustCode(t, s.Magazine().AddGroup("g", bad), toollife.ErrInvalid)

	mustCode(t, s.Magazine().AddGroup("g", strictCfg(10)), 0)
	mustCode(t, s.Magazine().AddGroup("g", strictCfg(10)), toollife.ErrInvalid) // 重复刀组
	mustCode(t, s.Magazine().AddTool("g", ""), toollife.ErrInvalid)
	mustCode(t, s.Magazine().AddTool("nope", "t0"), toollife.ErrGroupNotFound)
	mustCode(t, s.Magazine().AddTool("g", "t0"), 0)
	mustCode(t, s.Magazine().AddTool("g", "t0"), toollife.ErrInvalid) // 重复刀号
}

// 寿命上限为 0 的刀组：任何正申请在严格/宽松下都无刀可用（严格自身余量不足）。
func TestZeroLifeLimit(t *testing.T) {
	for _, mode := range []toollife.SelectMode{toollife.ModeStrict, toollife.ModeLenient} {
		cfg := toollife.GroupConfig{Basis: toollife.BasisPieces, LifeLimit: 0, WarnPermille: 500, Mode: mode}
		s := newSvc(t, "g", cfg, "t0")
		_, err := s.Apply("a1", "g", 1)
		mustCode(t, err, toollife.ErrNoTool)
	}
}

// 件数口径与秒口径在寿命算术上等价；首笔记账恰好到 200‰ 即预警。
func TestPiecesBasisAndZeroWarn(t *testing.T) {
	cfg := toollife.GroupConfig{Basis: toollife.BasisPieces, LifeLimit: 5, WarnPermille: 200, Mode: toollife.ModeStrict}
	s := newSvc(t, "g", cfg, "t0")
	okApply(t, s, "a1", "g", 1)
	mustCode(t, s.Settle("a1", 1), 0)
	if len(s.Warnings()) != 1 || s.Warnings()[0].Permille != 200 {
		t.Fatalf("warn threshold 0 must fire on first settle, got %+v", s.Warnings())
	}
}

func TestStatusString(t *testing.T) {
	if toollife.ToolStatus(99).String() != "unknown" {
		t.Fatalf("unknown status string")
	}
	if toollife.StatusBroken.String() != "broken" {
		t.Fatalf("broken string")
	}
}

// 换新时新刀号与现存刀冲突 -> 参数非法；换新参数缺失 -> 参数非法。
func TestReplaceValidation(t *testing.T) {
	s := newSvc(t, "g", strictCfg(100), "t0", "t1")
	mustCode(t, s.ReportBroken("g", "t0"), 0)
	mustCode(t, s.Replace("g", "t0", "t1"), toollife.ErrInvalid) // 新刀号已存在
	mustCode(t, s.Replace("g", "t0", "t0"), toollife.ErrInvalid) // 新旧同名
	mustCode(t, s.Replace("", "t0", "tn"), toollife.ErrInvalid)
	_, err := s.Query("")
	mustCode(t, err, toollife.ErrInvalid)
}

// 已耗尽刀重复报破损 -> 状态不允许；破损/锁定重复操作幂等。
func TestBrokenExhaustedAndIdempotentStateOps(t *testing.T) {
	s := newSvc(t, "g", strictCfg(5), "t0", "t1")
	okApply(t, s, "a1", "g", 5)
	mustCode(t, s.Settle("a1", 5), 0)
	mustCode(t, s.ReportBroken("g", "t0"), toollife.ErrState) // 已耗尽
	mustCode(t, s.Lock("g", "t1"), 0)
	mustCode(t, s.Lock("g", "t1"), 0) // 幂等
	mustCode(t, s.Unlock("g", "t1"), 0)
	mustCode(t, s.Unlock("g", "t1"), 0) // 幂等
	mustCode(t, s.ReportBroken("g", "t1"), 0)
	mustCode(t, s.ReportBroken("g", "t1"), 0) // 幂等
	mustCode(t, s.Lock("g", "t1"), toollife.ErrState)
}
