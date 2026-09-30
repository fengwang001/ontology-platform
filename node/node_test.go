package node

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
)

func newTestNode(t *testing.T, a int64, o int) (*Node, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	n, err := New(a, o, WithLogWriter(&buf))
	if err != nil {
		t.Fatalf("New(%d,%d) error: %v", a, o, err)
	}
	return n, &buf
}

func mustAdmit(t *testing.T, n *Node, id string, r, l int64) {
	t.Helper()
	if err := n.Admit(id, r, l); err != nil {
		t.Fatalf("Admit(%s r=%d l=%d) error: %v", id, r, l, err)
	}
}

func mustReport(t *testing.T, n *Node, id string, u int64) []Eviction {
	t.Helper()
	es, err := n.Report(id, u)
	if err != nil {
		t.Fatalf("Report(%s u=%d) error: %v", id, u, err)
	}
	return es
}

func idsOf(es []Eviction) []string {
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

func assertInvariants(t *testing.T, n *Node, where string) {
	t.Helper()
	s := n.Query()
	if s.Requests > s.Allocatable {
		t.Fatalf("[%s] sum requests %d > A %d", where, s.Requests, s.Allocatable)
	}
	if s.Limits > s.LimitCap {
		t.Fatalf("[%s] sum limits %d > cap %d", where, s.Limits, s.LimitCap)
	}
	if s.Usage > s.Allocatable {
		t.Fatalf("[%s] sum usage %d > A %d", where, s.Usage, s.Allocatable)
	}
}

func TestNewInvalid(t *testing.T) {
	if _, err := New(0, 2); !errors.Is(err, ErrInvalidAllocatable) {
		t.Fatalf("A=0: %v", err)
	}
	if _, err := New(-1, 2); !errors.Is(err, ErrInvalidAllocatable) {
		t.Fatalf("A<0: %v", err)
	}
	if _, err := New(100, 0); !errors.Is(err, ErrInvalidOversell) {
		t.Fatalf("O=0: %v", err)
	}
}

// 恰好装满的准入边界：sumR==A 且 sumL==O*A 仍可准入，再加必被拒。
func TestAdmitExactFitBoundary(t *testing.T) {
	n, _ := newTestNode(t, 100, 2)
	mustAdmit(t, n, "g", 60, 60)
	mustAdmit(t, n, "b", 40, 140) // sumR=100==A，sumL=200==O*A
	s := n.Query()
	if s.Requests != 100 || s.Limits != 200 {
		t.Fatalf("边界填充账目错误: %+v", s)
	}
	assertInvariants(t, n, "exact-fit")

	// r 与 l 同时导致超限时先报请求不足。
	if err := n.Admit("x", 1, 1); !errors.Is(err, ErrRequestExhausted) {
		t.Fatalf("同时超限应先报 ErrRequestExhausted，得到 %v", err)
	}

	// sumR==A 时，零请求容器同样因请求条件被拒。
	n2, _ := newTestNode(t, 100, 3)
	mustAdmit(t, n2, "g", 100, 100)
	// sumR==A 时，r=0 的容器恰好满足 <= 条件可准入；r>=1 才被拒（严格边界）。
	mustAdmit(t, n2, "be", 0, 1)
	if err := n2.Admit("x", 1, 1); !errors.Is(err, ErrRequestExhausted) {
		t.Fatalf("sumR+1>A 应拒绝，得到 %v", err)
	}
}

// 上限超卖边界：sumL 恰好 O*A 可准入，超 1 拒绝；请求有余量时只报上限超限。
func TestAdmitLimitOversellBoundary(t *testing.T) {
	n, _ := newTestNode(t, 100, 2)
	mustAdmit(t, n, "b", 50, 200) // sumR=50，sumL=200==O*A
	assertInvariants(t, n, "limit-fit")

	if err := n.Admit("b2", 0, 1); !errors.Is(err, ErrLimitOversold) {
		t.Fatalf("期望 ErrLimitOversold，得到 %v", err)
	}
	s := n.Query()
	if s.Requests != 50 || s.Limits != 200 || len(s.Containers) != 1 {
		t.Fatalf("被拒操作改动了账目: %+v", s)
	}

	if err := n.Admit("b3", 60, 100); !errors.Is(err, ErrRequestExhausted) {
		t.Fatalf("应先报 ErrRequestExhausted，得到 %v", err)
	}
}

func TestAdmitValidationErrors(t *testing.T) {
	n, _ := newTestNode(t, 100, 2)
	mustAdmit(t, n, "dup", 10, 10)
	cases := []struct {
		name string
		id   string
		r, l int64
		want error
	}{
		{"negative request", "c1", -1, 10, ErrNegativeRequest},
		{"zero limit", "c2", 0, 0, ErrInvalidLimit},
		{"negative limit", "c3", 0, -1, ErrInvalidLimit},
		{"limit<request", "c4", 10, 9, ErrInvalidLimit},
		{"duplicate id", "dup", 0, 1, ErrDuplicateID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := n.Admit(tc.id, tc.r, tc.l); !errors.Is(err, tc.want) {
				t.Fatalf("期望 %v，得到 %v", tc.want, err)
			}
		})
	}
	assertInvariants(t, n, "validation")
}

