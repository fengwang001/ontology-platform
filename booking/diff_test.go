package booking

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func errName(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

// genCases 生成一组单调时钟的随机操作：2~4 个槽、4~10 个患者，
// 时刻围绕 start-R/start-C/start-E/start+G 等边界抖动。
func genCases(rng *rand.Rand) []modelOp {
	const ns = 3
	type slotCfg struct {
		name      string
		start     int64
		capV, onV int
	}
	var cfgs []slotCfg
	names := []string{"s1", "s2", "s3"}
	base := int64(1000)
	for i := 0; i < ns; i++ {
		capV := 1 + rng.Intn(4)
		cfgs = append(cfgs, slotCfg{
			name:  names[i],
			start: base + int64(i)*2000,
			capV:  capV,
			onV:   rng.Intn(capV + 1),
		})
	}
	np := 4 + rng.Intn(6)
	patients := make([]string, np)
	for i := range patients {
		patients[i] = fmt.Sprintf("p%d", i)
	}
	var ops []modelOp
	for _, c := range cfgs {
		ops = append(ops, modelOp{
			kind: "addslot", now: c.start - 900, slot: c.name,
			start: c.start, cap: c.capV, on: c.onV,
		})
	}
	now := int64(0)
	nsteps := 60 + rng.Intn(120)
	for i := 0; i < nsteps; i++ {
		c := cfgs[rng.Intn(len(cfgs))]
		p := patients[rng.Intn(len(patients))]
		// 时刻围绕关键边界：start-R, start-C, start-E, start, start+G。
		bounds := []int64{c.start - 60, c.start - 120, c.start - 30,
			c.start, c.start + 10, c.start - 500, c.start + 200}
		now = bounds[rng.Intn(len(bounds))] + int64(rng.Intn(3)-1)
		ch := Online
		if rng.Intn(2) == 0 {
			ch = Onsite
		}
		kind := []string{"book", "book", "checkin", "cancel", "joinwait"}[rng.Intn(5)]
		ops = append(ops, modelOp{kind: kind, now: now, p: p, slot: c.name, ch: ch})
	}
	// 排序不做：允许同刻乱序，只要不回退即可；生成器时刻无序，
	// 这里显式按稳定序排列以满足单调时钟（同刻保持生成序）。
	stablesort(ops)
	return ops
}

func stablesort(ops []modelOp) {
	// 插入排序，规模小且保持同刻顺序。
	for i := 1; i < len(ops); i++ {
		for j := i; j > 0 && ops[j-1].now > ops[j].now; j-- {
			ops[j-1], ops[j] = ops[j], ops[j-1]
		}
	}
}

func applyReal(b *Booking, op modelOp) (int64, string) {
	switch op.kind {
	case "addslot":
		return 0, errName(b.AddSlot(op.now, op.slot, op.start, op.cap, op.on))
	case "book":
		id, err := b.Book(op.now, []byte(op.p), op.slot, op.ch)
		return id, errName(err)
	case "checkin":
		return 0, errName(b.CheckIn(op.now, []byte(op.p), op.slot))
	case "cancel":
		return 0, errName(b.Cancel(op.now, []byte(op.p), op.slot))
	case "joinwait":
		return 0, errName(b.JoinWait(op.now, []byte(op.p), op.slot, op.ch))
	}
	return 0, "unknown"
}

func realDigest(b *Booking) string {
	var sb strings.Builder
	for _, sn := range []string{"s1", "s2", "s3"} {
		start, capV, on, uo, us, ok := b.pool.Get(sn)
		if !ok {
			continue
		}
		b.mu.Lock()
		w := len(b.slots[sn].wait)
		b.mu.Unlock()
		fmt.Fprintf(&sb, "%s{uo=%d,us=%d,w=%d};", sn, uo, us, w)
		_ = start
		_ = capV
		_ = on
	}
	b.mu.Lock()
	ps := make([]string, 0)
	for _, pi := range []string{"p0", "p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9"} {
		if rec := b.cred.Records([]byte(pi)); len(rec) > 0 {
			ps = append(ps, fmt.Sprintf("%s%v", pi, rec))
		}
	}
	sortStrings(ps)
	fmt.Fprintf(&sb, "rec=%v,next=%d,now=%d", ps, b.nextID, b.now)
	b.mu.Unlock()
	return sb.String()
}

func sortStrings(x []string) {
	for i := 1; i < len(x); i++ {
		for j := i; j > 0 && x[j-1] > x[j]; j-- {
			x[j-1], x[j] = x[j], x[j-1]
		}
	}
}

func opString(op modelOp) string {
	if op.kind == "addslot" {
		return fmt.Sprintf("%s(now=%d slot=%s start=%d cap=%d on=%d)",
			op.kind, op.now, op.slot, op.start, op.cap, op.on)
	}
	return fmt.Sprintf("%s(now=%d p=%s slot=%s ch=%d)",
		op.kind, op.now, op.p, op.slot, op.ch)
}

func TestDifferentialAgainstNaive(t *testing.T) {
	if testing.Verbose() {
		t.Logf("朴素模拟参数 R=60 E=30 G=10 C=120 K=2 W=10000")
	}
	for g := 0; g < 1500; g++ {
		rng := rand.New(rand.NewSource(int64(g + 1)))
		ops := genCases(rng)
		real := New(60, 30, 10, 120, 2, 10000)
		na := newNaive(60, 30, 10, 120, 2, 10000)
		var log []string
		for i, op := range ops {
			rid, rerr := applyReal(real, op)
			nid, nerr := na.run(op)
			why := ""
			if rerr != "ok" {
				why = "拒绝依据=" + rerr
			} else {
				why = fmt.Sprintf("接受 id=%d", rid)
			}
			line := fmt.Sprintf("[g%d #%d] in=%s out=(id=%d,%s) 朴素=(id=%d,%s) %s",
				g, i, opString(op), rid, rerr, nid, nerr, why)
			if rid != nid || rerr != nerr || realDigest(real) != na.digest() {
				log = append(log, line)
				t.Fatalf("第 %d 组第 %d 步不一致\n输入: %s\n真实: id=%d err=%s\n朴素: id=%d err=%s\n真实状态: %s\n朴素状态: %s\n日志:\n%s",
					g, i, opString(op), rid, rerr, nid, nerr,
					realDigest(real), na.digest(), strings.Join(log, "\n"))
			}
			log = append(log, line)
		}
		if testing.Verbose() && g < 3 {
			t.Logf("---- 第 %d 组（输入/输出/判定依据）----\n%s", g, strings.Join(log, "\n"))
		}
	}
}
