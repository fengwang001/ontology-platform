package upload

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// mustCreate 创建会话并在失败时终止测试。
func mustCreate(t *testing.T, r *Registry, totalParts int, maxPartSize int64) string {
	t.Helper()
	id, err := r.CreateSession(totalParts, maxPartSize)
	if err != nil {
		t.Fatalf("CreateSession(%d, %d) 意外失败: %v", totalParts, maxPartSize, err)
	}
	t.Logf("输入: CreateSession(totalParts=%d, maxPartSize=%d) -> 输出: id=%s", totalParts, maxPartSize, id)
	return id
}

// mustUpload 上传分片并在失败时终止测试。
func mustUpload(t *testing.T, r *Registry, id string, part int, size int64) {
	t.Helper()
	err := r.UploadPart(id, part, size)
	t.Logf("输入: UploadPart(id=%s, part=%d, size=%d) -> 输出: err=%v", id, part, size, err)
	if err != nil {
		t.Fatalf("UploadPart(%s, %d, %d) 意外失败: %v", id, part, size, err)
	}
}

// checkLedger 判定依据：Stats 必须与逐片登记的朴素账目一致。
func checkLedger(t *testing.T, r *Registry, id string, uploaded int, ledger map[int]int64, completed bool) {
	t.Helper()
	st, err := r.Stats(id)
	if err != nil {
		t.Fatalf("Stats(%s) 意外失败: %v", id, err)
	}
	var wantBytes int64
	for _, sz := range ledger {
		wantBytes += sz
	}
	wantOver := uploaded - len(ledger)
	t.Logf("判定依据: 朴素账目 uploaded=%d registered=%d overwrites=%d bytes=%d completed=%v",
		uploaded, len(ledger), wantOver, wantBytes, completed)
	t.Logf("输出: Stats=%+v", st)
	if st.Uploaded != uploaded {
		t.Errorf("Uploaded=%d, 期望 %d", st.Uploaded, uploaded)
	}
	if st.Registered != len(ledger) {
		t.Errorf("Registered=%d, 期望 %d", st.Registered, len(ledger))
	}
	if st.Overwrites != wantOver {
		t.Errorf("Overwrites=%d, 期望 成功上传数-不同编号数=%d", st.Overwrites, wantOver)
	}
	if st.Bytes != wantBytes {
		t.Errorf("Bytes=%d, 期望 各片大小之和=%d", st.Bytes, wantBytes)
	}
	if st.Completed != completed {
		t.Errorf("Completed=%v, 期望 %v", st.Completed, completed)
	}
	if !reflect.DeepEqual(st.PartSizes, ledger) {
		t.Errorf("PartSizes=%v, 期望 %v", st.PartSizes, ledger)
	}
}

func TestCreateSessionInvalidParams(t *testing.T) {
	r := NewRegistry()
	for _, c := range [][2]int64{{0, 10}, {-1, 10}, {3, 0}, {3, -5}, {0, 0}} {
		_, err := r.CreateSession(int(c[0]), c[1])
		t.Logf("输入: CreateSession(%d, %d) -> 输出: err=%v", c[0], c[1], err)
		if !errors.Is(err, ErrInvalidSessionParams) {
			t.Errorf("CreateSession(%d, %d) err=%v, 期望 ErrInvalidSessionParams", c[0], c[1], err)
		}
	}
}

func TestOverwriteAccounting(t *testing.T) {
	r := NewRegistry()
	id := mustCreate(t, r, 3, 100)
	ledger := map[int]int64{}
	uploaded := 0

	mustUpload(t, r, id, 1, 100)
	ledger[1] = 100
	uploaded++
	checkLedger(t, r, id, uploaded, ledger, false)

	mustUpload(t, r, id, 2, 100)
	ledger[2] = 100
	uploaded++
	checkLedger(t, r, id, uploaded, ledger, false)

	// 同号覆盖：字节数减旧加新，覆盖次数加一。
	mustUpload(t, r, id, 1, 100)
	ledger[1] = 100
	uploaded++
	checkLedger(t, r, id, uploaded, ledger, false)

	// 末片覆盖为不同大小。
	mustUpload(t, r, id, 3, 40)
	ledger[3] = 40
	uploaded++
	mustUpload(t, r, id, 3, 77)
	ledger[3] = 77
	uploaded++
	checkLedger(t, r, id, uploaded, ledger, false)
}

func TestCompleteMissingPartsListedAll(t *testing.T) {
	r := NewRegistry()
	id := mustCreate(t, r, 6, 50)
	mustUpload(t, r, id, 2, 50)
	mustUpload(t, r, id, 5, 50)

	err := r.Complete(id)
	t.Logf("输入: Complete(%s) -> 输出: err=%v", id, err)
	if !errors.Is(err, ErrMissingParts) {
		t.Fatalf("Complete err=%v, 期望 ErrMissingParts", err)
	}
	// 判定依据：缺片须按编号升序一次列全 {1,3,4,6}。
	if !strings.Contains(err.Error(), "[1,3,4,6]") {
		t.Errorf("缺片列表=%q, 期望升序包含 [1,3,4,6]", err.Error())
	}
	// 已登记分片保留，可补传。
	checkLedger(t, r, id, 2, map[int]int64{2: 50, 5: 50}, false)
}

