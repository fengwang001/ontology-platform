package shares_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"ontology/shares"
)

// op 是一条随机生成的操作记录，用于日志打印与重放。
type op struct {
	kind    string
	id      string
	weight  int64
	healthy bool
	now     int64
}

func (o op) String() string {
	switch o.kind {
	case "AddHost", "SetWeight":
		return fmt.Sprintf("%s(%q, %d, %d)", o.kind, o.id, o.weight, o.now)
	case "SetHealth":
		return fmt.Sprintf("%s(%q, %t, %d)", o.kind, o.id, o.healthy, o.now)
	default:
		return fmt.Sprintf("%s(%q, %d)", o.kind, o.id, o.now)
	}
}

func applyOp(c *shares.Calculator, m *naiveModel, o op) (gotShares []shares.Share, gotErr, wantErr error, trace string) {
	switch o.kind {
	case "AddHost":
		return nil, c.AddHost(o.id, o.weight, o.now), m.addHost(o.id, o.weight, o.now), ""
	case "SetWeight":
		return nil, c.SetWeight(o.id, o.weight, o.now), m.setWeight(o.id, o.weight, o.now), ""
	case "SetHealth":
		return nil, c.SetHealth(o.id, o.healthy, o.now), m.setHealth(o.id, o.healthy, o.now), ""
	case "RemoveHost":
		return nil, c.RemoveHost(o.id, o.now), m.removeHost(o.id, o.now), ""
	default: // Shares
		want, tr, werr := m.shares(o.now)
		got, gerr := c.Shares(o.now)
		if werr == nil && !reflect.DeepEqual(got, want) {
			return got, fmt.Errorf("份额不一致: got=%v want=%v", got, want), nil, tr
		}
		return got, gerr, werr, tr
	}
}

// TestRandomSequencesAgainstNaive 生成 2000 组随机操作序列，
// 将被测实现与朴素参照实现逐步对照，日志打印输入、输出与判定依据。
func TestRandomSequencesAgainstNaive(t *testing.T) {
	const sequences = 2000
	ids := []string{"h0", "h1", "h2", "h3", "h4", "h5", "h6", "h7"}
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)))
		wSlow := int64(1 + rng.Intn(1000))
		total := int64(1 + rng.Intn(300))
		capPer := int64(1 + rng.Intn(int(total)))
		c, err := shares.NewCalculator(wSlow, total, capPer)
		if err != nil {
			t.Fatalf("seq=%d 构造失败: %v", seq, err)
		}
		m := newNaive(wSlow, total, capPer)

		nOps := 1 + rng.Intn(50)
		ops := make([]op, 0, nOps)
		now := int64(0)
		for i := 0; i < nOps; i++ {
			// 时间多数单调前进，偶尔回退或取非法值以覆盖拒绝路径。
			switch r := rng.Intn(100); {
			case r < 80:
				now += int64(rng.Intn(11))
			case r < 90:
				now = int64(rng.Intn(200))
			case r < 95:
				now = -1 - int64(rng.Intn(10))
			default:
				now = 1_000_000_000_000_000 + int64(rng.Intn(3))
			}
			id := ids[rng.Intn(len(ids))]
			if rng.Intn(50) == 0 {
				id = "" // 覆盖参数非法
			}
			weight := int64(1 + rng.Intn(50))
			switch rng.Intn(50) {
			case 0:
				weight = 0
			case 1:
				weight = 1_000_001
			}
			o := op{id: id, weight: weight, healthy: rng.Intn(2) == 0, now: now}
			switch rng.Intn(8) {
			case 0, 1:
				o.kind = "AddHost"
			case 2:
				o.kind = "SetWeight"
			case 3, 4:
				o.kind = "SetHealth"
			case 5:
				o.kind = "RemoveHost"
			default:
				o.kind = "Shares"
			}
			ops = append(ops, o)
		}

		var log strings.Builder
		fmt.Fprintf(&log, "seq=%d 配置 Wslow=%d T=%d Y=%d\n", seq, wSlow, total, capPer)
		failed := false
		for i, o := range ops {
			gotShares, gotErr, wantErr, trace := applyOp(c, m, o)
			mismatch := false
			if (gotErr == nil) != (wantErr == nil) {
				mismatch = true
			} else if gotErr != nil && !errors.Is(gotErr, wantErr) {
				mismatch = true
			}
			fmt.Fprintf(&log, "  op%d %s -> gotErr=%v wantErr=%v\n", i, o, gotErr, wantErr)
			if o.kind == "Shares" {
				fmt.Fprintf(&log, "    输出=%v\n    判定依据: %s\n", gotShares, trace)
				if gotErr == nil {
					checkSharesInvariants(t, gotShares, total, capPer)
				}
			}
			if mismatch {
				t.Errorf("seq=%d op%d %s 不一致:\n%s", seq, i, o, log.String())
				failed = true
				break
			}
		}
		if !failed {
			t.Logf("随机序列回放一致:\n%s", log.String())
		}
	}
}

// checkSharesInvariants 校验成功 Shares 的不变量：各项非负、
// 总和恒为 T、每项不超过 Y。
func checkSharesInvariants(t *testing.T, got []shares.Share, total, capPer int64) {
	t.Helper()
	var sum int64
	for _, s := range got {
		if s.Value < 0 {
			t.Errorf("份额为负: %v", got)
			return
		}
		if s.Value > capPer {
			t.Errorf("份额超过上限 Y=%d: %v", capPer, got)
			return
		}
		sum += s.Value
	}
	if sum != total {
		t.Errorf("份额总和 = %d, 期望 %d: %v", sum, total, got)
	}
}

// TestDeterministicReplay 相同操作序列重放得到完全相同的份额序列。
func TestDeterministicReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	build := func() *shares.Calculator {
		c, err := shares.NewCalculator(100, 500, 60)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	var ops []op
	now := int64(0)
	for i := 0; i < 200; i++ {
		now += int64(rng.Intn(5))
		o := op{
			id:      fmt.Sprintf("h%d", rng.Intn(6)),
			weight:  int64(1 + rng.Intn(20)),
			healthy: rng.Intn(2) == 0,
			now:     now,
		}
		switch rng.Intn(6) {
		case 0:
			o.kind = "AddHost"
		case 1:
			o.kind = "SetWeight"
		case 2:
			o.kind = "SetHealth"
		case 3:
			o.kind = "RemoveHost"
		default:
			o.kind = "Shares"
		}
		ops = append(ops, o)
	}
	run := func() [][]shares.Share {
		c := build()
		var out [][]shares.Share
		for _, o := range ops {
			switch o.kind {
			case "AddHost":
				_ = c.AddHost(o.id, o.weight, o.now)
			case "SetWeight":
				_ = c.SetWeight(o.id, o.weight, o.now)
			case "SetHealth":
				_ = c.SetHealth(o.id, o.healthy, o.now)
			case "RemoveHost":
				_ = c.RemoveHost(o.id, o.now)
			default:
				s, _ := c.Shares(o.now)
				out = append(out, s)
			}
		}
		return out
	}
	first := run()
	for replay := 0; replay < 3; replay++ {
		if got := run(); !reflect.DeepEqual(got, first) {
			t.Fatalf("第 %d 次重放结果不一致", replay)
		}
	}
}
