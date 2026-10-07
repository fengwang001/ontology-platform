package lsm

import (
	"errors"
	"testing"
)

// TestInstallSuccess 成功安装：输入原子删除、输出加入、占用释放、终点键更新。
func TestInstallSuccess(t *testing.T) {
	s := newTestService(t, testCfg())
	mustAdd(t, s, mf(1, 1, 10, 20, 1000))
	mustAdd(t, s, mf(2, 1, 30, 40, 1000))
	mustAdd(t, s, mf(3, 2, 15, 35, 10))
	p := mustPick(t, s) // 重写：inputs={1}, next={3}
	if p.Kind != PlanRewrite {
		t.Fatalf("expected rewrite, got %v", p.Kind)
	}
	out := FileMeta{ID: 100, Level: 2, Smallest: k(10), Largest: k(35), Size: 610}
	if err := s.Install(p.ID, []FileMeta{out}); err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if _, err := s.File(1); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("input file 1 must be deleted, got %v", err)
	}
	if _, err := s.File(3); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("next-level input file 3 must be deleted, got %v", err)
	}
	if _, err := s.File(100); err != nil {
		t.Fatalf("output file must be registered: %v", err)
	}
	if s.Pinned(1) || s.Pinned(3) || s.Pinned(100) {
		t.Fatalf("pins must be released after install")
	}
	// 终点键更新为本次源层输入的最大键 20：下一次起点是 min>20 的 f2。
	p2 := mustPick(t, s)
	if err := eqIDs(p2.Inputs, 2); err != nil {
		t.Fatalf("lastKey must advance to input max key: %v", err)
	}
}

// TestInstallMoveReRegister 直接下移计划可用同一编号把文件改挂到目标层。
func TestInstallMoveReRegister(t *testing.T) {
	s := newTestService(t, testCfg())
	mustAdd(t, s, mf(1, 1, 10, 20, 1000))
	p := mustPick(t, s)
	if p.Kind != PlanMove {
		t.Fatalf("expected move, got %v", p.Kind)
	}
	if err := s.Install(p.ID, []FileMeta{mf(1, 2, 10, 20, 1000)}); err != nil {
		t.Fatalf("move install failed: %v", err)
	}
	f, err := s.File(1)
	if err != nil {
		t.Fatalf("file 1 must still exist: %v", err)
	}
	if f.Level != 2 {
		t.Fatalf("file 1 must be re-registered at L2, got L%d", f.Level)
	}
}

// TestInstallFailureAtomic 安装失败整体不生效：文件不动、占用保持、可再次安装。
func TestInstallFailureAtomic(t *testing.T) {
	s := newTestService(t, testCfg())
	mustAdd(t, s, mf(1, 1, 10, 20, 1000))
	mustAdd(t, s, mf(2, 2, 50, 60, 10)) // 目标层不被消耗的文件
	p := mustPick(t, s)                 // move f1 -> L2
	// 输出与目标层现存文件重叠 -> 层不变量被破坏，整体不生效。
	bad := FileMeta{ID: 100, Level: 2, Smallest: k(55), Largest: k(70), Size: 10}
	if err := s.Install(p.ID, []FileMeta{bad}); !errors.Is(err, ErrInvariant) {
		t.Fatalf("expected ErrInvariant, got %v", err)
	}
	if _, err := s.File(1); err != nil {
		t.Fatalf("failed install must not consume inputs: %v", err)
	}
	if _, err := s.File(100); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("failed install must not add outputs")
	}
	if !s.Pinned(1) {
		t.Fatalf("failed install must keep pins")
	}
	// 修正后再次安装成功。
	good := FileMeta{ID: 100, Level: 2, Smallest: k(10), Largest: k(20), Size: 1000}
	if err := s.Install(p.ID, []FileMeta{good}); err != nil {
		t.Fatalf("retry install failed: %v", err)
	}
}

