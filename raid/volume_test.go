package raid

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testBlockSize = 64

// pattern 生成确定性块内容。
func pattern(seed int) []byte {
	b := make([]byte, testBlockSize)
	for i := range b {
		b[i] = byte(seed*31 + i*7 + 1)
	}
	return b
}

// testEnv 一套测试卷：内存盘 + 日志盘 + 断电控制器。
type testEnv struct {
	v       *Volume
	disks   []*MemDevice
	journal *MemDevice
	ctrl    *CrashController
	n       int
	stripes int
}

func newTestEnv(t *testing.T, n, stripes int) *testEnv {
	t.Helper()
	ctrl := NewCrashController()
	disks := make([]*MemDevice, n)
	wrapped := make([]Device, n)
	for i := range disks {
		disks[i] = NewMemDevice(stripes, testBlockSize)
		wrapped[i] = ctrl.Wrap(disks[i])
	}
	jdev := NewMemDevice(journalBlocks(n, stripes), testBlockSize)
	v, err := Open(wrapped, ctrl.Wrap(jdev))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return &testEnv{v: v, disks: disks, journal: jdev, ctrl: ctrl, n: n, stripes: stripes}
}

// reopen 模拟重新上电：解除武装后在同一批设备上重新打开卷（触发崩溃恢复）。
func (e *testEnv) reopen(t *testing.T) {
	t.Helper()
	e.ctrl.Disarm()
	wrapped := make([]Device, e.n)
	for i, d := range e.disks {
		wrapped[i] = e.ctrl.Wrap(d)
	}
	v, err := Open(wrapped, e.ctrl.Wrap(e.journal))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	e.v = v
}

// countingDevice 统计写次数，用于验证被拒绝的操作不写任何盘。
type countingDevice struct {
	Device
	writes atomic.Int64
}

func (c *countingDevice) WriteBlock(b int, data []byte) error {
	c.writes.Add(1)
	return c.Device.WriteBlock(b, data)
}

// slowDevice 放慢写入，用于拉宽重建窗口以制造并发。
type slowDevice struct {
	Device
	delay time.Duration
}

func (s *slowDevice) WriteBlock(b int, data []byte) error {
	time.Sleep(s.delay)
	return s.Device.WriteBlock(b, data)
}

// TestMappingTable 验证 N=3/4/5 时条带到盘的映射表：
// 校验盘 P(s)=n-1-(s mod n)，数据盘从 P(s)+1 起依次回绕。
func TestMappingTable(t *testing.T) {
	for _, n := range []int{3, 4, 5} {
		t.Run(fmt.Sprintf("N=%d", n), func(t *testing.T) {
			stripes := 2 * n // 覆盖两个完整回绕周期
			e := newTestEnv(t, n, stripes)
			t.Logf("输入: n=%d, 条带数=%d", n, stripes)
			for s := 0; s < stripes; s++ {
				pd := e.v.ParityDisk(s)
				wantPd := n - 1 - (s % n)
				seen := map[int]bool{pd: true}
				dataDisks := make([]int, 0, n-1)
				for k := 0; k < n-1; k++ {
					d := e.v.DataDisk(s, k)
					dataDisks = append(dataDisks, d)
					if seen[d] {
						t.Fatalf("条带 %d: 盘 %d 被重复分配", s, d)
					}
					seen[d] = true
					// 数据盘必须从校验盘下一块起依次排列并回绕
					if want := (wantPd + 1 + k) % n; d != want {
						t.Fatalf("条带 %d 数据块 %d: 盘=%d, 期望=%d", s, k, d, want)
					}
				}
				if pd != wantPd {
					t.Fatalf("条带 %d: 校验盘=%d, 期望=%d", s, pd, wantPd)
				}
				t.Logf("条带 %d -> 校验盘=%d 数据盘=%v (依据: P=n-1-(s mod n)=%d, 数据盘自 P+1 回绕)",
					s, pd, dataDisks, wantPd)
			}
			// 逻辑块 -> (盘, 物理块) 与映射表一致
			for b := 0; b < e.v.NumBlocks(); b++ {
				stripe, k, disk, phys, err := e.v.Locate(b)
				if err != nil {
					t.Fatalf("Locate(%d): %v", b, err)
				}
				if stripe != b/(n-1) || k != b%(n-1) || phys != stripe {
					t.Fatalf("块 %d: 映射=(s%d,k%d,p%d), 期望=(s%d,k%d,p%d)",
						b, stripe, k, phys, b/(n-1), b%(n-1), b/(n-1))
				}
				if want := e.v.DataDisk(stripe, k); disk != want {
					t.Fatalf("块 %d: 盘=%d, 期望=%d", b, disk, want)
				}
			}
			t.Logf("输出: %d 个逻辑块全部映射正确; 判定依据: 每盘每条带恰好一个块且位置符合公式",
				e.v.NumBlocks())
		})
	}
}

