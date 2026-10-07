package lsm

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// sortedMetas 返回按编号排序的文件副本，用于与朴素模型按集合对照。
func sortedMetas(files []FileMeta) []FileMeta {
	out := make([]FileMeta, len(files))
	copy(out, files)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// pendingPair 记录一个在途计划在正式实现与朴素模型中的对应关系。
type pendingPair struct {
	realID uint64
	plan   *naivePlan
	real   *Plan
}

// runSequence 以固定种子生成随机操作序列，同时驱动正式实现与朴素模型，
// 逐步对照结果，并返回每条操作的日志（输入、输出与判定依据）。
func runSequence(t *testing.T, seed int64, ops int) []string {
	t.Helper()
	cfg := Config{NumLevels: 4, L0Trigger: 3, BaseLevelBytes: 3000, LevelMultiplier: 10}
	real := newTestService(t, cfg)
	model := newNaive(cfg)
	rng := rand.New(rand.NewSource(seed))
	var logs []string
	logf := func(format string, args ...interface{}) {
		line := fmt.Sprintf(format, args...)
		logs = append(logs, line)
		t.Log(line)
	}

	var nextFileID uint64 = 1
	var pending []pendingPair

	errCategory := func(err error) string {
		switch {
		case err == nil:
			return "ok"
		case errors.Is(err, ErrInvalidArgument):
			return "invalid-argument"
		case errors.Is(err, ErrFileNotFound):
			return "file-not-found"
		case errors.Is(err, ErrFilePinned):
			return "file-pinned"
		case errors.Is(err, ErrPlanNotFound):
			return "plan-not-found"
		case errors.Is(err, ErrInvariant):
			return "invariant-violated"
		default:
			return "unknown"
		}
	}

	for op := 0; op < ops; op++ {
		choice := rng.Intn(100)
		if len(pending) == 0 && choice >= 70 {
			choice = 0 // 无在途计划时退化为登记
		}
		switch {
		case choice < 40:
			// 登记文件：非零层尝试若干次以找到满足不变量的区间。
			var f FileMeta
			var realErr, modelErr error
			for attempt := 0; attempt < 8; attempt++ {
				level := rng.Intn(cfg.NumLevels)
				lo := rng.Intn(60)
				hi := lo + rng.Intn(8)
				f = FileMeta{
					ID:       nextFileID,
					Level:    level,
					Smallest: k(byte(lo)),
					Largest:  k(byte(hi)),
					Size:     int64(rng.Intn(2000)),
				}
				realErr = real.AddFile(f)
				modelErr = model.add(f)
				if errCategory(realErr) != errCategory(modelErr) {
					t.Fatalf("op %d AddFile %+v: real=%s model=%s", op, f, errCategory(realErr), errCategory(modelErr))
				}
				if realErr == nil || level == 0 {
					break
				}
				// 不变量冲突：两边都已拒绝，换区间重试。
			}
			if realErr == nil {
				nextFileID++
			}
			logf("op %d AddFile id=%d L%d [%02x,%02x] size=%d -> %s",
				op, f.ID, f.Level, f.Smallest, f.Largest, f.Size, errCategory(realErr))

		case choice < 70:
			// 选取计划并对照。
			rp, rerr := real.Pick()
			mid, mp, merr := model.pick()
			if rerr != nil || merr != nil {
				t.Fatalf("op %d Pick: real err=%v model err=%v", op, rerr, merr)
			}
			if (rp == nil) != (mp == nil) {
				t.Fatalf("op %d Pick: real=%v model=%v (nil mismatch)", op, rp, mp)
			}
			if rp == nil {
				logf("op %d Pick -> empty (no level at score >= 1 without conflict)", op)
				continue
			}
			if rp.ID != mid || rp.Level != mp.level || rp.TargetLevel != mp.target || rp.Kind != mp.kind {
				t.Fatalf("op %d Pick: real=%v model=%+v", op, rp, mp)
			}
			if err := eqIDs(sortedMetas(rp.Inputs), mp.inputs...); err != nil {
				t.Fatalf("op %d Pick inputs: %v (real %v)", op, err, rp)
			}
			if err := eqIDs(sortedMetas(rp.NextInputs), mp.next...); err != nil {
				t.Fatalf("op %d Pick next-inputs: %v (real %v)", op, err, rp)
			}
			logf("op %d Pick -> plan#%d L%d->L%d %s inputs=%v next=%v score=%s | %s",
				op, rp.ID, rp.Level, rp.TargetLevel, rp.Kind,
				fileIDs(rp.Inputs), fileIDs(rp.NextInputs), rp.Score, rp.Reason)
			pending = append(pending, pendingPair{realID: rp.ID, plan: mp, real: rp})

		case choice < 90:
			// 安装一个随机在途计划。
			idx := rng.Intn(len(pending))
			pp := pending[idx]
			var outputs []FileMeta
			in := inputInterval(pp.real.allInputs())
			if rng.Intn(100) < 80 {
				// 合法输出：直接下移计划半数概率同编号改挂，否则合并区间单文件。
				if pp.real.Kind == PlanMove && rng.Intn(2) == 0 {
					src := pp.real.Inputs[0]
					outputs = []FileMeta{{ID: src.ID, Level: pp.real.TargetLevel, Smallest: src.Smallest, Largest: src.Largest, Size: src.Size}}
				} else {
					outputs = []FileMeta{{ID: nextFileID, Level: pp.real.TargetLevel, Smallest: in.lo, Largest: in.hi, Size: 1}}
					nextFileID++
				}
			} else {
				// 非法输出：落错层，或放一个几乎必然重叠的超大区间。
				if rng.Intn(2) == 0 {
					outputs = []FileMeta{{ID: nextFileID, Level: (pp.real.TargetLevel + 1) % cfg.NumLevels, Smallest: in.lo, Largest: in.hi, Size: 1}}
				} else {
					outputs = []FileMeta{{ID: nextFileID, Level: pp.real.TargetLevel, Smallest: k(0), Largest: k(255), Size: 1}}
				}
				nextFileID++
			}
			realErr := real.Install(pp.realID, outputs)
			modelErr := model.install(pp.realID, outputs)
			if errCategory(realErr) != errCategory(modelErr) {
				t.Fatalf("op %d Install plan#%d outputs=%v: real=%s(%v) model=%s(%v)",
					op, pp.realID, fileIDs(outputs), errCategory(realErr), realErr, errCategory(modelErr), modelErr)
			}
			logf("op %d Install plan#%d outputs=%v -> %s", op, pp.realID, fileIDs(outputs), errCategory(realErr))
			if realErr == nil {
				pending = append(pending[:idx], pending[idx+1:]...)
			}

		default:
			// 取消一个随机在途计划。
			idx := rng.Intn(len(pending))
			pp := pending[idx]
			realErr := real.Cancel(pp.realID)
			modelErr := model.cancel(pp.realID)
			if errCategory(realErr) != errCategory(modelErr) {
				t.Fatalf("op %d Cancel plan#%d: real=%s model=%s", op, pp.realID, errCategory(realErr), errCategory(modelErr))
			}
			logf("op %d Cancel plan#%d -> %s", op, pp.realID, errCategory(realErr))
			if realErr == nil {
				pending = append(pending[:idx], pending[idx+1:]...)
			}
		}
	}
	return logs
}

// TestModelRandomSequence 正式实现与朴素模型对照随机操作序列，
// 并验证相同操作序列重放得到完全相同的计划（确定性）。
func TestModelRandomSequence(t *testing.T) {
	const seed = 20261006
	logs1 := runSequence(t, seed, 400)
	// 重放：相同种子产生相同操作序列，结果必须完全一致。
	logs2 := runSequence(t, seed, 400)
	if len(logs1) != len(logs2) {
		t.Fatalf("replay produced %d logs, want %d", len(logs2), len(logs1))
	}
	for i := range logs1 {
		if logs1[i] != logs2[i] {
			t.Fatalf("replay diverged at op log %d:\n  first:  %s\n  second: %s", i, logs1[i], logs2[i])
		}
	}
}

// TestModelRandomSequenceMultiSeed 多种子扫描，扩大对照覆盖面。
func TestModelRandomSequenceMultiSeed(t *testing.T) {
	for seed := int64(1); seed <= 8; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runSequence(t, seed, 250)
		})
	}
}
