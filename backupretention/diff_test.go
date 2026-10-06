package backupretention

import (
	"bytes"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// rngOp 是差分测试中的一条抽象操作。
type rngOp struct {
	name   string
	id     string
	parent string
	size   int64
	ts     int64
	on     bool
	policy Policy
}

func genOps(r *rand.Rand, count int) []rngOp {
	var ops []rngOp
	var ts int64 = 1_700_000_000 // 固定起点，保证可重放
	nextSeq := 0
	newID := func() string {
		nextSeq++
		id := fmt.Sprintf("b%04d", nextSeq)
		return id
	}

	for i := 0; i < count; i++ {
		ts += int64(r.Intn(5)) // 时间单调不减（偶有相等）
		roll := r.Intn(100)
		if r.Intn(12) == 0 {
			// 约 8% 概率制造时钟回退/非法时刻，验证拒绝无副作用。
			bad := ts - int64(1+r.Intn(10))
			switch r.Intn(4) {
			case 0:
				ops = append(ops, rngOp{name: "plan", ts: bad})
			case 1:
				ops = append(ops, rngOp{name: "policy", ts: bad,
					policy: Policy{}})
			case 2:
				ops = append(ops, rngOp{name: "corrupt", id: "__pick__", ts: bad})
			default:
				id := newID()
				ops = append(ops, rngOp{name: "full", id: id,
					size: int64(r.Intn(1000)), ts: bad})
			}
			continue
		}
		switch {
		case roll < 38:
			id := newID()
			ops = append(ops, rngOp{name: "full", id: id, size: int64(r.Intn(1000)), ts: ts})
		case roll < 70:
			id := newID()
			// 父 id 由执行器在回放时从“当前真实已登记集合”中选取，
			// 保证生成器不预设任何可能被拒绝的状态。
			parent := "__pick__"
			if r.Intn(20) == 0 {
				parent = "ghost"
			}
			ops = append(ops, rngOp{
				name: "incr", id: id, parent: parent,
				size: int64(r.Intn(1000)), ts: ts,
			})
		case roll < 78:
			ops = append(ops, rngOp{name: "corrupt", id: "__pick__", ts: ts})
		case roll < 86:
			ops = append(ops, rngOp{name: "legal", id: "__pick__", ts: ts, on: r.Intn(2) == 0})
		case roll < 92:
			p := Policy{
				Daily:   r.Intn(30),
				Weekly:  r.Intn(12),
				Monthly: r.Intn(8),
			}
			if r.Intn(20) == 0 {
				p.Daily = 1001 // 偶发越界
			}
			ops = append(ops, rngOp{
				name: "policy", ts: ts,
				policy: p,
			})
		case roll < 98:
			ops = append(ops, rngOp{name: "plan", ts: ts})
		default:
			ops = append(ops, rngOp{name: "cleanup", ts: ts})
		}
	}
	return ops
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func reasonKey(r RetentionReason) string {
	return layersString(r.DirectLayers) +
		"/dep=" + boolStr(r.Dependency) + "/leg=" + boolStr(r.LegalHold)
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func planKey(p *Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "now=%d kept{", p.Now)
	for _, x := range p.Retained {
		fmt.Fprintf(&b, "%s@%d:%s,", x.ID, x.CreatedAt, reasonKey(x.Reason))
	}
	b.WriteString("}del{")
	for _, x := range p.Deletable {
		fmt.Fprintf(&b, "%s@%d,", x.ID, x.CreatedAt)
	}
	b.WriteString("}")
	return b.String()
}

// applyNaive 在朴素模型上执行操作，返回错误类别与（可能的）计划。
func applyNaive(m *naiveModel, op rngOp) (ErrorKind, *Plan, []string) {
	switch op.name {
	case "full":
		return m.registerFull(op.id, op.size, op.ts), nil, nil
	case "incr":
		return m.registerIncr(op.id, op.size, op.ts, op.parent), nil, nil
	case "corrupt":
		return m.markCorrupt(op.id, op.ts), nil, nil
	case "legal":
		return m.setLegal(op.id, op.ts, op.on), nil, nil
	case "policy":
		return m.setPolicy(op.policy, op.ts), nil, nil
	case "plan":
		p, k := m.plan(op.ts)
		return k, p, nil
	case "cleanup":
		p, del, k := m.cleanup(op.ts)
		return k, p, del
	}
	return KindUnknown, nil, nil
}

// resolvePicks 把占位父/目标 id 解析为朴素模型中当前真实存在的备份。
// 增量无父可选时退化为全量登记（由第二个返回值给出退化后的操作）。
func resolvePicks(m *naiveModel, r *rand.Rand, op rngOp) rngOp {
	pick := func() string {
		ids := make([]string, 0, len(m.backups))
		for id := range m.backups {
			ids = append(ids, id)
		}
		if len(ids) == 0 {
			return ""
		}
		return ids[r.Intn(len(ids))]
	}
	switch op.name {
	case "corrupt", "legal":
		if op.id == "__pick__" {
			op.id = pick()
		}
	case "incr":
		if op.parent == "__pick__" {
			parent := pick()
			if parent == "" {
				op.name = "full"
				op.parent = ""
			} else {
				op.parent = parent
			}
		}
	}
	return op
}

// applyReal 在真实服务上执行操作。
func applyReal(s *Service, op rngOp) (ErrorKind, *Plan, []string) {
	var err error
	var p *Plan
	switch op.name {
	case "full":
		err = s.RegisterFull(op.id, op.size, op.ts)
	case "incr":
		err = s.RegisterIncremental(op.id, op.size, op.ts, op.parent)
	case "corrupt":
		err = s.MarkCorrupt(op.id, op.ts)
	case "legal":
		err = s.SetLegalHold(op.id, op.ts, op.on)
	case "policy":
		err = s.SetPolicy(op.policy, op.ts)
	case "plan":
		p, err = s.Plan(op.ts)
	case "cleanup":
		p, err = s.Cleanup(op.ts)
	}
	k := ErrorKindOf(err)
	if op.name == "cleanup" && p != nil {
		del := make([]string, 0, len(p.Deletable))
		for _, b := range p.Deletable {
			del = append(del, b.ID)
		}
		return k, p, del
	}
	return k, p, nil
}

// TestNaiveDifferential 用固定多个种子重放随机序列，逐步比对错误类别、
// 计划的保留/删除集合与每个备份的保留原因，并打印完整操作日志。
func TestNaiveDifferential(t *testing.T) {
	seeds := []int64{1, 2, 3, 7, 42, 100, 2024}
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			r := rand.New(rand.NewSource(seed))
			ops := genOps(r, 1500)

			var realLog, naiveLog bytes.Buffer
			s := NewService(WithLogger(&realLog))
			m := newNaiveModel()

			for idx, op := range ops {
				op = resolvePicks(m, r, op)
				rk, rp, rdel := applyReal(s, op)
				nk, np, ndel := applyNaive(m, op)

				// 朴素侧操作日志：输入 + 输出 + 判定依据。
				logNaiveOp(&naiveLog, idx, op, nk, np, ndel)

				if rk != nk {
					t.Fatalf("seed=%d op#%d %+v error kind real=%d naive=%d\nNAIVE LOG:\n%s",
						seed, idx, op, rk, nk, tailLog(&naiveLog, 30))
				}
				if rp != nil {
					if planKey(rp) != planKey(np) {
						t.Fatalf("seed=%d op#%d %+v plan mismatch\nREAL:\n%s\nNAIVE:\n%s\nNAIVE LOG:\n%s",
							seed, idx, op, planKey(rp), planKey(np), tailLog(&naiveLog, 40))
					}
				}
				if rdel != nil || ndel != nil {
					sort.Strings(rdel)
					if !equalStrings(rdel, ndel) {
						t.Fatalf("seed=%d op#%d cleanup delete set real=%v naive=%v",
							seed, idx, rdel, ndel)
					}
				}
			}

			// 成功路径上双方注册表最终一致。
			if s.reg.len() != len(m.backups) {
				t.Fatalf("final registry size real=%d naive=%d", s.reg.len(), len(m.backups))
			}
			for id := range m.backups {
				if _, ok := s.reg.get(id); !ok {
					t.Fatalf("final registry divergence: %q in naive only", id)
				}
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func tailLog(buf *bytes.Buffer, n int) string {
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func logNaiveOp(buf *bytes.Buffer, idx int, op rngOp, k ErrorKind, p *Plan, del []string) {
	switch op.name {
	case "full":
		fmt.Fprintf(buf, "#%d FULL id=%s size=%d ts=%d -> kind=%d\n", idx, op.id, op.size, op.ts, k)
	case "incr":
		fmt.Fprintf(buf, "#%d INCR id=%s parent=%s size=%d ts=%d -> kind=%d\n",
			idx, op.id, op.parent, op.size, op.ts, k)
	case "corrupt":
		fmt.Fprintf(buf, "#%d CORRUPT id=%s ts=%d -> kind=%d\n", idx, op.id, op.ts, k)
	case "legal":
		fmt.Fprintf(buf, "#%d LEGAL id=%s on=%t ts=%d -> kind=%d\n",
			idx, op.id, op.on, op.ts, k)
	case "policy":
		fmt.Fprintf(buf, "#%d POLICY d=%d w=%d m=%d ts=%d -> kind=%d\n",
			idx, op.policy.Daily, op.policy.Weekly, op.policy.Monthly, op.ts, k)
	case "plan", "cleanup":
		fmt.Fprintf(buf, "#%d %s ts=%d -> kind=%d %s",
			idx, strings.ToUpper(op.name), op.ts, k, safePlanKey(p))
		if op.name == "cleanup" {
			fmt.Fprintf(buf, " deleted=%v", del)
		}
		buf.WriteByte('\n')
	}
}

func safePlanKey(p *Plan) string {
	if p == nil {
		return "<no plan>"
	}
	return planKey(p)
}

// TestDifferentialLogIsPrinted 让 -v 时可直观看到每条操作的输入/输出/依据。
func TestDifferentialLogIsPrinted(t *testing.T) {
	r := rand.New(rand.NewSource(123))
	ops := genOps(r, 40)
	m := newNaiveModel()
	var buf bytes.Buffer
	for i, op := range ops {
		k, p, del := applyNaive(m, op)
		logNaiveOp(&buf, i, op, k, p, del)
	}
	t.Log("\n" + buf.String())
	if !strings.Contains(buf.String(), "#0 ") {
		t.Fatal("differential log should contain operations")
	}
}
