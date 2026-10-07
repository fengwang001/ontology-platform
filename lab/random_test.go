package lab

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// 随机操作序列对照测试：同一序列同时喂给 System、独立朴素模型以及一个
// 用于重放的全新 System，逐步比较错误类别与输出；每步的输入、输出与
// 判定依据写入日志文件。驱动器跟踪系统状态，以大概率生成合法操作、
// 小概率生成非法扰动，保证各类成功与失败路径都被覆盖。

const randomSequences = 1500

var (
	itemPool    = []string{"glu", "k", "na", "cr", "alt", "ast"}
	tubePool    = []string{"tubeA", "tubeB"}
	patientPool = []string{"p1", "p2", "p3", "p4"}
)

// seqState 是驱动器对系统状态的跟踪（仅依据已被接受的操作更新）。
type seqState struct {
	specs       map[string]ItemSpec // 项目当前目录登记
	pending     map[string][]string // 患者 -> 待采集项目
	awaiting    map[string][]string // 管 -> 待签收项目
	tubePatient map[string]string   // 管 -> 患者
	tubeType    map[string]string   // 管 -> 管类别
	appSeq      int
	tubeSeq     int
}

func newSeqState() *seqState {
	return &seqState{
		specs:       make(map[string]ItemSpec),
		pending:     make(map[string][]string),
		awaiting:    make(map[string][]string),
		tubePatient: make(map[string]string),
		tubeType:    make(map[string]string),
	}
}

