package tier

import (
	"flag"
	"sort"
	"strconv"
	"strings"
	"testing"

	"ontology/rollup"
)

var verbose = flag.Bool("vv", false, "print every op and decision")

func note(t *testing.T, format string, args ...any) {
	t.Helper()
	if *verbose {
		t.Logf(format, args...)
	}
}

func dump(s *Store) string {
	var b strings.Builder
	b.WriteString("L0=[")
	for _, p := range s.l0 {
		b.WriteString("(" + strconv.FormatInt(p.TS, 10) + "," + strconv.FormatInt(p.V, 10) + ")")
	}
	b.WriteString("] L1={")
	for _, k := range sortedKeys(s.l1) {
		b.WriteString(strconv.FormatInt(k, 10) + ":" + bucketStr(s.l1[k]))
	}
	b.WriteString("} L2={")
	for _, k := range sortedKeys(s.l2) {
		b.WriteString(strconv.FormatInt(k, 10) + ":" + bucketStr(s.l2[k]))
	}
	b.WriteString("}")
	return b.String()
}

func bucketStr(v rollup.Bucket) string {
	return "{" + strconv.FormatInt(v.Count, 10) + "," + strconv.FormatInt(v.Sum, 10) +
		"," + strconv.FormatInt(v.Min, 10) + "," + strconv.FormatInt(v.Max, 10) + "}"
}

func sortedKeys(m map[int64]rollup.Bucket) []int64 {
	ks := make([]int64, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i] < ks[j] })
	return ks
}

// 规格中的完整示例：折叠、迟到写入三落层、整桶查询。
func TestSpecExample(t *testing.T) {
	s := New(120000, 7200000, 86400000, 1000)
	for _, w := range [][2]int64{{10000, 5}, {50000, 3}, {70000, 7}} {
		must(t, s.Write(w[0], w[1]))
	}
	must(t, s.Advance(180000))
	if b := s.l1[0]; b.Count != 2 || b.Sum != 8 || b.Min != 3 || b.Max != 5 {
		t.Fatalf("L1 m0 = %+v", b)
	}
	if len(s.l0) != 1 || s.l0[0].TS != 70000 {
		t.Fatalf("L0 = %+v, want only ts=70000", s.l0)
	}
	q1, _ := s.Query(0, 180000)
	if q1 != (Result{3, 15, 3, 7, 0}) {
		t.Fatalf("q1 = %+v", q1)
	}
	q2, _ := s.Query(0, 30000)
	if q2.Count != 0 || q2.Skipped != 2 {
		t.Fatalf("q2 = %+v", q2)
	}

	// 迟到写入：分钟 0 已够龄 -> 并入已有 L1 桶，单位不增。
	u := s.Units()
	must(t, s.Write(20000, 1))
	if s.Units() != u {
		t.Fatalf("units grew %d -> %d", u, s.Units())
	}
	if b := s.l1[0]; b.Count != 3 || b.Sum != 9 || b.Min != 1 || b.Max != 5 {
		t.Fatalf("L1 m0 after late write = %+v", b)
	}

	// 小时 0 在 10800000 恰等 A1：L1 全量（含迟到点）折成 L2。
	must(t, s.Advance(10800000))
	if len(s.l1) != 0 {
		t.Fatalf("L1 = %v, want empty", s.l1)
	}
	if b := s.l2[0]; b.Count != 4 || b.Sum != 16 || b.Min != 1 || b.Max != 7 {
		t.Fatalf("L2 h0 = %+v", b)
	}
	must(t, s.Write(100, 2))
	if b := s.l2[0]; b.Count != 5 || b.Sum != 18 || b.Min != 1 || b.Max != 7 {
		t.Fatalf("L2 h0 late = %+v", b)
	}
	q3, _ := s.Query(0, 3600000)
	if q3 != (Result{5, 18, 1, 7, 0}) {
		t.Fatalf("q3 = %+v", q3)
	}
}

