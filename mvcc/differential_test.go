package mvcc

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// op 是一条可重放的操作记录。
type op struct {
	kind  string
	args  []int
	tuple string
}

func (o op) String() string {
	if o.tuple != "" {
		return fmt.Sprintf("%s(%s, %v)", o.kind, o.tuple, o.args)
	}
	return fmt.Sprintf("%s%v", o.kind, o.args)
}

// genOps 生成一条随机操作序列。小号空间保证成功与各类拒绝都频繁出现。
func genOps(r *rand.Rand, n int) []op {
	kinds := []string{
		"Begin", "Begin", "BeginSub", "BeginSub",
		"CommitSub", "Commit", "Abort", "Snapshot",
		"Insert", "Insert", "Delete", "Delete", "Visible", "Visible",
	}
	ops := make([]op, 0, n)
	for i := 0; i < n; i++ {
		kind := kinds[r.Intn(len(kinds))]
		txID := func() int { return r.Intn(14) - 1 } // 含 0、负值与未知号
		cmd := func() int { return r.Intn(8) - 2 }   // 含负命令号
		tuple := fmt.Sprintf("t%d", r.Intn(6))
		switch kind {
		case "Begin":
			ops = append(ops, op{kind, []int{txID()}, ""})
		case "BeginSub":
			ops = append(ops, op{kind, []int{txID(), txID()}, ""})
		case "CommitSub", "Commit", "Abort":
			ops = append(ops, op{kind, []int{txID()}, ""})
		case "Snapshot":
			ops = append(ops, op{kind, nil, ""})
		case "Insert", "Delete":
			ops = append(ops, op{kind, []int{txID(), cmd()}, tuple})
		case "Visible":
			ops = append(ops, op{kind, []int{txID(), cmd(), r.Intn(8)}, tuple})
		}
	}
	return ops
}

// runOnEngine 在 Engine 上重放一条操作并返回结果描述。
func runOnEngine(e *Engine, o op) (string, error) {
	switch o.kind {
	case "Begin":
		return "", e.Begin(o.args[0])
	case "BeginSub":
		return "", e.BeginSub(o.args[0], o.args[1])
	case "CommitSub":
		return "", e.CommitSub(o.args[0])
	case "Commit":
		return "", e.Commit(o.args[0])
	case "Abort":
		return "", e.Abort(o.args[0])
	case "Snapshot":
		return fmt.Sprintf("快照号=%d", e.Snapshot()), nil
	case "Insert":
		return "", e.Insert(o.tuple, o.args[0], o.args[1])
	case "Delete":
		return "", e.Delete(o.tuple, o.args[0], o.args[1])
	case "Visible":
		vis, err := e.Visible(o.tuple, o.args[0], o.args[1], o.args[2])
		return fmt.Sprintf("可见=%v", vis), err
	}
	panic("未知操作: " + o.kind)
}

// runOnNaive 在朴素实现上重放同一操作，返回结果描述与判定依据。
func runOnNaive(n *naive, o op) (string, error, string) {
	switch o.kind {
	case "Begin":
		return "", n.Begin(o.args[0]), ""
	case "BeginSub":
		return "", n.BeginSub(o.args[0], o.args[1]), ""
	case "CommitSub":
		return "", n.CommitSub(o.args[0]), ""
	case "Commit":
		return "", n.Commit(o.args[0]), ""
	case "Abort":
		return "", n.Abort(o.args[0]), ""
	case "Snapshot":
		return fmt.Sprintf("快照号=%d", n.Snapshot()), nil, ""
	case "Insert":
		return "", n.Insert(o.tuple, o.args[0], o.args[1]), ""
	case "Delete":
		return "", n.Delete(o.tuple, o.args[0], o.args[1]), ""
	case "Visible":
		vis, err, basis := n.Visible(o.tuple, o.args[0], o.args[1], o.args[2])
		return fmt.Sprintf("可见=%v", vis), err, basis
	}
	panic("未知操作: " + o.kind)
}

// TestDifferential 对 2000 组随机操作序列做 Engine 与朴素实现的对拍，
// 日志逐条打印输入、输出与判定依据。
func TestDifferential(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		r := rand.New(rand.NewSource(int64(seq) + 1))
		ops := genOps(r, 30+r.Intn(20))
		e := New()
		n := newNaive()
		for i, o := range ops {
			gotOut, gotErr := runOnEngine(e, o)
			wantOut, wantErr, basis := runOnNaive(n, o)
			t.Logf("seq=%d step=%d 输入=%s | Engine 输出=%s 错误=%v | 朴素 输出=%s 错误=%v | 判定依据: %s",
				seq, i, o, gotOut, gotErr, wantOut, wantErr, basis)
			if gotOut != wantOut || !errors.Is(gotErr, wantErr) || (gotErr == nil) != (wantErr == nil) {
				t.Fatalf("seq=%d step=%d 输入=%s 不一致: Engine(%s, %v) vs 朴素(%s, %v)",
					seq, i, o, gotOut, gotErr, wantOut, wantErr)
			}
		}
	}
}
