package difftest

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	ct "ontology/contacttracing"
	"ontology/naivemodel"
)

const maxTime int64 = 10_000_000

// world 是一个小规模随机世界的生成器。
type world struct {
	h        *harness
	patients []string
	rooms    []string
}

func buildWorld(h *harness, nPatients, nRooms int) *world {
	w := &world{h: h}
	for i := 0; i < nPatients; i++ {
		w.patients = append(w.patients, fmt.Sprintf("P%02d", i))
	}
	for i := 0; i < nRooms; i++ {
		w.rooms = append(w.rooms, fmt.Sprintf("R%02d", i))
	}
	return w
}

func (w *world) pickPatient() string { return w.patients[w.h.rng.Intn(len(w.patients))] }
func (w *world) pickRoom() string    { return w.rooms[w.h.rng.Intn(len(w.rooms))] }

// runRandomSequence 执行一条随机操作序列，每步与朴素模型对照。
func runRandomSequence(t *testing.T, seed int64, nPatients, nRooms, steps int, log io.Writer) {
	rng := rand.New(rand.NewSource(seed))
	h := newHarness(t, rng, log)
	w := buildWorld(h, nPatients, nRooms)
	var now int64 = 1000

	h.printf("==== 随机序列 seed=%d patients=%d rooms=%d steps=%d ====\n", seed, nPatients, nRooms, steps)

	// 让所有患者成为“已知对象”：在 t=0 各登记一段已结束的独立住宿（各自独占病房槽位），
	// 避免对尚无记录患者做状态查询触发 NotFound（NotFound 另有专门用例）。
	for _, pid := range w.patients {
		bootRoom := w.rooms[0]
		if err := h.sys.RecordStay(pid, bootRoom, 1000, 0, 1); err != nil {
			t.Fatalf("bootstrap %s: %v", pid, err)
		}
		h.orc.stays = append(h.orc.stays, naivemodel.Stay{Patient: pid, Room: bootRoom, CheckIn: 0, CheckOut: 1})
	}

	// 每个患者是否已有（未撤销）病例。
	activeCase := map[string]string{}
	isolated := map[string]bool{}

	for i := 0; i < steps; i++ {
		h.stepNo = i
		// 时钟单调推进，偶尔跳跃。
		now += int64(rng.Intn(400))
		p := w.pickPatient()
		room := w.pickRoom()

		op := rng.Intn(100)
		switch {
		case op < 28:
			// 追补一段过去住宿（倾向于生成重叠接触）。
			base := now - int64(rng.Intn(3000)) - 10
			if base < 0 {
				base = 0
			}
			dur := int64(30 + rng.Intn(400))
			in := base
			out := base + dur
			if out > now {
				out = now
			}
			if in >= out {
				in, out = 0, 1
				if out > now {
					continue
				}
			}
			h.printf("[%03d] now=%d RecordStay(%s,%s,%d,%d)\n", i, now, p, room, in, out)
			err := h.sys.RecordStay(p, room, now, in, out)
			h.applyStayOrLog(p, room, in, out, now, err, false, 0)
		case op < 40:
			// 登记入住（开区间）。
			in := now - int64(rng.Intn(50))
			h.printf("[%03d] now=%d RecordAdmission(%s,%s,%d)\n", i, now, p, room, in)
			err := h.sys.RecordAdmission(p, room, now, in)
			h.applyStayOrLog(p, room, in, now, now, err, true, in)
		case op < 46:
			// 出住。
			if !h.openAdm[p] {
				i--
				h.printf("[%03d] (skip discharge: %s 无在住)\n", i, p)
				continue
			}
			out := now
			h.printf("[%03d] now=%d RecordDischarge(%s,%s,%d)\n", i, now, p, room, out)
			err := h.sys.RecordDischarge(p, room, now, out)
			if err == nil {
				h.openAdm[p] = false
				// 朴素镜像：找到该患者开放段并关闭。朴素模型不追踪开放段，
				// 我们在 applyAdmission 时以开放段加入，这里就地修改。
				for j := range h.orc.stays {
					s := &h.orc.stays[j]
					if s.Patient == p && s.CheckOutOpen {
						s.CheckOut = out
						s.CheckOutOpen = false
					}
				}
				h.printf("    -> OK（朴素镜像关闭开放段）\n")
			} else {
				h.printf("    -> err=%v（真实拒绝，朴素侧不作任何修改）\n", err)
			}
		case op < 60:
			// 病例登记。
			onset := now - int64(rng.Intn(2000))
			if onset < 0 {
				onset = 0
			}
			h.printf("[%03d] now=%d RegisterCase(%s,onset=%d)\n", i, now, p, onset)
			cid, err := h.sys.RegisterCase(p, now, onset)
			if err == nil {
				nc := &naivemodel.Case{ID: cid, Patient: p, OnsetAt: onset, RegisteredAt: now, IsolationOpen: true}
				h.orc.cases[cid] = nc
				h.orc.order = append(h.orc.order, cid)
				h.caseIDs = append(h.caseIDs, cid)
				activeCase[p] = cid
				isolated[cid] = false
				h.printf("    -> OK cid=%s（朴素镜像登记）\n", cid)
			} else {
				h.printf("    -> err=%v（朴素侧不登记）\n", err)
			}
		case op < 68:
			// 隔离登记。
			cid := activeCase[p]
			isoAt := now - int64(rng.Intn(1000))
			h.printf("[%03d] now=%d RecordIsolation(caseOf=%s, at=%d)\n", i, now, p, isoAt)
			var err error
			if cid == "" {
				// 用不存在 id 触发 NotFound。
				err = h.sys.RecordIsolation("NOPE", now, isoAt)
			} else {
				err = h.sys.RecordIsolation(cid, now, isoAt)
			}
			if err == nil {
				nc := h.orc.cases[cid]
				nc.IsolatedAt = isoAt
				nc.IsolationOpen = false
				isolated[cid] = true
				h.printf("    -> OK（朴素镜像隔离时刻=%d）\n", isoAt)
			} else {
				h.printf("    -> err=%v\n", err)
			}
		case op < 76:
			// 改正发病时刻。
			cid := activeCase[p]
			newOnset := now - int64(rng.Intn(3000))
			h.printf("[%03d] now=%d CorrectOnset(caseOf=%s, onset=%d)\n", i, now, p, newOnset)
			var err error
			if cid == "" {
				err = h.sys.CorrectOnset("NOPE", now, newOnset)
			} else {
				err = h.sys.CorrectOnset(cid, now, newOnset)
			}
			if err == nil {
				h.orc.cases[cid].OnsetAt = newOnset
				h.printf("    -> OK（朴素镜像发病=%d）\n", newOnset)
			} else {
				h.printf("    -> err=%v\n", err)
			}
		case op < 82:
			// 撤销病例。
			cid := activeCase[p]
			h.printf("[%03d] now=%d RevokeCase(caseOf=%s)\n", i, now, p)
			var err error
			if cid == "" {
				err = h.sys.RevokeCase("NOPE", now)
			} else {
				err = h.sys.RevokeCase(cid, now)
			}
			if err == nil {
				h.orc.cases[cid].Revoked = true
				delete(activeCase, p)
				h.printf("    -> OK（朴素镜像撤销 %s）\n", cid)
			} else {
				h.printf("    -> err=%v\n", err)
			}
		case op < 91:
			// 状态查询。
			h.printf("[%03d] now=%d PatientStatus(%s)\n", i, now, p)
			h.compareStatus(p, now)
		default:
			// 清单查询：优先一个已知病例。
			cid := "NOPE"
			if len(h.caseIDs) > 0 {
				cid = h.caseIDs[rng.Intn(len(h.caseIDs))]
			}
			h.printf("[%03d] now=%d ListContacts(%s)\n", i, now, cid)
			if cid == "NOPE" {
				if _, err := h.sys.ListContacts(cid, now); err == nil {
					t.Fatalf("step %d: expected not found", i)
				}
				h.printf("    -> NotFound（符合预期）\n")
			} else {
				h.compareContacts(cid, now)
			}
		}

		// 随机注入少量时钟回退尝试（必须被拒绝且不改状态）。
		if rng.Intn(20) == 0 {
			bad := h.sys.Stats().Now - 1
			if bad < 0 {
				continue
			}
			h.printf("[%03d] now=%d CLOCK_ROLLBACK_PROBE(at=%d)\n", i, bad, bad)
			q := w.pickPatient()
			_, err := h.sys.PatientStatusAt(q, bad)
			if ce, ok := err.(*ct.Error); !ok || ce.Kind != ct.ErrClockRollback {
				t.Fatalf("step %d: expected clock rollback, got %v", i, err)
			}
			h.printf("    -> CLOCK_ROLLBACK（已拒绝，状态与时钟不变）\n")
		}

		if i%25 == 0 {
			h.flush()
		}
	}
	h.flush()
}