// TestPartialWriteCrashEveryStep 部分条带写入的每个设备写步骤之后断电，
// 恢复后条带必须一致（校验 == 数据异或），且数据为旧值或新值之一。
func TestPartialWriteCrashEveryStep(t *testing.T) {
	const n, stripes = 3, 2
	// 一次部分写的设备写序列：日志头 -> 数据块 -> 校验块 -> 日志清除，共 4 步。
	const steps = 4
	oldData := [][]byte{pattern(100), pattern(101)}
	newBlock := pattern(200)

	for crashAt := 0; crashAt <= steps; crashAt++ {
		t.Run(fmt.Sprintf("crashAt=%d", crashAt), func(t *testing.T) {
			e := newTestEnv(t, n, stripes)
			if err := e.v.WriteStripe(0, oldData); err != nil {
				t.Fatalf("WriteStripe: %v", err)
			}
			e.ctrl.Arm(crashAt)
			writeErr := e.v.Write(0, newBlock) // 块 0: 条带 0 数据块 0
			crashed := e.ctrl.Crashed()
			e.reopen(t)

			ok, err := e.v.VerifyStripe(0)
			if err != nil {
				t.Fatalf("VerifyStripe: %v", err)
			}
			got, err := e.v.Read(0)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			isOld := bytesEqual(got, oldData[0])
			isNew := bytesEqual(got, newBlock)
			t.Logf("输入: 写块0(条带0), 断电点=%d/%d; 输出: 写返回err=%v 已断电=%v 条带一致=%v 数据=%s",
				crashAt, steps, writeErr, crashed, ok, dataLabel(isOld, isNew))
			t.Logf("判定依据: 意图记录先于数据落盘, 恢复时按数据块重算校验, 故任意断电点后校验==数据异或")
			if !ok {
				t.Fatalf("断电点 %d: 恢复后条带校验不一致", crashAt)
			}
			if !isOld && !isNew {
				t.Fatalf("断电点 %d: 数据既非旧值也非新值", crashAt)
			}
			// 数据块落盘（第 2 步）之后断电，新数据必须保留
			if crashAt >= 2 && !isNew {
				t.Fatalf("断电点 %d: 数据块已落盘但恢复后丢失新数据", crashAt)
			}
		})
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func dataLabel(isOld, isNew bool) string {
	switch {
	case isNew:
		return "新值"
	case isOld:
		return "旧值"
	default:
		return "未知(错误)"
	}
}

// TestConfirmedWriteSurvivesCrash 已确认（写调用已返回）的写在断电后不得丢失。
func TestConfirmedWriteSurvivesCrash(t *testing.T) {
	e := newTestEnv(t, 3, 2)
	old := [][]byte{pattern(10), pattern(11)}
	if err := e.v.WriteStripe(0, old); err != nil {
		t.Fatalf("WriteStripe: %v", err)
	}
	newBlock := pattern(20)
	if err := e.v.Write(1, newBlock); err != nil { // 写已确认
		t.Fatalf("Write: %v", err)
	}
	e.ctrl.Arm(0) // 下一次设备写即断电
	_ = e.v.Write(0, pattern(30))
	e.reopen(t)

	got, err := e.v.Read(1)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	ok, _ := e.v.VerifyStripe(0)
	t.Logf("输入: 确认写块1后断电; 输出: 块1=%s 条带一致=%v", dataLabel(bytesEqual(got, old[1]), bytesEqual(got, newBlock)), ok)
	t.Logf("判定依据: 写返回前数据+校验已落盘且意图已清除, 确认写不丢")
	if !bytesEqual(got, newBlock) {
		t.Fatalf("已确认的写入丢失")
	}
	if !ok {
		t.Fatalf("恢复后条带不一致")
	}
}

// xorBlocks 计算若干块的异或，用于在测试中独立推导期望校验。
func xorBlocks(blocks ...[]byte) []byte {
	acc := make([]byte, testBlockSize)
	for _, b := range blocks {
		xorInto(acc, b)
	}
	return acc
}

// TestRejectedOperations 非法操作整体拒绝且原因可区分，被拒绝的操作不写任何盘。
func TestRejectedOperations(t *testing.T) {
	const n, stripes = 4, 8
	ctrl := NewCrashController()
	disks := make([]*MemDevice, n)
	counting := make([]*countingDevice, n)
	wrapped := make([]Device, n)
	for i := range disks {
		disks[i] = NewMemDevice(stripes, testBlockSize)
		counting[i] = &countingDevice{Device: ctrl.Wrap(disks[i])}
		wrapped[i] = counting[i]
	}
	jdev := &countingDevice{Device: ctrl.Wrap(NewMemDevice(journalBlocks(n, stripes), testBlockSize))}
	v, err := Open(wrapped, jdev)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	totalWrites := func() int64 {
		var sum int64
		for _, c := range counting {
			sum += c.writes.Load()
		}
		return sum
	}
	// 记录基线（Open 格式化日志不算数据盘写）
	journalBaseline := jdev.writes.Load()
	dataBaseline := totalWrites()

	reject := func(name string, op func() error, want error) {
		t.Helper()
		beforeData, beforeJournal := totalWrites(), jdev.writes.Load()
		err := op()
		if !errors.Is(err, want) {
			t.Fatalf("%s: err=%v, 期望=%v", name, err, want)
		}
		dData := totalWrites() - beforeData
		dJournal := jdev.writes.Load() - beforeJournal
		t.Logf("输入: %s; 输出: 拒绝原因=%v 数据盘写=%d 日志写=%d; 判定依据: 被拒绝的操作不得写任何盘",
			name, want, dData, dJournal)
		if dData != 0 || dJournal != 0 {
			t.Fatalf("%s: 被拒绝的操作产生了 %d 次数据盘写、%d 次日志写", name, dData, dJournal)
		}
	}

	blk := pattern(1)
	reject("读越界块", func() error { _, err := v.Read(v.NumBlocks()); return err }, ErrBlockOutOfRange)
	reject("写越界块", func() error { return v.Write(v.NumBlocks(), blk) }, ErrBlockOutOfRange)
	reject("写越界条带", func() error { return v.WriteStripe(stripes, [][]byte{blk, blk, blk}) }, ErrStripeOutOfRange)
	reject("标记越界盘失效", func() error { return v.FailDisk(n) }, ErrDiskOutOfRange)
	reject("重建未失效盘", func() error { return v.Rebuild(1, nil) }, ErrDiskNotFailed)

	if err := v.FailDisk(0); err != nil {
		t.Fatalf("FailDisk(0): %v", err)
	}
	reject("已有失效盘再标记另一块", func() error { return v.FailDisk(1) }, ErrDiskAlreadyFailed)
	reject("重复标记同一盘失效", func() error { return v.FailDisk(0) }, ErrDiskAlreadyFailed)
	reject("重建未失效盘2", func() error { return v.Rebuild(2, nil) }, ErrDiskNotFailed)

	// 用慢速设备拉宽重建窗口，确保第二次 Rebuild 时重建仍在进行
	if err := v.Rebuild(0, &slowDevice{Device: NewMemDevice(stripes, testBlockSize), delay: 5 * time.Millisecond}); err != nil {
		t.Fatalf("Rebuild(0): %v", err)
	}
	reject("重建进行中再次发起", func() error { return v.Rebuild(0, nil) }, ErrRebuildInProgress)
	v.WaitRebuild()
	failed, rebuilding, _ := v.Status()
	t.Logf("重建完成后: 失效盘=%d 重建中=%v (基线: 数据盘写=%d 日志写=%d)",
		failed, rebuilding, dataBaseline, journalBaseline)
	if failed != -1 || rebuilding {
		t.Fatalf("重建后状态错误: failed=%d rebuilding=%v", failed, rebuilding)
	}
}

// TestDeterministicCrash 同一写入序列与同一断电点，两次运行得到逐字节相同的各盘内容。
func TestDeterministicCrash(t *testing.T) {
	scenario := func() ([][][]byte, [][]byte) {
		const n, stripes = 4, 6
		ctrl := NewCrashController()
		disks := make([]*MemDevice, n)
		wrapped := make([]Device, n)
		for i := range disks {
			disks[i] = NewMemDevice(stripes, testBlockSize)
			wrapped[i] = ctrl.Wrap(disks[i])
		}
		jdev := NewMemDevice(journalBlocks(n, stripes), testBlockSize)
		v, err := Open(wrapped, ctrl.Wrap(jdev))
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		_ = v.WriteStripe(0, [][]byte{pattern(1), pattern(2), pattern(3)})
		_ = v.Write(3, pattern(4))
		_ = v.Write(1, pattern(5))
		ctrl.Arm(7) // 固定断电点：第 8 次设备写时掉电
		_ = v.WriteStripe(2, [][]byte{pattern(6), pattern(7), pattern(8)})
		_ = v.Write(9, pattern(9))
		ctrl.Disarm()
		v2, err := Open(wrapped, ctrl.Wrap(jdev)) // 恢复
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		_ = v2.Write(0, pattern(10))
		snap := make([][][]byte, n)
		for i, d := range disks {
			snap[i] = d.Snapshot()
		}
		return snap, jdev.Snapshot()
	}
	disks1, journal1 := scenario()
	disks2, journal2 := scenario()
	same := true
	for i := range disks1 {
		for b := range disks1[i] {
			if !bytesEqual(disks1[i][b], disks2[i][b]) {
				same = false
			}
		}
	}
	for b := range journal1 {
		if !bytesEqual(journal1[b], journal2[b]) {
			same = false
		}
	}
	t.Logf("输入: 固定写序列+固定断电点(第8次设备写) 运行两次; 输出: 各盘逐字节相同=%v", same)
	t.Logf("判定依据: 设备写顺序与断电计数确定, 恢复重放确定, 故终态确定")
	if !same {
		t.Fatalf("同一写入序列与断电点产生了不同的盘内容")
	}
}

// TestConcurrentStripes 不同条带的写并发执行，同条带串行；
// 结束后每个已完成写入的条带都满足 校验 == 数据异或。
func TestConcurrentStripes(t *testing.T) {
	const n, stripes = 4, 32
	e := newTestEnv(t, n, stripes)

	var wg sync.WaitGroup
	// 8 个写协程，各写不相交的条带集合
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for s := g; s < stripes; s += 8 {
				for k := 0; k < n-1; k++ {
					if err := e.v.Write(s*(n-1)+k, pattern(g*1000+s*10+k)); err != nil {
						t.Errorf("Write: %v", err)
						return
					}
				}
			}
		}(g)
	}
	// 2 个协程反复写同一条带 0（同条带必须串行，结果只能是二者之一）
	hot := [][]byte{pattern(777), pattern(888)}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if err := e.v.Write(0, hot[g]); err != nil {
					t.Errorf("Write hot: %v", err)
					return
				}
			}
		}(g)
	}
	// 读协程并发读
	stop := make(chan struct{})
	var readerWg sync.WaitGroup
	readerWg.Add(1)
	go func() {
		defer readerWg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if _, err := e.v.Read(5); err != nil {
					t.Errorf("Read: %v", err)
					return
				}
			}
		}
	}()
	wg.Wait()
	close(stop)
	readerWg.Wait()

	bad := 0
	for s := 0; s < stripes; s++ {
		ok, err := e.v.VerifyStripe(s)
		if err != nil || !ok {
			bad++
		}
	}
	hotGot, _ := e.v.Read(0)
	hotOK := bytesEqual(hotGot, hot[0]) || bytesEqual(hotGot, hot[1])
	t.Logf("输入: 8 写协程(互不相交条带)+2 协程竞争条带0+1 读协程; 输出: 不一致条带数=%d 热点块为某次写入值=%v", bad, hotOK)
	t.Logf("判定依据: 每条带一把锁, 同条带串行不同条带并行, 完成写满足校验==数据异或")
	if bad != 0 {
		t.Fatalf("存在 %d 个不一致条带", bad)
	}
	if !hotOK {
		t.Fatalf("同条带并发写产生了撕裂数据")
	}
}