func TestReuploadThenComplete(t *testing.T) {
	r := NewRegistry()
	id := mustCreate(t, r, 4, 10)
	mustUpload(t, r, id, 1, 10)
	mustUpload(t, r, id, 4, 3)

	if err := r.Complete(id); !errors.Is(err, ErrMissingParts) {
		t.Fatalf("首次 Complete 应缺片拒绝, err=%v", err)
	}
	// 补传缺片后再次完成。
	mustUpload(t, r, id, 2, 10)
	mustUpload(t, r, id, 3, 10)
	err := r.Complete(id)
	t.Logf("输入: Complete(%s) -> 输出: err=%v (补传后)", id, err)
	if err != nil {
		t.Fatalf("补传后 Complete 意外失败: %v", err)
	}
	checkLedger(t, r, id, 4, map[int]int64{1: 10, 2: 10, 3: 10, 4: 3}, true)
}

func TestFrozenAfterComplete(t *testing.T) {
	r := NewRegistry()
	id := mustCreate(t, r, 2, 10)
	mustUpload(t, r, id, 1, 10)
	mustUpload(t, r, id, 2, 5)
	if err := r.Complete(id); err != nil {
		t.Fatalf("Complete 意外失败: %v", err)
	}
	ledger := map[int]int64{1: 10, 2: 5}

	// 完成后上传被拒，且不改变统计。
	err := r.UploadPart(id, 1, 10)
	t.Logf("输入: 完成后 UploadPart(part=1) -> 输出: err=%v", err)
	if !errors.Is(err, ErrSessionCompleted) {
		t.Errorf("完成后上传 err=%v, 期望 ErrSessionCompleted", err)
	}
	// 重复完成被拒。
	err = r.Complete(id)
	t.Logf("输入: 重复 Complete(%s) -> 输出: err=%v", id, err)
	if !errors.Is(err, ErrSessionCompleted) {
		t.Errorf("重复完成 err=%v, 期望 ErrSessionCompleted", err)
	}
	// 冻结后查询仍如实返回统计。
	checkLedger(t, r, id, 2, ledger, true)
}

func TestErrorPriority(t *testing.T) {
	r := NewRegistry()
	done := mustCreate(t, r, 2, 10)
	mustUpload(t, r, done, 1, 10)
	mustUpload(t, r, done, 2, 5)
	if err := r.Complete(done); err != nil {
		t.Fatalf("Complete 意外失败: %v", err)
	}
	active := mustCreate(t, r, 2, 10)
	before, err := r.Stats(active)
	if err != nil {
		t.Fatalf("Stats 意外失败: %v", err)
	}

	cases := []struct {
		name string
		id   string
		part int
		size int64
		want error
		why  string
	}{
		{"不存在优先于越界与大小", "no-such", 99, -1, ErrSessionNotFound, "会话不存在时不再检查编号与大小"},
		{"已完成优先于越界与大小", done, 99, -1, ErrSessionCompleted, "会话已完成时不再检查编号与大小"},
		{"越界优先于大小", active, 99, -1, ErrPartOutOfRange, "编号越界时不再检查大小"},
		{"越界下界", active, 0, 10, ErrPartOutOfRange, "编号须 >= 1"},
		{"非末片须等于上限", active, 1, 9, ErrInvalidPartSize, "非末片大小 != 上限"},
		{"末片须为正", active, 2, 0, ErrInvalidPartSize, "末片大小 < 1"},
		{"末片须不超上限", active, 2, 11, ErrInvalidPartSize, "末片大小 > 上限"},
	}
	for _, c := range cases {
		err := r.UploadPart(c.id, c.part, c.size)
		t.Logf("输入: UploadPart(id=%s, part=%d, size=%d) -> 输出: err=%v | 判定依据: %s",
			c.id, c.part, c.size, err, c.why)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err=%v, 期望 %v", c.name, err, c.want)
		}
	}
	// 判定依据：被拒绝的操作不得改变任何统计。
	after, err := r.Stats(active)
	if err != nil {
		t.Fatalf("Stats 意外失败: %v", err)
	}
	t.Logf("判定依据: 拒绝前 Stats=%+v, 拒绝后 Stats=%+v", before, after)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("被拒绝的操作改变了统计: 前=%+v 后=%+v", before, after)
	}
}

func TestSizeRulesBoundary(t *testing.T) {
	r := NewRegistry()
	// N=1 时唯一分片即末片，大小在 1..上限 之间。
	id := mustCreate(t, r, 1, 8)
	if err := r.UploadPart(id, 1, 8); err != nil {
		t.Errorf("单片会话末片=上限 应成功, err=%v", err)
	}
	if err := r.UploadPart(id, 1, 9); !errors.Is(err, ErrInvalidPartSize) {
		t.Errorf("单片会话超出上限 应拒绝, err=%v", err)
	}
	if err := r.UploadPart(id, 1, 1); err != nil {
		t.Errorf("单片会话末片=1 应成功, err=%v", err)
	}
	checkLedger(t, r, id, 2, map[int]int64{1: 1}, false)
}

func TestStatsNotFound(t *testing.T) {
	r := NewRegistry()
	_, err := r.Stats("ghost")
	t.Logf("输入: Stats(ghost) -> 输出: err=%v", err)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("Stats err=%v, 期望 ErrSessionNotFound", err)
	}
	if err := r.Complete("ghost"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("Complete err=%v, 期望 ErrSessionNotFound", err)
	}
}
