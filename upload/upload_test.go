package upload

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func mustErr(t *testing.T, err error, want Reason, basis string) {
	t.Helper()
	ue, ok := err.(*Error)
	if !ok {
		t.Fatalf("判定依据[%s]: 期望 *Error(%s), 实际 err=%v", basis, want, err)
	}
	t.Logf("判定依据[%s]: 输出 reason=%s msg=%q", basis, ue.Reason, ue.Msg)
	if ue.Reason != want {
		t.Fatalf("判定依据[%s]: 期望 reason=%s, 实际 %s", basis, want, ue.Reason)
	}
}

func logStats(t *testing.T, basis string, st Stats) {
	t.Helper()
	t.Logf("判定依据[%s]: 统计 uploads=%d chunks=%d overwrites=%d bytes=%d completed=%v",
		basis, st.Uploads, st.Chunks, st.Overwrites, st.Bytes, st.Completed)
}

func TestCreateInvalidParams(t *testing.T) {
	r := NewRegistry()
	for _, c := range []struct {
		total   int
		maxSize int64
	}{
		{0, 10}, {-1, 10}, {3, 0}, {3, -5},
	} {
		err := r.Create("s", c.total, c.maxSize)
		t.Logf("输入 Create(total=%d,maxSize=%d) -> 输出 err=%v", c.total, c.maxSize, err)
		mustErr(t, err, ReasonInvalidArgument, "创建参数非正")
	}
	if err := r.Create("s", 3, 10); err != nil {
		t.Fatalf("合法创建失败: %v", err)
	}
	mustErr(t, r.Create("s", 3, 10), ReasonInvalidArgument, "会话 ID 冲突")
}

func TestOverwriteAccounting(t *testing.T) {
	r := NewRegistry()
	if err := r.Create("s", 3, 100); err != nil {
		t.Fatal(err)
	}
	// 朴素账目：逐片登记。
	naive := map[int]int64{}
	uploads := 0
	apply := func(idx int, size int64) {
		t.Helper()
		err := r.Upload("s", idx, size)
		t.Logf("输入 Upload(idx=%d,size=%d) -> 输出 err=%v", idx, size, err)
		if err != nil {
			t.Fatalf("上传失败: %v", err)
		}
		naive[idx] = size
		uploads++
		var bytes int64
		for _, v := range naive {
			bytes += v
		}
		st, _ := r.Stats("s")
		logStats(t, fmt.Sprintf("登记 idx=%d 后", idx), st)
		if st.Uploads != uploads || st.Chunks != len(naive) ||
			st.Overwrites != uploads-len(naive) || st.Bytes != bytes {
			t.Fatalf("账目不一致: got %+v, want uploads=%d chunks=%d bytes=%d",
				st, uploads, len(naive), bytes)
		}
	}
	apply(1, 100) // 新片
	apply(1, 100) // 覆盖：字节减旧加新（相等），覆盖数+1
	apply(2, 100) // 新片
	apply(3, 50)  // 末片可小于上限
	apply(3, 70)  // 覆盖末片：字节 -50+70
	st, _ := r.Stats("s")
	if st.Overwrites != 2 || st.Bytes != 270 || st.Uploads != 5 || st.Chunks != 3 {
		t.Fatalf("覆盖记账错误: %+v", st)
	}
}