// TestRebuildConcurrentWrites 重建期间读写不中断，已重建的块立即参与读取。
func TestRebuildConcurrentWrites(t *testing.T) {
	const n, stripes = 4, 48
	e := newTestEnv(t, n, stripes)
	// 先铺满数据
	for s := 0; s < stripes; s++ {
		data := make([][]byte, n-1)
		for k := range data {
			data[k] = pattern(s*10 + k)
		}
		if err := e.v.WriteStripe(s, data); err != nil {
			t.Fatalf("WriteStripe: %v", err)
		}
	}
	if err := e.v.FailDisk(1); err != nil {
		t.Fatalf("FailDisk: %v", err)
	}
	// 慢速替换盘拉宽重建窗口
	repl := &slowDevice{Device: NewMemDevice(stripes, testBlockSize), delay: 200 * time.Microsecond}
	if err := e.v.Rebuild(1, repl); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	// 重建期间并发写：6 个协程各写不相交条带；并并发读失效盘上的块
	var wg sync.WaitGroup
	written := sync.Map{} // block -> pattern seed
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for s := g; s < stripes; s += 6 {
				for k := 0; k < n-1; k++ {
					b := s*(n-1) + k
					seed := 5000 + g*100 + s*10 + k
					if err := e.v.Write(b, pattern(seed)); err != nil {
						t.Errorf("Write: %v", err)
						return
					}
					written.Store(b, seed)
				}
			}
		}(g)
	}
	var readErr atomic.Int64
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				// 读失效盘 1 上的块：条带 s 中盘 1 可能是数据盘或校验盘
				if _, err := e.v.Read((i*7 + 1) % (stripes * (n - 1))); err != nil {
					readErr.Add(1)
					return
				}
			}
		}()
	}
	wg.Wait()
	e.v.WaitRebuild()

	failed, rebuilding, progress := e.v.Status()
	bad := 0
	for s := 0; s < stripes; s++ {
		ok, _ := e.v.VerifyStripe(s)
		if !ok {
			bad++
		}
	}
	mismatch := 0
	written.Range(func(key, value any) bool {
		got, err := e.v.Read(key.(int))
		if err != nil || !bytesEqual(got, pattern(value.(int))) {
			mismatch++
		}
		return true
	})
	t.Logf("输入: 重建(慢速替换盘)期间 6 写协程+2 读协程; 输出: 失效盘=%d 重建中=%v 进度=%d 不一致条带=%d 写后读错=%d 读错误=%d",
		failed, rebuilding, progress, bad, mismatch, readErr.Load())
	t.Logf("判定依据: 重建与写按条带锁串行化, 已重建块立即参与读, 重建后每条带校验==数据异或")
	if failed != -1 || rebuilding {
		t.Fatalf("重建未完成: failed=%d rebuilding=%v", failed, rebuilding)
	}
	if bad != 0 || mismatch != 0 || readErr.Load() != 0 {
		t.Fatalf("不一致条带=%d 写后读错=%d 读错误=%d", bad, mismatch, readErr.Load())
	}
}

