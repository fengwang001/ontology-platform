package histogram

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// logAssign 打印输入值、桶归属、计数与判定依据。
func logAssign(t *testing.T, v int64, b Bucket, reason string) {
	t.Helper()
	t.Logf("输入值=%d 桶归属=[%d,%d) 下标=%d 溢出=%v 计数=%d 判定依据=%s",
		v, b.Lower, b.Upper, b.Index, b.Overflow, b.Count, reason)
}

func mustNew(t *testing.T, width, upper int64, maxActive int) *Histogram {
	t.Helper()
	h, err := New(width, upper, maxActive)
	if err != nil {
		t.Fatalf("New(%d, %d, %d) 意外失败: %v", width, upper, maxActive, err)
	}
	return h
}

func mustBucket(t *testing.T, h *Histogram, v int64) Bucket {
	t.Helper()
	b, ok, err := h.BucketOf(v)
	if err != nil {
		t.Fatalf("BucketOf(%d) 意外失败: %v", v, err)
	}
	if !ok {
		t.Fatalf("BucketOf(%d) 桶不存在", v)
	}
	return b
}

// TestBucketBoundary 验证左闭右开：值等于桶左边界归该桶，等于右边界归下一桶。
func TestBucketBoundary(t *testing.T) {
	h := mustNew(t, 10, 40, 8) // 桶: [0,10) [10,20) [20,30) [30,40)
	cases := []struct {
		v        int64
		wantIdx  int64
		wantLow  int64
		wantHigh int64
		reason   string
	}{
		{0, 0, 0, 10, "0 等于桶0左边界，归桶0"},
		{9, 0, 0, 10, "9 < 右边界10，右开，归桶0"},
		{10, 1, 10, 20, "10 等于桶1左边界，左闭，归桶1"},
		{19, 1, 10, 20, "19 < 右边界20，归桶1"},
		{20, 2, 20, 30, "20 等于桶2左边界，归桶2"},
		{39, 3, 30, 40, "39 是值域内最大值，归桶3"},
	}
	for _, c := range cases {
		if err := h.Add(c.v); err != nil {
			t.Fatalf("Add(%d) 失败: %v", c.v, err)
		}
		b := mustBucket(t, h, c.v)
		logAssign(t, c.v, b, c.reason)
		if b.Index != c.wantIdx || b.Lower != c.wantLow || b.Upper != c.wantHigh {
			t.Errorf("值=%d 归属错误: got 下标=%d [%d,%d), want 下标=%d [%d,%d)",
				c.v, b.Index, b.Lower, b.Upper, c.wantIdx, c.wantLow, c.wantHigh)
		}
		if b.Overflow {
			t.Errorf("值=%d 不应进溢出桶", c.v)
		}
	}
}

// TestOverflowBucket 验证达到或超过值域上界的值进独立溢出桶。
func TestOverflowBucket(t *testing.T) {
	h := mustNew(t, 10, 40, 8)
	for _, v := range []int64{40, 41, 1000} {
		if err := h.Add(v); err != nil {
			t.Fatalf("Add(%d) 失败: %v", v, err)
		}
	}
	b := mustBucket(t, h, 40)
	logAssign(t, 40, b, "40 达到值域上界，左闭右开，归溢出桶")
	if !b.Overflow || b.Lower != 40 || b.Upper != -1 {
		t.Errorf("溢出桶快照错误: %+v", b)
	}
	if b.Count != 3 {
		t.Errorf("溢出桶计数=%d, want 3（40/41/1000 同桶）", b.Count)
	}
	// 溢出桶与常规桶互相独立。
	if err := h.Add(39); err != nil {
		t.Fatalf("Add(39) 失败: %v", err)
	}
	reg := mustBucket(t, h, 39)
	logAssign(t, 39, reg, "39 < 上界40，归常规桶3，与溢出桶独立")
	if reg.Overflow || reg.Index != 3 {
		t.Errorf("值=39 应归常规桶3: %+v", reg)
	}
	if got := h.Len(); got != 2 {
		t.Errorf("活跃桶数=%d, want 2（桶3 + 溢出桶）", got)
	}
}

// TestRemoveUntilGone 验证撤回减计数，减到零时桶消失、查询返回不存在。
func TestRemoveUntilGone(t *testing.T) {
	h := mustNew(t, 10, 40, 8)
	for i := 0; i < 2; i++ {
		if err := h.Add(5); err != nil {
			t.Fatalf("Add(5) 失败: %v", err)
		}
	}
	b := mustBucket(t, h, 5)
	logAssign(t, 5, b, "两次加值，计数=2")
	if b.Count != 2 {
		t.Fatalf("计数=%d, want 2", b.Count)
	}
	if err := h.Remove(5); err != nil {
		t.Fatalf("Remove(5) 失败: %v", err)
	}
	b = mustBucket(t, h, 5)
	logAssign(t, 5, b, "撤回一次，计数 2->1，桶仍在")
	if b.Count != 1 {
		t.Fatalf("计数=%d, want 1", b.Count)
	}
	if err := h.Remove(5); err != nil {
		t.Fatalf("Remove(5) 失败: %v", err)
	}
	_, ok, err := h.BucketOf(5)
	if err != nil {
		t.Fatalf("BucketOf(5) 意外失败: %v", err)
	}
	t.Logf("输入值=5 计数减到零，桶消失，BucketOf 返回 ok=%v（不存在而非零）", ok)
	if ok {
		t.Error("桶计数减到零后应消失，查询应返回不存在")
	}
	if got := h.Len(); got != 0 {
		t.Errorf("活跃桶数=%d, want 0", got)
	}
}