func TestCompleteMissingAllListedThenReupload(t *testing.T) {
	r := NewRegistry()
	if err := r.Create("s", 5, 10); err != nil {
		t.Fatal(err)
	}
	for _, idx := range []int{4, 1} { // 乱序登记 1、4
		if err := r.Upload("s", idx, 10); err != nil {
			t.Fatal(err)
		}
	}
	missing, err := r.Complete("s")
	t.Logf("输入 Complete -> 输出 missing=%v err=%v", missing, err)
	mustErr(t, err, ReasonMissingChunks, "缺片拒绝")
	if !reflect.DeepEqual(missing, []int{2, 3, 5}) {
		t.Fatalf("缺片未按升序一次列全: %v", missing)
	}
	st, _ := r.Stats("s")
	logStats(t, "缺片拒绝后分片保留", st)
	if st.Completed || st.Chunks != 2 {
		t.Fatalf("拒绝后状态被改变: %+v", st)
	}
	// 补传缺片（含一次覆盖）后再完成。
	for _, idx := range []int{2, 3, 5} {
		size := int64(10)
		if idx == 5 {
			size = 7 // 末片
		}
		if err := r.Upload("s", idx, size); err != nil {
			t.Fatalf("补传失败: %v", err)
		}
	}
	if err := r.Upload("s", 2, 10); err != nil { // 覆盖已登记片
		t.Fatal(err)
	}
	missing, err = r.Complete("s")
	t.Logf("输入 Complete(补传后) -> 输出 missing=%v err=%v", missing, err)
	if err != nil || missing != nil {
		t.Fatalf("补传后完成失败: missing=%v err=%v", missing, err)
	}
}

func TestFrozenAfterComplete(t *testing.T) {
	r := NewRegistry()
	if err := r.Create("s", 2, 10); err != nil {
		t.Fatal(err)
	}
	_ = r.Upload("s", 1, 10)
	_ = r.Upload("s", 2, 5)
	if _, err := r.Complete("s"); err != nil {
		t.Fatal(err)
	}
	// 冻结后查询仍如实返回。
	st, err := r.Stats("s")
	logStats(t, "完成后查询", st)
	if err != nil || !st.Completed || st.Bytes != 15 || st.Uploads != 2 {
		t.Fatalf("冻结后查询异常: %+v err=%v", st, err)
	}
	// 冻结后上传与重复完成均按已完成拒绝，统计不变。
	mustErr(t, r.Upload("s", 1, 10), ReasonCompleted, "冻结后上传")
	_, err = r.Complete("s")
	mustErr(t, err, ReasonCompleted, "重复完成")
	st2, _ := r.Stats("s")
	if st2 != st {
		t.Fatalf("被拒操作改变了统计: %+v -> %+v", st, st2)
	}
}

func TestErrorPriority(t *testing.T) {
	r := NewRegistry()
	if err := r.Create("s", 2, 10); err != nil {
		t.Fatal(err)
	}
	// 不存在优先于一切。
	mustErr(t, r.Upload("ghost", 99, 0), ReasonNotFound, "不存在>越界>大小")
	_ = r.Upload("s", 1, 10)
	_ = r.Upload("s", 2, 10)
	if _, err := r.Complete("s"); err != nil {
		t.Fatal(err)
	}
	// 已完成优先于越界与大小。
	mustErr(t, r.Upload("s", 99, 0), ReasonCompleted, "已完成>越界>大小")
	// 越界优先于大小（另起一个未完成会话）。
	if err := r.Create("t", 2, 10); err != nil {
		t.Fatal(err)
	}
	mustErr(t, r.Upload("t", 99, 0), ReasonIndexOutOfRange, "越界>大小")
	mustErr(t, r.Upload("t", 0, 5), ReasonIndexOutOfRange, "编号下界")
	mustErr(t, r.Upload("t", 1, 0), ReasonInvalidSize, "大小下界")
	mustErr(t, r.Upload("t", 1, 11), ReasonInvalidSize, "大小上限")
	mustErr(t, r.Upload("t", 1, 5), ReasonInvalidSize, "非末片必须等于上限")
	if err := r.Upload("t", 2, 5); err != nil {
		t.Fatalf("末片 1..上限 应合法: %v", err)
	}
	st, _ := r.Stats("t")
	logStats(t, "被拒操作不改变统计", st)
	if st.Uploads != 1 || st.Chunks != 1 || st.Bytes != 5 {
		t.Fatalf("被拒操作改变了统计: %+v", st)
	}
}