// u 恰等于 r 不算超出：u==r 不驱逐；sumU 恰好等于 A 也不驱逐。
func TestReportUsageEqualsRequestNoEvict(t *testing.T) {
	n, _ := newTestNode(t, 100, 2)
	mustAdmit(t, n, "g", 40, 40)
	mustAdmit(t, n, "b", 60, 160)

	if es := mustReport(t, n, "g", 40); len(es) != 0 {
		t.Fatalf("u==r 不应驱逐，得到 %v", idsOf(es))
	}
	if es := mustReport(t, n, "b", 60); len(es) != 0 {
		t.Fatalf("sumU==A 恰好达标不应驱逐，得到 %v", idsOf(es))
	}

	// u==r 的突发型归入“未超发”组；仅超发者被踢。
	n2, _ := newTestNode(t, 100, 2)
	mustAdmit(t, n2, "b1", 50, 100)
	mustAdmit(t, n2, "b2", 50, 100)
	mustReport(t, n2, "b1", 50)
	es := mustReport(t, n2, "b2", 51)
	if got := idsOf(es); len(got) != 1 || got[0] != "b2" {
		t.Fatalf("应驱逐超发的 b2，得到 %v", got)
	}
	if e := es[0]; !e.BurstableFirst || e.BurstSize != 1 || e.KindRank != 1 {
		t.Fatalf("b2 次序依据错误: %+v", e)
	}
}

// 保证型最后被驱逐：保证型 u<=r 恒成立，永远落在未超发组，
// 而超发组的总量恰为超出量，驱逐超发组即可达标，保证型始终保留。
func TestGuaranteedEvictedLast(t *testing.T) {
	n, _ := newTestNode(t, 100, 2)
	mustAdmit(t, n, "be-a", 0, 60)
	mustAdmit(t, n, "be-z", 0, 60)
	mustAdmit(t, n, "g", 50, 50)
	mustReport(t, n, "g", 50)
	es := mustReport(t, n, "be-z", 60) // 50+60=110，踢 be-z
	if got := idsOf(es); len(got) != 1 || got[0] != "be-z" {
		t.Fatalf("得到 %v", got)
	}
	// 保证型仍在册。
	if _, err := n.Report("g", 50); err != nil {
		t.Fatalf("保证型应仍在册: %v", err)
	}
	assertInvariants(t, n, "guaranteed-last")

	// 排序器直接校验：保证型排在尽力型、突发型之后；未超发的突发型也在超发组之后。
	g := &container{id: "g", request: 50, limit: 50, usage: 50, kind: KindGuaranteed}
	be := &container{id: "be", request: 0, limit: 60, usage: 1, kind: KindBestEffort}
	buOver := &container{id: "bu", request: 10, limit: 60, usage: 11, kind: KindBurstable}
	buFlat := &container{id: "bf", request: 10, limit: 60, usage: 10, kind: KindBurstable}
	if !evictionLess(be, g) || !evictionLess(buOver, g) {
		t.Fatalf("保证型应排在尽力型/超发突发型之后")
	}
	if evictionLess(buFlat, buOver) {
		t.Fatalf("未超发者不应排在超发者之前")
	}
}