// TestRebuildVerifyEveryStripe 重建后逐条带校验，且重建数据与原数据逐字节一致。
func TestRebuildVerifyEveryStripe(t *testing.T) {
	const n, stripes = 5, 30
	e := newTestEnv(t, n, stripes)
	for s := 0; s < stripes; s++ {
		data := make([][]byte, n-1)
		for k := range data {
			data[k] = pattern(s*100 + k)
		}
		if err := e.v.WriteStripe(s, data); err != nil {
			t.Fatalf("WriteStripe: %v", err)
		}
	}
	if err := e.v.FailDisk(2); err != nil {
		t.Fatalf("FailDisk: %v", err)
	}
	if err := e.v.Rebuild(2, NewMemDevice(stripes, testBlockSize)); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	e.v.WaitRebuild()

	badStripes := 0
	for s := 0; s < stripes; s++ {
		ok, err := e.v.VerifyStripe(s)
		if err != nil || !ok {
			badStripes++
		}
	}
	badBlocks := 0
	for b := 0; b < e.v.NumBlocks(); b++ {
		s, k := b/(n-1), b%(n-1)
		got, err := e.v.Read(b)
		if err != nil || !bytesEqual(got, pattern(s*100+k)) {
			badBlocks++
		}
	}
	t.Logf("输入: n=%d 铺满 %d 条带后失效盘2并重建; 输出: 不一致条带=%d 数据错误块=%d",
		n, stripes, badStripes, badBlocks)
	t.Logf("判定依据: 重建块=其余块异或, 重建后每条带 校验==数据异或 且数据逐字节等于原值")
	if badStripes != 0 || badBlocks != 0 {
		t.Fatalf("不一致条带=%d 数据错误块=%d", badStripes, badBlocks)
	}
}

