package kitchen_test

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"

	"ontology/kitchen"
)

const verboseDiff = false // 失败时始终打印；成功时仅在 KITCHEN_DIFF_VERBOSE=1 打印全部

type opKind int

const (
	opAdmitImm opKind = iota
	opAdmitRes
	opComplete
	opCancel
	opPause
	opResume
	opTick
)

type genOp struct {
	kind opKind
	id   int
	dur  int64
	pick int64
	at   int64
}

// genSequence 生成一条单调时间、语义尽量可执行的随机操作序列。
func genSequence(rng *rand.Rand, n int, horizon int64) []genOp {
	var ops []genOp
	t := int64(0)
	nextID := 1
	for i := 0; i < n; i++ {
		step := int64(rng.Intn(4))
		t += step
		if t > horizon {
			t = horizon
		}
		roll := rng.Intn(100)
		switch {
		case roll < 45:
			ops = append(ops, genOp{kind: opAdmitImm, id: nextID, dur: int64(1 + rng.Intn(12)), at: t})
			nextID++
		case roll < 70:
			dur := int64(1 + rng.Intn(10))
			// 目标开工覆盖“未来多远”：部分很近以触发预约过近。
			target := t + dur + int64(rng.Intn(20))
			ops = append(ops, genOp{kind: opAdmitRes, id: nextID, dur: dur, pick: target, at: t})
			nextID++
		case roll < 82:
			id := 1 + rng.Intn(max1(nextID-1))
			ops = append(ops, genOp{kind: opComplete, id: id, at: t})
		case roll < 90:
			id := 1 + rng.Intn(max1(nextID-1))
			ops = append(ops, genOp{kind: opCancel, id: id, at: t})
		case roll < 94:
			ops = append(ops, genOp{kind: opPause, at: t})
		case roll < 98:
			ops = append(ops, genOp{kind: opResume, at: t})
		default:
			ops = append(ops, genOp{kind: opTick, at: t})
		}
	}
	return ops
}

func max1(x int) int {
	if x < 1 {
		return 1
	}
	return x
}

func oid(i int) string { return fmt.Sprintf("ORD-%04d", i) }

func errCode(err error) kitchen.ErrorCode {
	if err == nil {
		return 0
	}
	var ke *kitchen.Error
	if errors.As(err, &ke) {
		return ke.Code
	}
	return -1
}

func TestDifferentialAgainstNaive(t *testing.T) {
	cfg := kitchen.Config{
		Parallelism:     2,
		EnterThreshold:  8,
		ExitThreshold:   3,
		RejectThreshold: 25,
		ReservationLead: 4,
	}
	rng := rand.New(rand.NewSource(20261006))
	for iter := 0; iter < 40; iter++ {
		seed := rng.Int63()
		t.Run(fmt.Sprintf("iter%02d", iter), func(t *testing.T) {
			runOneDiff(t, cfg, seed, 120)
		})
	}
}

