package transfer_test

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"ontology/internal/naive"
	"ontology/internal/transfer"
)

type opKind int

const (
	opCreate opKind = iota
	opCancel
	opShip
	opReceive
	opClose
	opRecover
)

type op struct {
	kind   opKind
	id     string
	item   string
	qty    int64
	at     int64
	lines  []transfer.Line
	nlines []naive.Line
	src    string
	dst    string
}

func errClass(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

func codeName(c transfer.ErrorCode) string {
	switch c {
	case transfer.ErrInvalidArgument:
		return "invalid_argument"
	case transfer.ErrClockRollback:
		return "clock_rollback"
	case transfer.ErrTransferNotFound:
		return "not_found"
	case transfer.ErrInvalidState:
		return "invalid_state"
	case transfer.ErrInsufficientStock:
		return "insufficient_stock"
	case transfer.ErrOverReceipt:
		return "over_receipt"
	case transfer.ErrCloseTooEarly:
		return "close_too_early"
	case transfer.ErrRecoveryExceed:
		return "recovery_exceed"
	case transfer.ErrNoShortage:
		return "no_shortage"
	}
	return "?"
}

func realClass(err error) (string, transfer.Status) {
	if err == nil {
		return "ok", 0
	}
	if e, ok := err.(*transfer.Error); ok {
		return codeName(e.Code), e.State
	}
	return err.Error(), 0
}

func naiveClass(err error) (string, naive.Status) {
	if err == nil {
		return "ok", 0
	}
	if e, ok := err.(*naive.Err); ok {
		return string(e.Code), e.State
	}
	return err.Error(), 0
}

// TestRandomDifferential 用大量随机操作序列对照生产实现与朴素模型：
// 每次操作的错误类别、状态、各仓库存、单据行、守恒必须完全一致。
func TestRandomDifferential(t *testing.T) {
	const runs = 400
	for seed := int64(0); seed < runs; seed++ {
		rng := rand.New(rand.NewSource(seed))
		runOne(t, rng, seed)
	}
}

func runOne(t *testing.T, rng *rand.Rand, seed int64) {
	t.Helper()

	warehouses := []string{"WH1", "WH2", "WH3"}
	items := []string{"A", "B", "C"}
	permille := rng.Int63n(300)
	wait := int64(1 + rng.Intn(50))

	initial := map[string]map[string]int64{}
	for _, wh := range warehouses {
		initial[wh] = map[string]int64{}
		for _, it := range items {
			initial[wh][it] = int64(rng.Intn(60))
		}
	}
	svc := transfer.NewService(transfer.Config{TolerancePermille: permille, CloseWaitSeconds: wait}, initial)
	model := naive.New(permille, wait, initial)

	created := map[string]bool{}
	clock := int64(0)
	logf := func(format string, args ...any) {
		if testing.Verbose() || seed%100 == 99 {
			t.Logf("[seed=%d] %s", seed, fmt.Sprintf(format, args...))
		}
	}

	nOps := 80 + rng.Intn(120)
	for step := 0; step < nOps; step++ {
		o := op{at: clock}
		// 时刻：经常前进，偶尔回退以触发时钟错误。
		if rng.Intn(8) == 0 && clock > 0 {
			o.at = rng.Int63n(clock)
		} else {
			clock += int64(rng.Intn(10))
			o.at = clock
		}

		// 偏向创建新单；没有单时必须创建。
		canCreate := len(created) < 30
		if len(created) == 0 || (canCreate && rng.Intn(3) == 0) {
			o.kind = opCreate
			o.id = fmt.Sprintf("T%d-%d", seed, step)
			for {
				i, j := rng.Intn(3), rng.Intn(3)
				if i != j {
					o.src, o.dst = warehouses[i], warehouses[j]
					break
				}
			}
			nl := 1 + rng.Intn(3)
			perm := rng.Perm(3)[:nl]
			for _, pi := range perm {
				it := items[pi]
				q := int64(1 + rng.Intn(70))
				o.lines = append(o.lines, transfer.Line{Item: it, Qty: q})
				o.nlines = append(o.nlines, naive.Line{Item: it, Qty: q})
			}
			// 极小概率构造非法参数。
			if rng.Intn(20) == 0 {
				o.lines = append(o.lines, o.lines[0])
				o.nlines = append(o.nlines, o.nlines[0])
			}
			created[o.id] = true
		} else {
			ids := make([]string, 0, len(created))
			for id := range created {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			o.id = ids[rng.Intn(len(ids))]
			o.item = items[rng.Intn(3)]
			o.qty = int64(1 + rng.Intn(50))
			o.kind = opKind(1 + rng.Intn(5))
		}

		var rerr, merr error
		switch o.kind {
		case opCreate:
			rerr = svc.Create(o.id, o.src, o.dst, o.lines, o.at)
			merr = model.Create(o.id, o.src, o.dst, o.nlines, o.at)
		case opCancel:
			rerr = svc.Cancel(o.id, o.at)
			merr = model.Cancel(o.id, o.at)
		case opShip:
			rerr = svc.Ship(o.id, o.at)
			merr = model.Ship(o.id, o.at)
		case opReceive:
			rerr = svc.Receive(o.id, o.item, o.qty, o.at)
			merr = model.Receive(o.id, o.item, o.qty, o.at)
		case opClose:
			rerr = svc.Close(o.id, o.at)
			merr = model.Close(o.id, o.at)
		case opRecover:
			rerr = svc.Recover(o.id, o.item, o.qty, o.at)
			merr = model.Recover(o.id, o.item, o.qty, o.at)
		}

		rc, rs := realClass(rerr)
		mc, ms := naiveClass(merr)
		logf("step=%d kind=%d id=%s item=%s qty=%d at=%d -> real=%s(state=%d) naive=%s(state=%d)",
			step, o.kind, o.id, o.item, o.qty, o.at, rc, rs, mc, ms,
		)
		if rc != mc || int(rs) != int(ms) {
			t.Fatalf("seed=%d step=%d divergence: real=%s(%d) naive=%s(%d)",
				seed, step, rc, rs, mc, ms)
		}
		if rerr == nil {
			clock = o.at
		}

		assertSnapshotsEqual(t, svc, model, warehouses, items, created, seed, step)
	}
}

func assertSnapshotsEqual(
	t *testing.T,
	svc *transfer.Service,
	model *naive.Model,
	warehouses, items []string,
	created map[string]bool,
	seed int64, step int,
) {
	t.Helper()
	for _, wh := range warehouses {
		for _, it := range items {
			ra, rf := svc.Stock(wh, it)
			ma, mf := model.Stock(wh, it)
			if ra != ma || rf != mf {
				t.Fatalf("seed=%d step=%d stock %s/%s real=(%d,%d) naive=(%d,%d)",
					seed, step, wh, it, ra, rf, ma, mf)
			}
			if ra < 0 || rf < 0 {
				t.Fatalf("negative stock seed=%d step=%d %s/%s=(%d,%d)", seed, step, wh, it, ra, rf)
			}
		}
	}
	ids := make([]string, 0, len(created))
	for id := range created {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		rl, rerr := svc.OrderLines(id)
		ml, mok := model.OrderLines(id)
		rok := rerr == nil
		if rok != mok {
			t.Fatalf("seed=%d step=%d existence %s real=%v naive=%v", seed, step, id, rok, mok)
		}
		if !rok || !mok {
			continue
		}
		rs, _ := svc.OrderStatus(id)
		ms, _ := model.OrderStatus(id)
		if int(rs) != int(ms) {
			t.Fatalf("seed=%d step=%d status %s real=%d naive=%d", seed, step, id, rs, ms)
		}
		rm := map[string]transfer.LineState{}
		for _, ln := range rl {
			rm[ln.Item] = ln
		}
		for _, ln := range ml {
			g, ok := rm[ln.Item]
			if !ok || g.Issued != ln.Issued || g.Received != ln.Received ||
				g.Shortage != ln.Shortage || g.Overage != ln.Overage || g.Requested != ln.Requested {
				t.Fatalf("seed=%d step=%d line mismatch %s/%s real=%+v naive=%+v",
					seed, step, id, ln.Item, g, ln)
			}
		}
	}
	for _, it := range items {
		if !svc.VerifyItem(it) {
			t.Fatalf("seed=%d step=%d conservation broken item=%s", seed, step, it)
		}
		if !model.VerifyItem(it) {
			t.Fatalf("seed=%d step=%d naive conservation broken item=%s", seed, step, it)
		}
	}
}