// 并列按标识 + 达标即停：两个尽力型 u-r 相同（各 50），
// 保证型上报 100 使 sumU=200：按标识升序踢 a(50)->150 仍超，再踢 z(50)->100 达标即停。
func TestTieBreakerByIDAndStopAsSoonAsFit(t *testing.T) {
	n, _ := newTestNode(t, 100, 2)
	mustAdmit(t, n, "z", 0, 50)
	mustAdmit(t, n, "a", 0, 50)
	mustAdmit(t, n, "g", 100, 100)   // sumR=100==A，sumL=200==O*A
	mustReport(t, n, "z", 50)        // sumU=50
	mustReport(t, n, "a", 50)        // sumU=100 恰好，不驱逐
	es := mustReport(t, n, "g", 100) // sumU=200；固定次序 [a z g]，连踢 a、z 后 100 达标
	if got := idsOf(es); len(got) != 2 || got[0] != "a" || got[1] != "z" {
		t.Fatalf("期望固定次序 [a z] 且达标即停，得到 %v", got)
	}
	for i, want := range []string{"a", "z"} {
		if es[i].ID != want || es[i].Kind != KindBestEffort || !es[i].BurstableFirst ||
			es[i].KindRank != 0 || es[i].BurstSize != 50 {
			t.Fatalf("第 %d 个驱逐依据错误: %+v", i, es[i])
		}
	}
	s := n.Query()
	if s.Usage != 100 || len(s.Containers) != 1 { // 仅保证型保留
		t.Fatalf("达标即停后账目错误: %+v", s)
	}
	if g, ok := lookup(n, "g"); !ok || g.Usage != 100 {
		t.Fatalf("保证型应保留到最后: %+v %v", g, ok)
	}
	assertInvariants(t, n, "tie-id")
}

// 第三键 u-r 大者先：同为尽力型超发，超发量大的先踢，踢一个即达标。
func TestEvictionBurstSizeKey(t *testing.T) {
	n, _ := newTestNode(t, 100, 2)
	mustAdmit(t, n, "small", 0, 40)
	mustAdmit(t, n, "big", 0, 40)
	mustAdmit(t, n, "g", 30, 30)
	mustReport(t, n, "g", 30)
	mustReport(t, n, "small", 36)
	es := mustReport(t, n, "big", 40) // sumU=106，踢 big(40) ->66 达标
	if got := idsOf(es); len(got) != 1 || got[0] != "big" {
		t.Fatalf("u-r 大者应先踢，得到 %v", got)
	}
	if es[0].BurstSize != 40 {
		t.Fatalf("BurstSize 依据错误: %d", es[0].BurstSize)
	}
}

func lookup(n *Node, id string) (ContainerInfo, bool) {
	for _, c := range n.Query().Containers {
		if c.ID == id {
			return c, true
		}
	}
	return ContainerInfo{}, false
}