func runOneDiff(t *testing.T, cfg kitchen.Config, seed int64, n int) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	ops := genSequence(rng, n, 200)

	var log strings.Builder
	fmt.Fprintf(&log, "=== differential seed=%d ops=%d cfg=%+v ===\n", seed, n, cfg)

	real, err := kitchen.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	naive := newNaive(cfg, &log)

	for step, op := range ops {
		fmt.Fprintf(&log, "step %d t=%d op=%s", step, op.at, opName(op.kind))

		var realErr error
		var realPromise int64
		var realWait int64
		var naiveRes naiveAdmit
		var naiveCode kitchen.ErrorCode

		switch op.kind {
		case opAdmitImm:
			req := kitchen.OrderRequest{ID: oid(op.id), Kind: kitchen.Immediate, Duration: op.dur}
			fmt.Fprintf(&log, " id=%s dur=%d", req.ID, op.dur)
			res, rerr := real.Admit(req, op.at)
			realErr, realPromise, realWait = rerr, res.PromisePickup, res.ExpectedWait
			naiveRes = naive.admit(req, op.at)
			naiveCode = naiveRes.errCode
			fmt.Fprintf(&log, " -> real(err=%v promise=%d wait=%d) waitBasis={slots P=%d}",
				realErr, realPromise, realWait, cfg.Parallelism)
		case opAdmitRes:
			req := kitchen.OrderRequest{ID: oid(op.id), Kind: kitchen.Reservation, Duration: op.dur, TargetPickup: op.pick}
			fmt.Fprintf(&log, " id=%s dur=%d pickup=%d targetStart=%d", req.ID, op.dur, op.pick, op.pick-op.dur)
			res, rerr := real.Admit(req, op.at)
			realErr, realPromise = rerr, res.PromisePickup
			naiveRes = naive.admit(req, op.at)
			naiveCode = naiveRes.errCode
			fmt.Fprintf(&log, " -> real(err=%v promise=%d)", realErr, realPromise)
		case opComplete:
			id := oid(op.id)
			fmt.Fprintf(&log, " id=%s", id)
			realErr = real.Complete(id, op.at)
			naiveCode = naive.complete(id, op.at)
			fmt.Fprintf(&log, " -> real(err=%v)", realErr)
		case opCancel:
			id := oid(op.id)
			fmt.Fprintf(&log, " id=%s", id)
			realErr = real.Cancel(id, op.at)
			naiveCode = naive.cancel(id, op.at)
			fmt.Fprintf(&log, " -> real(err=%v)", realErr)
		case opPause:
			realErr = real.Pause(op.at)
			naiveCode = naive.pause(op.at)
			fmt.Fprintf(&log, " -> real(err=%v)", realErr)
		case opResume:
			realErr = real.Resume(op.at)
			naiveCode = naive.resume(op.at)
			fmt.Fprintf(&log, " -> real(err=%v)", realErr)
		case opTick:
			realErr = real.Tick(op.at)
			naive.tickTo(op.at)
			naiveCode = 0
			fmt.Fprintf(&log, " -> real(err=%v)", realErr)
		}

		// 错误码一致。
		rc := errCode(realErr)
		if rc != naiveCode {
			fmt.Fprintf(&log, "\n*** MISMATCH error code real=%d naive=%d ***\n", rc, naiveCode)
			dumpDiffFailure(t, log.String())
			return
		}
		// 接单成功时承诺取货一致。
		if (op.kind == opAdmitImm || op.kind == opAdmitRes) && rc == 0 {
			if realPromise != naiveRes.promise {
				fmt.Fprintf(&log, "\n*** MISMATCH promise real=%d naive=%d ***\n", realPromise, naiveRes.promise)
				dumpDiffFailure(t, log.String())
				return
			}
		}
		// 压单状态与事件一致。
		if real.Pressed() != naive.press {
			re := real.PressureEvents()
			rwait := real.HypotheticalWait(op.at)
			nwait := naive.estimateWait(op.at)
			fmt.Fprintf(&log, "\n*** MISMATCH pressed real=%v naive=%v realEvents=%+v naiveEvents=%v ***\n  naive orders:\n",
				real.Pressed(), naive.press, re, naive.events)
			fmt.Fprintf(&log, "  waits: real=%d naive=%d\n", rwait, nwait)
			for _, no := range naive.snapshot() {
				fmt.Fprintf(&log, "    %s st=%d start=%d\n", no.id, no.status, no.startAt)
			}
			for _, inf := range real.CookingOrders() {
				fmt.Fprintf(&log, "  real cooking %s start=%d dur=%d\n", inf.ID, inf.StartAt, inf.Duration)
			}
			dumpDiffFailure(t, log.String())
			return
		}
		// 全订单状态/开工时刻/承诺一致。
		if !snapshotsEqual(real, naive) {
			fmt.Fprintf(&log, "\n*** MISMATCH order snapshots ***%s\n", snapshotDiff(real, naive))
			dumpDiffFailure(t, log.String())
			return
		}
		log.WriteByte('\n')
	}

	// 事件序列（enter/exit 交替）一致。
	realEvents := real.PressureEvents()
	if len(realEvents) != len(naive.events) {
		fmt.Fprintf(&log, "\n*** MISMATCH event count real=%d naive=%d ***\n", len(realEvents), len(naive.events))
		dumpDiffFailure(t, log.String())
		return
	}
	for i, ev := range realEvents {
		want := fmt.Sprintf("enter@%d", ev.At)
		if !ev.Entered {
			want = fmt.Sprintf("exit@%d", ev.At)
		}
		if (i%2 == 0) != ev.Entered || naive.events[i] != want {
			fmt.Fprintf(&log, "\n*** MISMATCH event %d real=%s naive=%s ***\n", i, want, naive.events[i])
			dumpDiffFailure(t, log.String())
			return
		}
	}

	if verboseDiff || os.Getenv("KITCHEN_DIFF_VERBOSE") == "1" {
		t.Logf("\n%s", log.String())
	}
}

func opName(k opKind) string {
	switch k {
	case opAdmitImm:
		return "ADMIT_IMMEDIATE"
	case opAdmitRes:
		return "ADMIT_RESERVATION"
	case opComplete:
		return "COMPLETE"
	case opCancel:
		return "CANCEL"
	case opPause:
		return "PAUSE"
	case opResume:
		return "RESUME"
	default:
		return "TICK"
	}
}

func snapshotsEqual(real *kitchen.Kitchen, naive *naiveModel) bool {
	ns := naive.snapshot()
	for _, no := range ns {
		ro, ok := real.Order(no.id)
		if !ok {
			return false
		}
		if ro.Status != no.status || ro.StartAt != no.startAt || ro.PromisePickup != no.promise {
			return false
		}
	}
	return true
}

func snapshotDiff(real *kitchen.Kitchen, naive *naiveModel) string {
	var b strings.Builder
	ns := naive.snapshot()
	for _, no := range ns {
		ro, ok := real.Order(no.id)
		if !ok {
			fmt.Fprintf(&b, "\n  %s: missing in real", no.id)
			continue
		}
		if ro.Status != no.status {
			fmt.Fprintf(&b, "\n  %s: status real=%d naive=%d", no.id, ro.Status, no.status)
		}
		if ro.StartAt != no.startAt {
			fmt.Fprintf(&b, "\n  %s: startAt real=%d naive=%d", no.id, ro.StartAt, no.startAt)
		}
		if ro.PromisePickup != no.promise {
			fmt.Fprintf(&b, "\n  %s: promise real=%d naive=%d", no.id, ro.PromisePickup, no.promise)
		}
	}
	return b.String()
}

func dumpDiffFailure(t *testing.T, log string) {
	t.Helper()
	t.Fatalf("differential mismatch; per-step input/output/basis log:\n%s", log)
}