// TestSerialReplayEquivalence 随机操作序列串行重放两次，
// 并与逐片登记的朴素账目逐步对账。
func TestSerialReplayEquivalence(t *testing.T) {
	const total, maxSize = 6, 16
	rng := rand.New(rand.NewSource(42))
	type op struct {
		idx  int
		size int64
	}
	var ops []op
	for i := 0; i < 500; i++ {
		ops = append(ops, op{rng.Intn(total + 2), int64(rng.Intn(int(maxSize)+3) - 1)})
	}
	run := func() Stats {
		r := NewRegistry()
		if err := r.Create("s", total, maxSize); err != nil {
			t.Fatal(err)
		}
		naive := map[int]int64{}
		uploads := 0
		for _, o := range ops {
			err := r.Upload("s", o.idx, o.size)
			valid := o.idx >= 1 && o.idx <= total && o.size >= 1 && o.size <= maxSize &&
				(o.idx == total || o.size == maxSize)
			if valid && err != nil || !valid && err == nil {
				t.Fatalf("op=%+v 判定与规则不符: err=%v", o, err)
			}
			if valid {
				naive[o.idx] = o.size
				uploads++
			}
			st, _ := r.Stats("s")
			var bytes int64
			for _, v := range naive {
				bytes += v
			}
			if st.Uploads != uploads || st.Chunks != len(naive) ||
				st.Overwrites != uploads-len(naive) || st.Bytes != bytes {
				t.Fatalf("op=%+v 后与朴素账目不一致: got %+v want uploads=%d chunks=%d bytes=%d",
					o, st, uploads, len(naive), bytes)
			}
		}
		st, _ := r.Stats("s")
		return st
	}
	first, second := run(), run()
	t.Logf("判定依据[串行重放]: 第一次=%+v 第二次=%+v", first, second)
	if first != second {
		t.Fatalf("同一序列重放结果不同: %+v vs %+v", first, second)
	}
}

// TestConcurrentUploadComplete 上传与完成并发交错：
// 每个上传要么计入完成结果、要么以已完成被拒；不变量始终成立。
func TestConcurrentUploadComplete(t *testing.T) {
	for round := 0; round < 20; round++ {
		const total, maxSize = 8, 32
		r := NewRegistry()
		if err := r.Create("s", total, maxSize); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		succeeded := 0
		rejectedCompleted := 0
		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for i := 0; i < 50; i++ {
					idx := (w*50+i)%total + 1
					err := r.Upload("s", idx, maxSize) // 全部满片，便于核算字节
					mu.Lock()
					switch {
					case err == nil:
						succeeded++
					default:
						ue, ok := err.(*Error)
						if !ok || ue.Reason != ReasonCompleted {
							t.Errorf("上传出现非预期拒绝: %v", err)
						}
						rejectedCompleted++
					}
					mu.Unlock()
				}
			}(w)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				missing, err := r.Complete("s")
				if err == nil {
					return
				}
				ue := err.(*Error)
				if ue.Reason == ReasonCompleted { // 重复完成
					return
				}
				if ue.Reason != ReasonMissingChunks {
					t.Errorf("完成出现非预期拒绝: %v", err)
					return
				}
				_ = missing
			}
		}()
		wg.Wait()
		st, err := r.Stats("s")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("判定依据[并发交错 round=%d]: 成功上传=%d 已完成拒绝=%d 统计=%+v",
			round, succeeded, rejectedCompleted, st)
		if st.Uploads != succeeded {
			t.Fatalf("成功上传数与统计不符: %d != %d", succeeded, st.Uploads)
		}
		if st.Overwrites != st.Uploads-st.Chunks {
			t.Fatalf("覆盖数不变量被破坏: %+v", st)
		}
		if st.Bytes != int64(st.Chunks)*maxSize {
			t.Fatalf("字节数不变量被破坏: %+v", st)
		}
		if !st.Completed && rejectedCompleted > 0 {
			t.Fatalf("存在已完成拒绝但会话未完成: %+v", st)
		}
	}
}