// TestCancelKeepsLastKey 取消释放占用且不更新起点记录。
func TestCancelKeepsLastKey(t *testing.T) {
	s := newTestService(t, testCfg())
	mustAdd(t, s, mf(1, 1, 10, 20, 600))
	mustAdd(t, s, mf(2, 1, 30, 40, 600))
	p1 := mustPick(t, s)
	if err := eqIDs(p1.Inputs, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(p1.ID); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	if s.Pinned(1) {
		t.Fatalf("cancel must release pins")
	}
	// 起点记录未动：再次选取仍从 f1 开始，计划完全一致。
	p2 := mustPick(t, s)
	if err := eqIDs(p2.Inputs, 1); err != nil {
		t.Fatalf("cancel must not advance lastKey: %v", err)
	}
	if p2.Kind != p1.Kind || p2.Level != p1.Level {
		t.Fatalf("re-pick after cancel must reproduce the same plan")
	}
	// 已取消的计划不能再安装或取消。
	if err := s.Cancel(p1.ID); !errors.Is(err, ErrPlanNotFound) {
		t.Fatalf("expected ErrPlanNotFound, got %v", err)
	}
	if err := s.Install(p1.ID, nil); !errors.Is(err, ErrPlanNotFound) {
		t.Fatalf("expected ErrPlanNotFound, got %v", err)
	}
}

// TestErrorPrecedence 同时触及多类错误时只报次序最靠前的一类：
// 参数非法 > 文件不存在 > 文件已被占用 > 计划不存在 > 层不变量被破坏。
func TestErrorPrecedence(t *testing.T) {
	s := newTestService(t, testCfg())
	mustAdd(t, s, mf(1, 1, 10, 20, 1000))
	p := mustPick(t, s) // 占用 f1

	// 参数非法（min>max）+ 计划不存在 -> 参数非法。
	bad := FileMeta{ID: 50, Level: 2, Smallest: k(9), Largest: k(1), Size: 1}
	if err := s.Install(999, []FileMeta{bad}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid argument must win over plan-not-found, got %v", err)
	}
	// 文件已被占用（输出编号撞上在途占用文件）+ 计划不存在 -> 文件已被占用。
	clash := mf(1, 2, 10, 20, 1000)
	if err := s.Install(999, []FileMeta{clash}); !errors.Is(err, ErrFilePinned) {
		t.Fatalf("file-pinned must win over plan-not-found, got %v", err)
	}
	// 仅计划不存在。
	ok := FileMeta{ID: 51, Level: 2, Smallest: k(10), Largest: k(20), Size: 1}
	if err := s.Install(999, []FileMeta{ok}); !errors.Is(err, ErrPlanNotFound) {
		t.Fatalf("expected plan-not-found, got %v", err)
	}
	// 文件不存在。
	if _, err := s.File(4242); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("expected file-not-found, got %v", err)
	}
	// 输出落在错误层 + 计划存在 -> 参数非法优先于一切后续检查。
	wrongLevel := FileMeta{ID: 52, Level: 3, Smallest: k(10), Largest: k(20), Size: 1}
	if err := s.Install(p.ID, []FileMeta{wrongLevel}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid argument must win, got %v", err)
	}
}

// TestAddFileValidation 登记时的参数校验与层不变量校验。
func TestAddFileValidation(t *testing.T) {
	s := newTestService(t, testCfg())
	if err := s.AddFile(mf(1, 9, 1, 2, 1)); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("level out of range: %v", err)
	}
	if err := s.AddFile(FileMeta{ID: 2, Level: 1, Smallest: k(5), Largest: k(3)}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("min>max: %v", err)
	}
	mustAdd(t, s, mf(3, 1, 10, 20, 10))
	if err := s.AddFile(mf(3, 1, 30, 40, 10)); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("duplicate id: %v", err)
	}
	// 端点相接允许。
	mustAdd(t, s, mf(4, 1, 20, 30, 10))
	// 真重叠违反层不变量。
	if err := s.AddFile(mf(5, 1, 15, 25, 10)); !errors.Is(err, ErrInvariant) {
		t.Fatalf("overlap must violate invariant, got %v", err)
	}
	// 零层允许任意重叠。
	mustAdd(t, s, mf(6, 0, 10, 50, 1))
	mustAdd(t, s, mf(7, 0, 10, 50, 1))
}

// TestInstallEmptyOutputs 允许空输出（输入被整体吞并删除）。
func TestInstallEmptyOutputs(t *testing.T) {
	s := newTestService(t, testCfg())
	mustAdd(t, s, mf(1, 1, 10, 20, 1000))
	p := mustPick(t, s)
	if err := s.Install(p.ID, nil); err != nil {
		t.Fatalf("empty outputs install failed: %v", err)
	}
	if _, err := s.File(1); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("input must be consumed")
	}
}