// 驱逐后腾出准入空间：被驱逐者的 r 与 l 即刻退出账目，可立即重新准入。
func TestEvictionFreesAdmissionCapacity(t *testing.T) {
	n, _ := newTestNode(t, 100, 2)
	mustAdmit(t, n, "g", 40, 40)
	mustAdmit(t, n, "b1", 60, 160) // sumR=100, sumL=200 满
	// 驱逐前无请求余量：新容器进不来。
	if err := n.Admit("b2", 1, 1); !errors.Is(err, ErrRequestExhausted) {
		t.Fatalf("驱逐前应无空间，得到 %v", err)
	}
	mustReport(t, n, "g", 40)
	es := mustReport(t, n, "b1", 61) // sumU=101，踢突发的 b1 ->40
	if got := idsOf(es); len(got) != 1 || got[0] != "b1" {
		t.Fatalf("应驱逐 b1，得到 %v", got)
	}
	s := n.Query()
	if s.Requests != 40 || s.Limits != 40 {
		t.Fatalf("驱逐后 r/l 应退出账目: %+v", s)
	}
	// 腾出 60 请求与 160 上限空间，同标识也可重新准入。
	mustAdmit(t, n, "b1", 60, 160)
	if s = n.Query(); s.Requests != 100 || s.Limits != 200 {
		t.Fatalf("重新准入后账目错误: %+v", s)
	}
	// 删除同样腾空间。
	if err := n.Delete("b1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	mustAdmit(t, n, "b1", 60, 160)
}

func TestReportAndDeleteValidation(t *testing.T) {
	n, _ := newTestNode(t, 100, 2)
	mustAdmit(t, n, "b", 10, 20)

	if _, err := n.Report("missing", 0); !errors.Is(err, ErrContainerNotFound) {
		t.Fatalf("上报不存在容器: %v", err)
	}
	if err := n.Delete("missing"); !errors.Is(err, ErrContainerNotFound) {
		t.Fatalf("删除不存在容器: %v", err)
	}
	if _, err := n.Report("b", -1); !errors.Is(err, ErrInvalidUsage) {
		t.Fatalf("u 为负: %v", err)
	}
	if _, err := n.Report("b", 21); !errors.Is(err, ErrInvalidUsage) {
		t.Fatalf("u 超过 l: %v", err)
	}
	s := n.Query()
	if s.Requests != 10 || s.Limits != 20 || s.Usage != 0 || len(s.Containers) != 1 {
		t.Fatalf("非法操作改动了账目: %+v", s)
	}
	if err := n.Delete("b"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if s = n.Query(); s.Requests != 0 || s.Limits != 0 || len(s.Containers) != 0 {
		t.Fatalf("删除后账目错误: %+v", s)
	}
}

// 相同操作序列重放得到完全相同的驱逐序列。
func TestDeterministicReplay(t *testing.T) {
	script := func(n *Node) []string {
		var seq []string
		mustAdmit(t, n, "be-z", 0, 50)
		mustAdmit(t, n, "be-a", 0, 50)
		mustAdmit(t, n, "bu", 20, 90)
		mustAdmit(t, n, "g", 80, 80)
		mustReport(t, n, "be-z", 50)
		mustReport(t, n, "g", 80)
		for _, id := range []string{"be-a", "bu"} {
			var u int64
			if id == "be-a" {
				u = 50
			} else {
				u = 70
			}
			es := mustReport(t, n, id, u)
			for _, e := range es {
				seq = append(seq, e.ID)
			}
		}
		return seq
	}
	n1, _ := newTestNode(t, 100, 3)
	n2, _ := newTestNode(t, 100, 3)
	got1, got2 := script(n1), script(n2)
	if len(got1) == 0 {
		t.Fatalf("脚本应产生驱逐")
	}
	if strings.Join(got1, ",") != strings.Join(got2, ",") {
		t.Fatalf("重放驱逐序列不一致: %v vs %v", got1, got2)
	}
	// map 迭代无序，多跑几轮确认稳定。
	for i := 0; i < 20; i++ {
		nx, _ := newTestNode(t, 100, 3)
		if got := script(nx); strings.Join(got, ",") != strings.Join(got1, ",") {
			t.Fatalf("第 %d 轮重放不一致: %v vs %v", i, got, got1)
		}
	}
}

// 并发调用准入、上报、删除与查询，竞态检测下保持账目不变量。
func TestConcurrentOperations(t *testing.T) {
	var buf bytes.Buffer
	n, err := New(100, 3, WithLogWriter(&buf))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		id := "c" + itoa(i)
		if err := n.Admit(id, 2, 7); err != nil {
			t.Fatalf("seed admit %s: %v", id, err)
		}
	}

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				id := "c" + itoa((base+k)%40)
				if _, err := n.Report(id, int64((base+k)%8)); err != nil {
					// 容器可能已被压力驱逐：这是正常结果，仅校验可区分原因。
					if !errors.Is(err, ErrContainerNotFound) {
						t.Errorf("report 非预期错误: %v", err)
						return
					}
					continue
				}
				s := n.Query()
				if s.Requests > s.Allocatable || s.Limits > s.LimitCap || s.Usage > s.Allocatable {
					t.Errorf("并发下不变量被破坏: %+v", s)
					return
				}
			}
		}(w * 5)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for k := 0; k < 100; k++ {
			id := "tmp" + itoa(k)
			if err := n.Admit(id, 0, 2); err == nil {
				_ = n.Delete(id)
			}
		}
	}()
	wg.Wait()
	assertInvariants(t, n, "concurrent")
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// 日志打印输入、输出与判定依据。
func TestLogContainsInputOutputAndReason(t *testing.T) {
	var buf bytes.Buffer
	n, err := New(100, 2, WithLogWriter(&buf))
	if err != nil {
		t.Fatal(err)
	}
	mustAdmit(t, n, "be", 0, 60)
	mustAdmit(t, n, "g", 40, 40)
	if _, err := n.Report("be", 50); err != nil {
		t.Fatal(err)
	}
	logText := buf.String()
	for _, want := range []string{"INIT", "ADMIT", "REPORT", "判定依据", "sumU"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("日志缺少 %q:\n%s", want, logText)
		}
	}
	// 再造一次真实驱逐验证 EVICT 输出与次序依据。
	buf.Reset()
	if _, err := n.Report("g", 40); err != nil { // sumU=50+40=90 无压
		t.Fatal(err)
	}
	buf.Reset()
	if _, err := n.Report("be", 60); err != nil { // sumU=100 恰好，不驱逐
		t.Fatal(err)
	}
	buf.Reset()
	// g 已无法再加；重新构造一次严格超压：be 60 + 新突发容器 41。
	if err := n.Admit("bu", 0, 50); err != nil {
		t.Fatal(err)
	}
	es, err := n.Report("bu", 41) // sumU=60+40+41=141；键1 be 与 bu 均超发，键2 同尽力型，键3 be 的 60 更大，先踢 be ->81
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 1 || es[0].ID != "be" {
		t.Fatalf("应驱逐 be，得到 %v", idsOf(es))
	}
	if !strings.Contains(buf.String(), "EVICT") {
		t.Fatalf("驱逐日志缺失:\n%s", buf.String())
	}
}