// TestInvalidParams 验证非法构造参数被整体拒绝且原因可区分。
func TestInvalidParams(t *testing.T) {
	cases := []struct {
		name         string
		width, upper int64
		maxActive    int
		want         error
		reason       string
	}{
		{"桶宽为零", 0, 40, 8, ErrNonPositiveWidth, "width=0 非正"},
		{"桶宽为负", -5, 40, 8, ErrNonPositiveWidth, "width=-5 非正"},
		{"上界为零", 10, 0, 8, ErrInvalidUpper, "upper=0 非正"},
		{"上界为负", 10, -40, 8, ErrInvalidUpper, "upper=-40 非正"},
		{"上界非桶宽整数倍", 10, 45, 8, ErrInvalidUpper, "45%10!=0"},
		{"活跃桶上限非正", 10, 40, 0, ErrInvalidMaxActive, "maxActive=0 非正"},
	}
	for _, c := range cases {
		_, err := New(c.width, c.upper, c.maxActive)
		t.Logf("输入参数 width=%d upper=%d maxActive=%d 判定依据=%s 错误=%v",
			c.width, c.upper, c.maxActive, c.reason, err)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got err=%v, want errors.Is %v", c.name, err, c.want)
		}
	}
}

// TestInvalidOpsRejected 验证非法操作整体拒绝且直方图不变。
func TestInvalidOpsRejected(t *testing.T) {
	h := mustNew(t, 10, 40, 2)
	if err := h.Add(5); err != nil {
		t.Fatalf("Add(5) 失败: %v", err)
	}
	before := h.Buckets()

	cases := []struct {
		name   string
		op     func() error
		want   error
		reason string
	}{
		{"加负值", func() error { return h.Add(-1) }, ErrNegativeValue, "-1 < 0 非法"},
		{"撤负值", func() error { return h.Remove(-7) }, ErrNegativeValue, "-7 < 0 非法"},
		{"撤回不存在的桶", func() error { return h.Remove(15) }, ErrBucketNotFound, "桶1无计数"},
		{"活跃桶数超限", func() error {
			if err := h.Add(25); err != nil { // 占满第 2 个活跃桶
				return err
			}
			return h.Add(35) // 第 3 个活跃桶，超限
		}, ErrTooManyBuckets, "maxActive=2，第3个活跃桶被拒"},
	}
	for _, c := range cases {
		err := c.op()
		t.Logf("操作=%s 判定依据=%s 错误=%v", c.name, c.reason, err)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got err=%v, want errors.Is %v", c.name, err, c.want)
		}
	}

	// 超限用例中 Add(25) 成功、Add(35) 被拒：直方图应只多出桶2。
	want := append(append([]Bucket{}, before...), Bucket{Index: 2, Lower: 20, Upper: 30, Count: 1})
	got := h.Buckets()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("失败后直方图被改变: got %+v, want %+v", got, want)
	}
	t.Logf("失败后直方图快照=%+v，与预期逐字段一致", got)
}

// TestConcurrentAddsMatchSerial 并发加到不同桶，结果与串行参照一致。
func TestConcurrentAddsMatchSerial(t *testing.T) {
	const width, upper = 10, 100
	const workers, perWorker = 8, 500

	// 串行参照：worker w 只写桶 w，每桶 perWorker 次。
	ref := mustNew(t, width, upper, 16)
	for w := 0; w < workers; w++ {
		for i := 0; i < perWorker; i++ {
			if err := ref.Add(int64(w) * width); err != nil {
				t.Fatalf("参照 Add 失败: %v", err)
			}
		}
	}

	h := mustNew(t, width, upper, 16)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(v int64) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if err := h.Add(v); err != nil {
					t.Errorf("并发 Add(%d) 失败: %v", v, err)
					return
				}
			}
		}(int64(w) * width)
	}
	wg.Wait()

	got, want := h.Buckets(), ref.Buckets()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("并发结果与串行参照不一致:\n got=%+v\nwant=%+v", got, want)
	}
	for _, b := range got {
		t.Logf("桶下标=%d 区间=[%d,%d) 计数=%d 判定依据=与串行参照逐字段一致",
			b.Index, b.Lower, b.Upper, b.Count)
	}
}

// TestConcurrentReadsIdentical 并发读取的快照必须逐字段相同。
func TestConcurrentReadsIdentical(t *testing.T) {
	h := mustNew(t, 10, 100, 16)
	for v := int64(0); v < 100; v += 10 {
		for i := 0; i < 3; i++ {
			if err := h.Add(v); err != nil {
				t.Fatalf("Add(%d) 失败: %v", v, err)
			}
		}
	}
	if err := h.Add(1000); err != nil { // 溢出桶
		t.Fatalf("Add(1000) 失败: %v", err)
	}
	want := h.Buckets()

	const readers = 16
	errs := make(chan error, readers)
	var wg sync.WaitGroup
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if got := h.Buckets(); !reflect.DeepEqual(got, want) {
					errs <- fmt.Errorf("并发读快照不一致: got %+v, want %+v", got, want)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	t.Logf("%d 个并发读者各读 200 次，快照均与参照逐字段相同，活跃桶数=%d", readers, len(want))
}