// applyStayOrLog 处理住宿操作对朴素镜像的应用。
func (h *harness) applyStayOrLog(p, room string, in, out, now int64, err error, open bool, openIn int64) {
	if err != nil {
		h.printf("    -> err=%v（真实拒绝：朴素侧不追加住宿）\n", err)
		return
	}
	st := naivemodel.Stay{Patient: p, Room: room, CheckIn: in, CheckOut: out}
	if open {
		st.CheckOutOpen = true
		st.CheckOut = now
		h.openAdm[p] = true
	}
	h.orc.stays = append(h.orc.stays, st)
	h.printf("    -> OK（朴素镜像追加住宿）\n")
	_ = openIn
}

// TestRandomDifferential 与独立朴素模型对照不少于 1500 组随机操作序列。
func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	dir := filepath.Join("..", "..", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "random_differential.log")
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	mw := io.MultiWriter(f)

	const sequences = 1500
	for s := 0; s < sequences; s++ {
		seed := int64(1000 + s)
		nP := 3 + (s % 6)
		nR := 2 + (s % 4)
		steps := 40 + (s % 60)
		runRandomSequence(t, seed, nP, nR, steps, mw)
	}
	fmt.Fprintf(mw, "ALL %d RANDOM SEQUENCES MATCHED NAIVE MODEL\n", sequences)
	t.Logf("differential log written to %s", logPath)
}