// TestDegradedCrashReplay 降级状态下断电：意图记录携带新数据与新校验，
// 恢复时整体重放，已确认的写入不丢。
func TestDegradedCrashReplay(t *testing.T) {
	const n, stripes = 3, 2
	oldData := [][]byte{pattern(100), pattern(101)}
	newBlock := pattern(200)

	// 情形 A：写入失效盘上的数据块（数据只体现在新校验里）。
	// 设备写序列：日志负载 -> 日志头 -> 校验块 -> 日志清除，共 4 步。
	for crashAt := 0; crashAt <= 4; crashAt++ {
		t.Run(fmt.Sprintf("failedDataDisk/crashAt=%d", crashAt), func(t *testing.T) {
			e := newTestEnv(t, n, stripes)
			if err := e.v.WriteStripe(0, oldData); err != nil {
				t.Fatalf("WriteStripe: %v", err)
			}
			if err := e.v.FailDisk(0); err != nil { // 盘 0 是条带 0 的数据盘
				t.Fatalf("FailDisk: %v", err)
			}
			e.ctrl.Arm(crashAt)
			writeErr := e.v.Write(0, newBlock)
			e.reopen(t) // 失效盘状态由日志超级块恢复

			failed, _, _ := e.v.Status()
			got, err := e.v.Read(0) // 降级读：异或还原
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			isNew := bytesEqual(got, newBlock)
			// 独立推导期望校验: newP = oldP ^ oldD ^ newD = (old0^old1)^old0^new = old1^new
			wantParity := xorBlocks(oldData[0], oldData[1])
			if crashAt >= 2 {
				wantParity = xorBlocks(oldData[1], newBlock)
			}
			storedParity, err := e.disks[2].ReadBlock(0)
			if err != nil {
				t.Fatalf("ReadBlock: %v", err)
			}
			parityOK := bytesEqual(storedParity, wantParity)
			t.Logf("输入: 盘0失效后写块0, 断电点=%d/4; 输出: 写err=%v 失效盘=%d 校验块正确=%v 降级读=%s",
				crashAt, writeErr, failed, parityOK, dataLabel(bytesEqual(got, oldData[0]), isNew))
			t.Logf("判定依据: 降级意图记录携带新校验, 恢复整体重放, 重放幂等故确认写不丢")
			if failed != 0 {
				t.Fatalf("恢复后失效盘状态丢失: %d", failed)
			}
			if !parityOK {
				t.Fatalf("断电点 %d: 恢复后校验块与独立推导不符", crashAt)
			}
			// 日志头提交（第 2 步）后断电，重放必须让新数据生效
			if crashAt >= 2 && !isNew {
				t.Fatalf("断电点 %d: 意图已提交但重放后新数据未生效", crashAt)
			}
			// 重建后条带完全恢复一致
			if err := e.v.Rebuild(0, NewMemDevice(stripes, testBlockSize)); err != nil {
				t.Fatalf("Rebuild: %v", err)
			}
			e.v.WaitRebuild()
			ok, _ := e.v.VerifyStripe(0)
			got2, _ := e.v.Read(0)
			t.Logf("重建后: 条带一致=%v 块0=%s", ok, dataLabel(bytesEqual(got2, oldData[0]), bytesEqual(got2, newBlock)))
			if !ok {
				t.Fatalf("重建后条带不一致")
			}
			if !bytesEqual(got2, got) {
				t.Fatalf("重建前后降级读结果不一致")
			}
		})
	}

	// 情形 B：校验盘失效，写存活数据盘（意图携带新数据）。
	for crashAt := 0; crashAt <= 4; crashAt++ {
		t.Run(fmt.Sprintf("failedParityDisk/crashAt=%d", crashAt), func(t *testing.T) {
			e := newTestEnv(t, n, stripes)
			if err := e.v.WriteStripe(0, oldData); err != nil {
				t.Fatalf("WriteStripe: %v", err)
			}
			if err := e.v.FailDisk(2); err != nil { // 盘 2 是条带 0 的校验盘
				t.Fatalf("FailDisk: %v", err)
			}
			e.ctrl.Arm(crashAt)
			writeErr := e.v.Write(0, newBlock)
			e.reopen(t)

			got, err := e.v.Read(0)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			isNew := bytesEqual(got, newBlock)
			t.Logf("输入: 校验盘2失效后写块0, 断电点=%d/4; 输出: 写err=%v 降级读=%s",
				crashAt, writeErr, dataLabel(bytesEqual(got, oldData[0]), isNew))
			t.Logf("判定依据: 意图记录携带新数据, 恢复整体重放到存活数据盘")
			if crashAt >= 2 && !isNew {
				t.Fatalf("断电点 %d: 意图已提交但重放后新数据未生效", crashAt)
			}
			if err := e.v.Rebuild(2, NewMemDevice(stripes, testBlockSize)); err != nil {
				t.Fatalf("Rebuild: %v", err)
			}
			e.v.WaitRebuild()
			ok, _ := e.v.VerifyStripe(0)
			t.Logf("重建后: 条带一致=%v", ok)
			if !ok {
				t.Fatalf("重建后条带不一致")
			}
		})
	}
}
