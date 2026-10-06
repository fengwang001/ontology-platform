package difftest

import (
	"fmt"
	"io"
	"math/rand"
	"sort"
	"strings"
	"testing"

	ct "ontology/contacttracing"
	"ontology/naivemodel"
)

// oracle 是与真实系统并行的朴素参考世界，直接镜像已接受的全部住宿与病例。
type oracle struct {
	now   int64
	stays []naivemodel.Stay
	cases map[string]*naivemodel.Case
	order []string // 病例创建顺序
}

func newOracle() *oracle {
	return &oracle{cases: map[string]*naivemodel.Case{}}
}

func (o *oracle) model(now int64) *naivemodel.Model {
	cs := make([]*naivemodel.Case, 0, len(o.order))
	for _, id := range o.order {
		cs = append(cs, o.cases[id])
	}
	return &naivemodel.Model{Now: now, Stays: o.stays, Cases: cs}
}

type harness struct {
	t      *testing.T
	rng    *rand.Rand
	sys    *ct.System
	orc    *oracle
	log    io.Writer
	sb     strings.Builder
	stepNo int
	// 患者 -> 当前在住记录（真实镜像，用于生成合法 discharge/admission）
	openAdm map[string]bool
	// 病例 id 列表（含已撤销）
	caseIDs []string
	// 患者与病房集合
	pids []string
}

func newHarness(t *testing.T, rng *rand.Rand, log io.Writer) *harness {
	return &harness{
		t:       t,
		rng:     rng,
		sys:     ct.New(),
		orc:     newOracle(),
		log:     log,
		openAdm: map[string]bool{},
	}
}

func (h *harness) printf(format string, args ...any) {
	h.sb.WriteString(fmt.Sprintf(format, args...))
}

func (h *harness) flush() {
	if h.log != nil {
		_, _ = io.WriteString(h.log, h.sb.String())
	}
	h.sb.Reset()
}

func kindStr(k int) string {
	switch k {
	case 1:
		return "CLOSE"
	case 2:
		return "SECONDARY"
	default:
		return "-"
	}
}

// compareContacts 比较某病例清单。
func (h *harness) compareContacts(cid string, now int64) {
	got, err := h.sys.ListContacts(cid, now)
	nc, exists := h.orc.cases[cid]
	if !exists {
		if err == nil {
			h.t.Fatalf("step %d: real returned no error for missing case", h.stepNo)
		}
		h.printf("    ListContacts(%s) -> err=%v (case missing in oracle too)\n", cid, err)
		return
	}
	if err != nil {
		// 撤销病例真实系统返回空清单；朴素模型同样空。
		h.t.Fatalf("step %d: ListContacts unexpected err %v", h.stepNo, err)
	}
	want := h.orc.model(now).DeriveCase(nc).Sources
	if len(got) != len(want) {
		h.t.Fatalf("step %d: case %s contact count real=%d naive=%d real=%v", h.stepNo, cid, len(got), len(want), got)
	}
	for _, e := range got {
		w, ok := want[e.Patient]
		if !ok {
			h.t.Fatalf("step %d: real has extra contact %s for %s", h.stepNo, e.Patient, cid)
		}
		if int(e.Kind) != w.Kind || e.LastContactAt != w.LastContactAt {
			h.t.Fatalf("step %d: contact %s mismatch real=(%s,%d) naive=(%s,%d)",
				h.stepNo, e.Patient, e.Kind, e.LastContactAt, kindStr(w.Kind), w.LastContactAt)
		}
	}
	h.printf("    ListContacts(%s) -> %d 条（与朴素模型一致）\n", cid, len(got))
}

// compareStatus 比较某患者状态。
func (h *harness) compareStatus(pid string, now int64) {
	res, err := h.sys.PatientStatusAt(pid, now)
	if err != nil {
		h.t.Fatalf("step %d: status %s unexpected err %v", h.stepNo, pid, err)
	}
	st := h.orc.model(now).PatientStatus(pid)
	if int(res.Status) != st.Kind || res.ReleaseAt != st.ReleaseAt {
		h.t.Fatalf("step %d: status %s real=(%d,%d) naive=(%d,%d)",
			h.stepNo, pid, res.Status, res.ReleaseAt, st.Kind, st.ReleaseAt)
	}
	h.printf("    PatientStatus(%s) -> kind=%d releaseAt=%d（与朴素模型一致）\n", pid, st.Kind, st.ReleaseAt)
}

func sortedPIDs(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