// 删除恰等阈值：now-(h+1)*Hour == A2 时整小时三层数据被删除。
func TestDeleteAtExactThreshold(t *testing.T) {
	s := New(60000, 120000, 3600000, 1000)
	if err := s.Write(100, 9); err != nil {
		t.Fatal(err)
	}
	if err := s.Advance(3720000); err != nil {
		t.Fatal(err)
	}
	if s.Units() != 1 || s.l2[0].Sum != 9 {
		t.Fatalf("expected single L2 bucket, %s", dump(s))
	}
	if err := s.Advance(7320000); err != nil {
		t.Fatal(err)
	}
	if s.Units() != 0 {
		t.Fatalf("hour 0 should be deleted at exact A2, %s", dump(s))
	}
	if err := s.Write(100, 1); err != ErrExpired {
		t.Fatalf("write deleted hour = %v want ErrExpired", err)
	}
}

// 时钟语义：回退报错；相等为无操作；参数越界。
func TestClockRules(t *testing.T) {
	s := New(60000, 120000, 3600000, 10)
	must(t, s.Advance(50))
	if err := s.Advance(49); err != ErrClock {
		t.Fatalf("rewind = %v", err)
	}
	must(t, s.Advance(50)) // 相等为无操作
	if err := s.Advance(maxTS + 1); err != ErrInvalid {
		t.Fatalf("now out of range = %v", err)
	}
	if err := s.Write(-1, 0); err != ErrInvalid {
		t.Fatalf("ts<0 = %v", err)
	}
	if err := s.Write(0, maxV+1); err != ErrInvalid {
		t.Fatalf("v too big = %v", err)
	}
	if _, err := s.Query(10, 10); err != ErrInvalid {
		t.Fatalf("from==to = %v", err)
	}
}

// 容量：新增占单位、并入不占；满后新增被拒且状态不变。
func TestCapacity(t *testing.T) {
	s := New(60000, 120000, 3600000, 2)
	must(t, s.Write(0, 1))
	must(t, s.Write(1, 2))
	if err := s.Write(2, 3); err != ErrCapacity {
		t.Fatalf("third new point = %v want ErrCapacity", err)
	}
	must(t, s.Advance(120000)) // 恰等 A0：分钟 0 折成一个桶
	if s.Units() != 1 {
		t.Fatalf("fold should keep 1 bucket, got %d", s.Units())
	}
	for v := int64(3); v <= 6; v++ {
		must(t, s.Write(v, v)) // 并入已有桶不占容量
	}
	if b := s.l1[0]; b.Count != 6 {
		t.Fatalf("bucket = %+v want count 6", b)
	}
	must(t, s.Write(700000, 1)) // 年轻 L0 点占满第 2 个单位
	if err := s.Write(120000, 1); err != ErrCapacity {
		t.Fatalf("new L1 bucket at cap = %v, want ErrCapacity", err)
	}
}

// 构造参数必须 0<A0<=A1<=A2 且 Cap>0，违反返回 nil。
func TestNewValidation(t *testing.T) {
	for _, p := range [][4]int64{
		{0, 1, 2, 3}, {2, 1, 2, 3}, {1, 3, 2, 3}, {1, 1, 1, 0}, {-1, 1, 1, 1}} {
		if New(p[0], p[1], p[2], p[3]) != nil {
			t.Fatalf("New(%v) non-nil, want failure", p)
		}
	}
	if New(1, 1, 1, 1) == nil {
		t.Fatal("valid construction returned nil")
	}
}

// 拒绝顺序中溢出先于容量：并入使 Sum 超 int64 返回 ErrOverflow，桶不变。
func TestWriteOverflow(t *testing.T) {
	s := New(60000, 120000, 3600000, 1000000)
	must(t, s.Advance(120000))
	const big = 1_000_000_000_000
	for i := 0; i < 9_223_372; i++ {
		if err := s.Write(100, big); err != nil {
			t.Fatalf("unexpected err i=%d: %v", i, err)
		}
	}
	before := s.l1[0]
	if err := s.Write(100, big); err != ErrOverflow || s.l1[0] != before {
		t.Fatalf("overflow err=%v bucket=%+v before=%+v", err, s.l1[0], before)
	}
}
