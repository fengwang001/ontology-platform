package billing

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

const (
	diffSequences = 1200
	diffSteps     = 300
)

// genSample 按 rng 生成采样；allowInvalid 为 true 时按一定概率制造非法输入，
// 以覆盖参数校验路径。
func genSample(rng *rand.Rand, n int, allowInvalid bool) Sample {
	s := Sample{
		Slot:    rng.Intn(n),
		Ingress: rng.Int63n(MaxRate + 1),
		Egress:  rng.Int63n(MaxRate + 1),
		Version: rng.Int63n(20) + 1,
	}
	if allowInvalid {
		switch rng.Intn(12) {
		case 0:
			s.Slot = -1
		case 1:
			s.Slot = n
		case 2:
			s.Ingress = -1
		case 3:
			s.Ingress = MaxRate + 1
		case 4:
			s.Egress = -1
		case 5:
			s.Egress = MaxRate + 1
		case 6:
			s.Version = 0
		case 7:
			s.Version = -3
		}
	}
	return s
}

// runOneSequence 对同一操作序列分别驱动真实实现与朴素模型，逐步比对。
func runOneSequence(t *testing.T, seq int, seed int64) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	n := 1 + rng.Intn(40)
	real := NewSettler(int64(seed%1_000_000), n)
	model := NewNaiveSettler(n)
	var log traceLogger
	log.line("===== 序列 %d 种子 %d N=%d 步数=%d =====", seq, seed, n, diffSteps)

	for step := 1; step <= diffSteps; step++ {
		op := rng.Intn(100)
		switch {
		case op < 56: // 提交采样
			sample := genSample(rng, n, true)
			errReal := real.Submit(sample)
			errModel := model.Submit(sample)
			log.line("[%03d] submit slot=%d in=%d out=%d ver=%d -> real=%s model=%s | 判定: 按版本与内容规则接受/幂等/冲突/过期，或参数/结算拒绝",
				step, sample.Slot, sample.Ingress, sample.Egress, sample.Version,
				errSig(errReal), errSig(errModel))
			if errSig(errReal) != errSig(errModel) {
				t.Fatalf("seq=%d seed=%d step=%d submit 分歧: real=%s model=%s sample=%+v\n%s",
					seq, seed, step, errSig(errReal), errSig(errModel), sample, log.String())
			}
		case op < 72: // 撤回
			slot := rng.Intn(n)
			version := rng.Int63n(22) - 1 // 偶尔产生 0/负数非法版本
			errReal := real.Withdraw(slot, version)
			errModel := model.Withdraw(slot, version)
			log.line("[%03d] withdraw slot=%d ver=%d -> real=%s model=%s | 判定: 版本相等才生效，缺失报不存在",
				step, slot, version, errSig(errReal), errSig(errModel))
			if errSig(errReal) != errSig(errModel) {
				t.Fatalf("seq=%d seed=%d step=%d withdraw 分歧: real=%s model=%s slot=%d ver=%d\n%s",
					seq, seed, step, errSig(errReal), errSig(errModel), slot, version, log.String())
			}
		case op < 86: // 查询当前费率
			rateReal, errReal := real.CurrentRate()
			rateModel, errModel := model.Rate()
			log.line("[%03d] rate -> real=(%d,%s) model=(%d,%s) | 判定: floor(5K/100) 个最大值丢弃后取剩余最大",
				step, rateReal, errSig(errReal), rateModel, errSig(errModel))
			if errSig(errReal) != errSig(errModel) || (errReal == nil && rateReal != rateModel) {
				t.Fatalf("seq=%d seed=%d step=%d rate 分歧\n%s", seq, seed, step, log.String())
			}
		case op < 96: // 概览
			oReal := real.Overview()
			oModel := model.Overview()
			log.line("[%03d] overview -> real={k=%d missing=%d rate=%d defined=%t settled=%t} model={k=%d missing=%d rate=%d defined=%t settled=%t} | 判定: 与即时查询一致",
				step,
				oReal.ReceivedSlots, oReal.MissingSlots, oReal.BilledRate, oReal.RateDefined, oReal.Settled,
				oModel.ReceivedSlots, oModel.MissingSlots, oModel.BilledRate, oModel.RateDefined, oModel.Settled)
			if oReal != oModel {
				t.Fatalf("seq=%d seed=%d step=%d overview 分歧: real=%+v model=%+v\n%s",
					seq, seed, step, oReal, oModel, log.String())
			}
		default: // 结算
			committed := rng.Int63n(MaxRate + 1)
			tol := rng.Intn(MaxBasisPt + 1)
			if rng.Intn(10) == 0 {
				if rng.Intn(2) == 0 {
					committed = -1
				} else {
					tol = MaxBasisPt + 1
				}
			}
			rReal, errReal := real.Settle(committed, tol)
			rModel, errModel := model.Settle(committed, tol)
			log.line("[%03d] settle committed=%d tol=%d -> real=(%+v,%s) model=(%+v,%s) | 判定: 缺失比例超限报数据不足，成功后封存并回放首次结果",
				step, committed, tol, rReal, errSig(errReal), rModel, errSig(errModel))
			if errSig(errReal) != errSig(errModel) {
				t.Fatalf("seq=%d seed=%d step=%d settle 错误分歧: %s vs %s\n%s",
					seq, seed, step, errSig(errReal), errSig(errModel), log.String())
			}
			if errReal == nil {
				if d := cmpResult(rReal, rModel); d != "" {
					t.Fatalf("seq=%d seed=%d step=%d settle 结果分歧: %s\n%s", seq, seed, step, d, log.String())
				}
			}
		}
	}

	// 序列终态再全面比对一次。
	if oReal, oModel := real.Overview(), model.Overview(); oReal != oModel {
		t.Fatalf("seq=%d seed=%d 终态 overview 分歧: %+v vs %+v\n%s", seq, seed, oReal, oModel, log.String())
	}
	if d, m := dumpRealSlots(real, n), dumpModelSlots(model, n); d != m {
		t.Fatalf("seq=%d seed=%d 终态槽位分歧\nreal:\n%s\nmodel:\n%s", seq, seed, d, m)
	}
	t.Logf("序列 %d 完成（种子 %d, N=%d）：%d 步全部一致", seq, seed, n, diffSteps)
	traceOutputs[seq] = log.String()
}

func dumpRealSlots(s *Settler, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		hv, _ := s.HighestSeenVersion(i)
		out += fmt.Sprintf("slot=%d highest=%d\n", i, hv)
	}
	return out
}

func dumpModelSlots(m *NaiveSettler, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += fmt.Sprintf("slot=%d highest=%d\n", i, m.slots[i].highestSeen)
	}
	return out
}

var traceOutputs = map[int]string{}

// TestRandomDifferential 用 1200 组随机操作序列对照朴素模型，
// 每步输入/输出/判定依据写入 diff_trace.log（位于包测试目录）。
func TestRandomDifferential(t *testing.T) {
	baseSeed := int64(20261006)
	for seq := 0; seq < diffSequences; seq++ {
		runOneSequence(t, seq, baseSeed+int64(seq))
	}

	logPath := filepath.Join(".", "diff_trace.log")
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("创建轨迹日志失败: %v", err)
	}
	defer f.Close()
	for seq := 0; seq < diffSequences; seq++ {
		if _, err := f.WriteString(traceOutputs[seq]); err != nil {
			t.Fatalf("写轨迹日志失败: %v", err)
		}
	}
	t.Logf("已写入 %d 组序列完整轨迹到 %s", diffSequences, logPath)
}