func removeStr(list []string, s string) []string {
	out := list[:0]
	for _, x := range list {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

func (st *seqState) pendingPatients() []string {
	var out []string
	for p, items := range st.pending {
		if len(items) > 0 {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func (st *seqState) awaitingTubes() []string {
	var out []string
	for tb, items := range st.awaiting {
		if len(items) > 0 {
			out = append(out, tb)
		}
	}
	sort.Strings(out)
	return out
}

func TestRandomizedAgainstNaiveModel(t *testing.T) {
	logPath := filepath.Join(os.TempDir(), "lab_random_model.log")
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()

	for seq := 0; seq < randomSequences; seq++ {
		rng := rand.New(rand.NewSource(int64(1552)*1_000_003 + int64(seq)))
		runRandomSequence(t, seq, rng, w)
	}
	w.Flush()
	t.Logf("随机对照逐步日志（输入/输出/判定依据）: %s", logPath)
}

// triple 承载同一操作在三个系统上的执行结果。
type triple struct {
	realErr, naiveErr, replayErr error
	realOut, naiveOut, replayOut any
}

func (tr *triple) check(t *testing.T, seq, step int, desc string, w *bufio.Writer) {
	t.Helper()
	realCode := errCode(tr.realErr)
	fmt.Fprintf(w, "seq=%d step=%d %s => err=%s out=%s\n",
		seq, step, desc, codeName(realCode), formatOut(tr.realOut))
	if realCode != errCode(tr.naiveErr) {
		t.Fatalf("seq=%d step=%d %s: 错误类别不一致 real=%v naive=%v", seq, step, desc, tr.realErr, tr.naiveErr)
	}
	if realCode != errCode(tr.replayErr) {
		t.Fatalf("seq=%d step=%d %s: 重放错误类别不一致 real=%v replay=%v", seq, step, desc, tr.realErr, tr.replayErr)
	}
	if !outEqual(tr.realOut, tr.naiveOut) {
		t.Fatalf("seq=%d step=%d %s: 输出不一致\nreal=%s\nnaive=%s",
			seq, step, desc, formatOut(tr.realOut), formatOut(tr.naiveOut))
	}
	if !outEqual(tr.realOut, tr.replayOut) {
		t.Fatalf("seq=%d step=%d %s: 重放输出不一致\nreal=%s\nreplay=%s",
			seq, step, desc, formatOut(tr.realOut), formatOut(tr.replayOut))
	}
}

func runRandomSequence(t *testing.T, seq int, rng *rand.Rand, w *bufio.Writer) {
	t.Helper()
	real := NewSystem()
	replay := NewSystem()
	naive := newNaiveSystem()
	st := newSeqState()
	now := int64(0)
	step := 0

	doRegister := func(item, tube string, maxDelivery int64, cold bool, hemo int) {
		desc := fmt.Sprintf("RegisterItem(now=%d item=%s tube=%s maxDelivery=%d cold=%v hemo=%d)",
			now, item, tube, maxDelivery, cold, hemo)
		tr := triple{}
		tr.realErr = real.RegisterItem(now, item, tube, maxDelivery, cold, hemo)
		tr.naiveErr = naive.registerItem(now, item, tube, maxDelivery, cold, hemo)
		tr.replayErr = replay.RegisterItem(now, item, tube, maxDelivery, cold, hemo)
		tr.check(t, seq, step, desc, w)
		if tr.realErr == nil {
			st.specs[item] = ItemSpec{TubeType: tube, MaxDelivery: maxDelivery, RequireCold: cold, MaxHemolysis: hemo}
		}
		step++
	}
	doSubmit := func(appID, patient string, items []string, priority int) {
		desc := fmt.Sprintf("SubmitApplication(now=%d app=%s patient=%s items=%v priority=%d)",
			now, appID, patient, items, priority)
		tr := triple{}
		tr.realErr = real.SubmitApplication(now, appID, patient, items, priority)
		tr.naiveErr = naive.submitApplication(now, appID, patient, items, priority)
		tr.replayErr = replay.SubmitApplication(now, appID, patient, items, priority)
		tr.check(t, seq, step, desc, w)
		if tr.realErr == nil {
			st.pending[patient] = append(st.pending[patient], items...)
		}
		step++
	}
	doCollect := func(tubeID, tubeType, patient string, items []string, collectTime int64) {
		desc := fmt.Sprintf("Collect(now=%d tube=%s type=%s patient=%s items=%v collectTime=%d)",
			now, tubeID, tubeType, patient, items, collectTime)
		tr := triple{}
		tr.realErr = real.Collect(now, tubeID, tubeType, patient, items, collectTime)
		tr.naiveErr = naive.collect(now, tubeID, tubeType, patient, items, collectTime)
		tr.replayErr = replay.Collect(now, tubeID, tubeType, patient, items, collectTime)
		tr.check(t, seq, step, desc, w)
		if tr.realErr == nil {
			for _, it := range items {
				st.pending[patient] = removeStr(st.pending[patient], it)
			}
			st.awaiting[tubeID] = append([]string(nil), items...)
			st.tubePatient[tubeID] = patient
			st.tubeType[tubeID] = tubeType
		}
		step++
	}
	doTransport := func(tubeID string, cold bool) {
		desc := fmt.Sprintf("RegisterTransport(now=%d tube=%s cold=%v)", now, tubeID, cold)
		tr := triple{}
		tr.realErr = real.RegisterTransport(now, tubeID, cold)
		tr.naiveErr = naive.registerTransport(now, tubeID, cold)
		tr.replayErr = replay.RegisterTransport(now, tubeID, cold)
		tr.check(t, seq, step, desc, w)
		step++
	}
	doSign := func(tubeID string, hemo int) {
		desc := fmt.Sprintf("Sign(now=%d tube=%s hemo=%d)", now, tubeID, hemo)
		tr := triple{}
		tr.realOut, tr.realErr = real.Sign(now, tubeID, hemo)
		tr.naiveOut, tr.naiveErr = naive.sign(now, tubeID, hemo)
		tr.replayOut, tr.replayErr = replay.Sign(now, tubeID, hemo)
		tr.check(t, seq, step, desc, w)
		if tr.realErr == nil {
			patient := st.tubePatient[tubeID]
			for _, v := range tr.realOut.([]ItemVerdict) {
				if !v.Accepted && v.NewStatus == StatusPending {
					st.pending[patient] = append(st.pending[patient], v.ItemID)
				}
			}
			delete(st.awaiting, tubeID)
		}
		step++
	}
	doCancel := func(patient, item string) {
		desc := fmt.Sprintf("Cancel(now=%d patient=%s item=%s)", now, patient, item)
		tr := triple{}
		tr.realErr = real.Cancel(now, patient, item)
		tr.naiveErr = naive.cancel(now, patient, item)
		tr.replayErr = replay.Cancel(now, patient, item)
		tr.check(t, seq, step, desc, w)
		if tr.realErr == nil {
			st.pending[patient] = removeStr(st.pending[patient], item)
			for tb, items := range st.awaiting {
				if contains(items, item) {
					st.awaiting[tb] = removeStr(items, item)
					if len(st.awaiting[tb]) == 0 {
						delete(st.awaiting, tb)
					}
					break
				}
			}
		}
		step++
	}
	doQuery := func(patient string) {
		desc := fmt.Sprintf("QueryPatient(now=%d patient=%s)", now, patient)
		tr := triple{}
		tr.realOut, tr.realErr = real.QueryPatient(now, patient)
		tr.naiveOut, tr.naiveErr = naive.queryPatient(now, patient)
		tr.replayOut, tr.replayErr = replay.QueryPatient(now, patient)
		tr.check(t, seq, step, desc, w)
		step++
	}

	// 起始：登记全部项目（随机规格），保证后续申请大多合法。
	for _, item := range itemPool {
		doRegister(item, tubePool[rng.Intn(len(tubePool))], int64(1+rng.Intn(200)),
			rng.Intn(2) == 0, rng.Intn(5))
	}

	steps := 20 + rng.Intn(25)
	for i := 0; i < steps; i++ {
		// 时间推进：多数单调，少量回退，极少越界为负。
		switch r := rng.Intn(100); {
		case r < 5:
			back := int64(rng.Intn(10) + 1)
			if now >= back {
				now -= back
			}
		case r < 7:
			now = -1
		default:
			now += int64(rng.Intn(4))
			if now < 0 {
				now = 0
			}
		}

		choice := rng.Intn(100)
		switch {
		case choice < 18: // 提交申请
			st.appSeq++
			patient := patientPool[rng.Intn(len(patientPool))]
			var candidates []string
			for _, item := range itemPool {
				if !contains(st.pending[patient], item) {
					candidates = append(candidates, item)
				}
			}
			var items []string
			if len(candidates) > 0 && rng.Intn(10) > 0 {
				shuffleStrings(rng, candidates)
				n := 1 + rng.Intn(min(3, len(candidates)))
				items = append([]string(nil), candidates[:n]...)
			} else {
				items = randomItems(rng) // 可能非法或重复
			}
			doSubmit(fmt.Sprintf("app%d", st.appSeq), patient, items, rng.Intn(5))

		case choice < 38: // 采集
			patients := st.pendingPatients()
			if len(patients) == 0 {
				doQuery(patientPool[rng.Intn(len(patientPool))])
				break
			}
			patient := patients[rng.Intn(len(patients))]
			byType := map[string][]string{}
			for _, item := range st.pending[patient] {
				spec := st.specs[item]
				byType[spec.TubeType] = append(byType[spec.TubeType], item)
			}
			var types []string
			for tt := range byType {
				types = append(types, tt)
			}
			sort.Strings(types)
			tubeType := types[rng.Intn(len(types))]
			group := byType[tubeType]
			shuffleStrings(rng, group)
			n := 1 + rng.Intn(min(3, len(group)))
			items := append([]string(nil), group[:n]...)
			st.tubeSeq++
			tubeID := fmt.Sprintf("tube%d", st.tubeSeq)
			if rng.Intn(15) == 0 {
				tubeType = tubePool[rng.Intn(len(tubePool))] // 可能管类别不一致
			}
			collectTime := now
			switch rng.Intn(15) {
			case 0:
				collectTime = now + 1
			case 1:
				collectTime = now - int64(rng.Intn(20))
			}
			doCollect(tubeID, tubeType, patient, items, collectTime)

		case choice < 53: // 签收
			tubes := st.awaitingTubes()
			if len(tubes) == 0 {
				doSign(fmt.Sprintf("tube%d", 1+rng.Intn(max(st.tubeSeq, 1))), rng.Intn(5))
				break
			}
			hemo := rng.Intn(5)
			if rng.Intn(30) == 0 {
				hemo = 5
			}
			doSign(tubes[rng.Intn(len(tubes))], hemo)

		case choice < 63: // 送出登记
			tubes := st.awaitingTubes()
			if len(tubes) == 0 {
				doTransport(fmt.Sprintf("tube%d", 1+rng.Intn(max(st.tubeSeq, 1))), rng.Intn(2) == 0)
				break
			}
			doTransport(tubes[rng.Intn(len(tubes))], rng.Intn(2) == 0)

		case choice < 73: // 取消
			patient := patientPool[rng.Intn(len(patientPool))]
			var items []string
			items = append(items, st.pending[patient]...)
			for tb, tbItems := range st.awaiting {
				if st.tubePatient[tb] == patient {
					items = append(items, tbItems...)
				}
			}
			if len(items) == 0 || rng.Intn(10) == 0 {
				doCancel(patient, itemPool[rng.Intn(len(itemPool))]) // 可能不存在
			} else {
				doCancel(patient, items[rng.Intn(len(items))])
			}

		case choice < 85: // 查询
			doQuery(patientPool[rng.Intn(len(patientPool))])

		case choice < 93: // 目录变更（不追溯）
			doRegister(itemPool[rng.Intn(len(itemPool))], tubePool[rng.Intn(len(tubePool))],
				int64(1+rng.Intn(200)), rng.Intn(2) == 0, rng.Intn(5))

		default: // 纯随机扰动
			doSubmit(fmt.Sprintf("app-x%d", rng.Intn(3)), patientPool[rng.Intn(len(patientPool))],
				randomItems(rng), rng.Intn(5))
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func shuffleStrings(rng *rand.Rand, list []string) {
	rng.Shuffle(len(list), func(i, j int) { list[i], list[j] = list[j], list[i] })
}

func randomItems(rng *rand.Rand) []string {
	if rng.Intn(10) == 0 {
		return nil
	}
	n := 1 + rng.Intn(3)
	items := make([]string, 0, n)
	for i := 0; i < n; i++ {
		items = append(items, itemPool[rng.Intn(len(itemPool))])
	}
	return items
}

func errCode(err error) ErrorCode {
	if err == nil {
		return 0
	}
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return -1
}

func codeName(code ErrorCode) string {
	if code == 0 {
		return "OK"
	}
	return code.String()
}

func formatOut(out any) string {
	switch v := out.(type) {
	case nil:
		return "-"
	case []ItemVerdict:
		s := "["
		for i, x := range v {
			if i > 0 {
				s += " "
			}
			s += fmt.Sprintf("{%s accepted=%v reason=%s newStatus=%s}", x.ItemID, x.Accepted, x.Reason, x.NewStatus)
		}
		return s + "]"
	case []ItemView:
		s := "["
		for i, x := range v {
			if i > 0 {
				s += " "
			}
			rem := "nil"
			if x.RemainingSec != nil {
				rem = fmt.Sprintf("%d", *x.RemainingSec)
			}
			s += fmt.Sprintf("{%s app=%s pri=%d status=%s rej=%d lastRej=%s remaining=%s}",
				x.ItemID, x.AppID, x.Priority, x.Status, x.Rejections, x.LastRejectReason, rem)
		}
		return s + "]"
	}
	return fmt.Sprintf("%v", out)
}

func outEqual(a, b any) bool {
	switch av := a.(type) {
	case nil:
		return b == nil
	case []ItemVerdict:
		bv, ok := b.([]ItemVerdict)
		return ok && reflect.DeepEqual(av, bv)
	case []ItemView:
		bv, ok := b.([]ItemView)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			x, y := av[i], bv[i]
			if x.ItemID != y.ItemID || x.AppID != y.AppID || x.Priority != y.Priority ||
				x.Status != y.Status || x.Rejections != y.Rejections || x.LastRejectReason != y.LastRejectReason {
				return false
			}
			if (x.RemainingSec == nil) != (y.RemainingSec == nil) {
				return false
			}
			if x.RemainingSec != nil && *x.RemainingSec != *y.RemainingSec {
				return false
			}
		}
		return true
	}
	return false
}
