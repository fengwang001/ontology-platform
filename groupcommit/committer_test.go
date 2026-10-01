package groupcommit

import (
	"errors"
	"fmt"
	"log"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// logWriter 把提交器日志接入测试日志，便于打印输入、输出与判定依据。
type logWriter struct{ t *testing.T }

func (w logWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSuffix(string(p), "\n"))
	return len(p), nil
}

func testLogger(t *testing.T) *log.Logger {
	t.Helper()
	return log.New(logWriter{t}, "", 0)
}

// newPausedCommitter 构造一个尚未启动调度协程的提交器，
// 使测试可以按确定顺序入队后再启动，保证批次划分可复现。
func newPausedCommitter(cfg Config, persist PersistFunc) *Committer {
	c := &Committer{
		persist:  persist,
		maxItems: cfg.MaxItems,
		maxBytes: cfg.MaxBytes,
		logger:   cfg.Logger,
	}
	c.cond = sync.NewCond(&c.mu)
	return c
}

func waitQueueLen(t *testing.T, c *Committer, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.mu.Lock()
		got := len(c.queue)
		c.mu.Unlock()
		if got >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待队列长度达到 %d 超时，当前为 %d", want, got)
		}
		time.Sleep(time.Millisecond)
	}
}

// pendingWrites 保存一组已入队、等待结果的写请求。
type pendingWrites struct {
	results []Result
	wg      sync.WaitGroup
}

// enqueueInOrder 按 payloads 顺序逐条调用 Write 入队。提交器处于暂停状态时
// 队列只增不减，因此入队顺序确定，批次划分可复现。
func enqueueInOrder(t *testing.T, c *Committer, payloads [][]byte) *pendingWrites {
	t.Helper()
	pw := &pendingWrites{results: make([]Result, len(payloads))}
	for i, p := range payloads {
		pw.wg.Add(1)
		go func(i int, p []byte) {
			defer pw.wg.Done()
			seq, err := c.Write(p)
			pw.results[i] = Result{Seq: seq, Err: err}
		}(i, p)
		waitQueueLen(t, c, i+1)
	}
	return pw
}

func (pw *pendingWrites) wait() []Result {
	pw.wg.Wait()
	return pw.results
}

// recorder 记录每次持久化调用的批次内容，并可按调用次数注入失败。
type recorder struct {
	mu        sync.Mutex
	attempts  int
	batches   [][][]byte
	failOn    map[int]error
	beforeRun func(attempt int)
}

func (r *recorder) persist(batch [][]byte) error {
	r.mu.Lock()
	r.attempts++
	attempt := r.attempts
	err := r.failOn[attempt]
	if err == nil {
		cp := make([][]byte, len(batch))
		for i, p := range batch {
			cp[i] = append([]byte(nil), p...)
		}
		r.batches = append(r.batches, cp)
	}
	beforeRun := r.beforeRun
	r.mu.Unlock()
	if beforeRun != nil {
		beforeRun(attempt)
	}
	return err
}

func (r *recorder) persisted() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, b := range r.batches {
		for _, p := range b {
			out = append(out, string(p))
		}
	}
	return out
}

func (r *recorder) batchSizes() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []int
	for _, b := range r.batches {
		out = append(out, len(b))
	}
	return out
}

func makePayloads(sizes ...int) [][]byte {
	out := make([][]byte, len(sizes))
	for i, s := range sizes {
		out[i] = []byte(strings.Repeat(string(rune('a'+i)), s))
	}
	return out
}

// TestConcurrentWriters 覆盖 200 个并发调用方：每个调用方恰好收到自己
// 那条请求的结果，序号从 1 起连续、无空洞、无重号，且不串台。
func TestConcurrentWriters(t *testing.T) {
	const n = 200
	rec := &recorder{}
	c, err := NewCommitter(Config{MaxItems: 7, MaxBytes: 1 << 20, Logger: testLogger(t)}, rec.persist)
	if err != nil {
		t.Fatalf("NewCommitter: %v", err)
	}

	payload := func(i int) []byte { return []byte(fmt.Sprintf("req-%03d", i)) }
	t.Logf("输入: %d 个并发调用方，MaxItems=7，MaxBytes=1MiB，负载 req-000..req-%03d", n, n-1)

	results := make([]Result, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			seq, err := c.Write(payload(i))
			results[i] = Result{Seq: seq, Err: err}
		}(i)
	}
	wg.Wait()
	c.Close()

	owner := make([]int, n+1) // owner[seq] = 调用方下标+1
	for i, r := range results {
		if r.Err != nil {
			t.Fatalf("调用方 %d 收到错误: %v", i, r.Err)
		}
		if r.Seq < 1 || r.Seq > n {
			t.Fatalf("调用方 %d 收到越界序号 %d", i, r.Seq)
		}
		if owner[r.Seq] != 0 {
			t.Fatalf("序号 %d 被重复分配（调用方 %d 与 %d）", r.Seq, owner[r.Seq]-1, i)
		}
		owner[r.Seq] = i + 1
	}
	for seq := 1; seq <= n; seq++ {
		if owner[seq] == 0 {
			t.Fatalf("序号 %d 出现空洞", seq)
		}
	}

	persisted := rec.persisted()
	if len(persisted) != n {
		t.Fatalf("持久化条数 = %d，期望 %d", len(persisted), n)
	}
	for seq := 1; seq <= n; seq++ {
		want := string(payload(owner[seq] - 1))
		if persisted[seq-1] != want {
			t.Fatalf("串台: 序号 %d 属于调用方 %d（负载 %s），但持久化记录为 %s",
				seq, owner[seq]-1, want, persisted[seq-1])
		}
	}
	t.Logf("输出: 全部 %d 个调用方成功，序号 1..%d 无空洞无重号", n, n)
	t.Logf("判定依据: 序号集合恰为 1..%d，且每个序号在持久化日志中的负载与持有者调用方一致", n)
}

// TestBatchFailureSeqReclaim 覆盖第 k 批持久化失败后的序号连续性：
// 失败批整批以同一原因失败、序号回收，后续批次从回收处继续分配。
func TestBatchFailureSeqReclaim(t *testing.T) {
	errBoom := errors.New("injected persist failure")
	rec := &recorder{failOn: map[int]error{2: errBoom}}
	c := newPausedCommitter(Config{MaxItems: 3, MaxBytes: 1 << 20, Logger: testLogger(t)}, rec.persist)

	payloads := make([][]byte, 10)
	for i := range payloads {
		payloads[i] = []byte(fmt.Sprintf("item-%02d", i))
	}
	t.Logf("输入: 10 条请求按序入队，MaxItems=3，第 2 批注入失败 %v", errBoom)
	pw := enqueueInOrder(t, c, payloads)
	c.start()
	results := pw.wait()
	c.Close()

	// 期望批次: [0,1,2] [3,4,5](失败) [6,7,8] [9]。
	for i, r := range results {
		switch {
		case i >= 3 && i <= 5:
			if !errors.Is(r.Err, errBoom) {
				t.Fatalf("请求 %d 应以 %v 失败，实际: seq=%d err=%v", i, errBoom, r.Seq, r.Err)
			}
			if r.Seq != 0 {
				t.Fatalf("失败的请求 %d 不应占用序号，实际 seq=%d", i, r.Seq)
			}
		default:
			if r.Err != nil {
				t.Fatalf("请求 %d 应成功，实际错误: %v", i, r.Err)
			}
		}
	}
	wantSeq := []uint64{1, 2, 3, 0, 0, 0, 4, 5, 6, 7}
	for i, r := range results {
		if r.Seq != wantSeq[i] {
			t.Fatalf("请求 %d 序号 = %d，期望 %d", i, r.Seq, wantSeq[i])
		}
	}
	t.Logf("输出: 各请求序号 = %v", wantSeq)

	wantPersisted := []string{"item-00", "item-01", "item-02", "item-06", "item-07", "item-08", "item-09"}
	if got := rec.persisted(); !reflect.DeepEqual(got, wantPersisted) {
		t.Fatalf("持久化内容 = %v，期望 %v", got, wantPersisted)
	}
	if got := rec.batchSizes(); !reflect.DeepEqual(got, []int{3, 3, 1}) {
		t.Fatalf("成功批次大小 = %v，期望 [3 3 1]", got)
	}
	c.mu.Lock()
	nextSeq := c.nextSeq
	c.mu.Unlock()
	if nextSeq != 7 {
		t.Fatalf("最终 nextSeq = %d，期望 7（失败批序号已回收）", nextSeq)
	}
	t.Logf("判定依据: 失败批 3 条序号回收后，下一批从 4 继续；已持久化序号为 1..7 连续无空洞，nextSeq=%d", nextSeq)
}

// TestMaxItemsBoundary 覆盖条数上限截批：10 条请求、MaxItems=4，
// 应切出 [4 4 2] 三个批次，序号按队列顺序连续分配。
func TestMaxItemsBoundary(t *testing.T) {
	rec := &recorder{}
	c := newPausedCommitter(Config{MaxItems: 4, MaxBytes: 1 << 20, Logger: testLogger(t)}, rec.persist)

	payloads := make([][]byte, 10)
	for i := range payloads {
		payloads[i] = []byte(fmt.Sprintf("p%02d", i))
	}
	t.Logf("输入: 10 条请求按序入队，MaxItems=4，MaxBytes 足够大")
	pw := enqueueInOrder(t, c, payloads)
	c.start()
	results := pw.wait()
	c.Close()

	if got := rec.batchSizes(); !reflect.DeepEqual(got, []int{4, 4, 2}) {
		t.Fatalf("批次大小 = %v，期望 [4 4 2]", got)
	}
	for i, r := range results {
		if r.Err != nil || r.Seq != uint64(i+1) {
			t.Fatalf("请求 %d 结果 = (%d, %v)，期望序号 %d", i, r.Seq, r.Err, i+1)
		}
	}
	t.Logf("输出: 批次大小 [4 4 2]，序号 1..10 按入队顺序分配")
	t.Logf("判定依据: 每批达到 4 条即截批，最后一批为剩余 2 条")
}

// TestMaxBytesBoundary 覆盖字节上限截批：再加一条将超过 MaxBytes 时截批，
// 且单条恰好等于上限时可以入批。
func TestMaxBytesBoundary(t *testing.T) {
	rec := &recorder{}
	c := newPausedCommitter(Config{MaxItems: 100, MaxBytes: 10, Logger: testLogger(t)}, rec.persist)

	// 各条大小: 4,4,4,6,1,9,10
	// 期望批次: [4,4](+4=12>10 截) [4,6](=10) [1,9](=10) [10](恰等于上限)
	payloads := makePayloads(4, 4, 4, 6, 1, 9, 10)
	t.Logf("输入: 7 条请求大小为 [4 4 4 6 1 9 10]，MaxItems=100，MaxBytes=10")
	pw := enqueueInOrder(t, c, payloads)
	c.start()
	results := pw.wait()
	c.Close()

	if got := rec.batchSizes(); !reflect.DeepEqual(got, []int{2, 2, 2, 1}) {
		t.Fatalf("批次大小 = %v，期望 [2 2 2 1]", got)
	}
	rec.mu.Lock()
	var gotSizes [][]int
	for _, b := range rec.batches {
		var sizes []int
		for _, p := range b {
			sizes = append(sizes, len(p))
		}
		gotSizes = append(gotSizes, sizes)
	}
	rec.mu.Unlock()
	wantSizes := [][]int{{4, 4}, {4, 6}, {1, 9}, {10}}
	if !reflect.DeepEqual(gotSizes, wantSizes) {
		t.Fatalf("批次组成 = %v，期望 %v", gotSizes, wantSizes)
	}
	for i, r := range results {
		if r.Err != nil || r.Seq != uint64(i+1) {
			t.Fatalf("请求 %d 结果 = (%d, %v)，期望序号 %d", i, r.Seq, r.Err, i+1)
		}
	}
	t.Logf("输出: 批次组成 %v，序号 1..7 按入队顺序分配", wantSizes)
	t.Logf("判定依据: 累计字节再加一条将超过 10 即截批；等于 10 不截；单条等于上限单独成批")
}

// TestRejections 覆盖立即拒绝路径：空负载、单条超限、关闭后提交，
// 均不占用序号也不进入队列。
func TestRejections(t *testing.T) {
	rec := &recorder{}
	c, err := NewCommitter(Config{MaxItems: 4, MaxBytes: 8, Logger: testLogger(t)}, rec.persist)
	if err != nil {
		t.Fatalf("NewCommitter: %v", err)
	}
	t.Logf("输入: MaxBytes=8；依次提交空负载、9 字节超限负载、8 字节恰达上限负载")

	if _, err := c.Write(nil); !errors.Is(err, ErrEmptyPayload) {
		t.Fatalf("空负载应返回 ErrEmptyPayload，实际: %v", err)
	}
	if _, err := c.Write([]byte("123456789")); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("超限负载应返回 ErrPayloadTooLarge，实际: %v", err)
	}
	seq, err := c.Write([]byte("12345678"))
	if err != nil || seq != 1 {
		t.Fatalf("恰达上限的负载应成功且序号为 1，实际: seq=%d err=%v", seq, err)
	}
	t.Logf("输出: 空负载与超限负载被拒绝，恰达上限负载获得序号 1")

	c.Close()
	if _, err := c.Write([]byte("x")); !errors.Is(err, ErrClosed) {
		t.Fatalf("关闭后提交应返回 ErrClosed，实际: %v", err)
	}
	if got := rec.persisted(); len(got) != 1 {
		t.Fatalf("持久化条数 = %d，期望 1（被拒绝的请求未入队）", len(got))
	}
	t.Logf("判定依据: 前两次拒绝未消耗序号（第三次仍得 1），关闭后提交返回 ErrClosed")
}

// TestInvalidConfig 覆盖创建参数非正与持久化函数为空时的可区分错误。
func TestInvalidConfig(t *testing.T) {
	noop := func([][]byte) error { return nil }
	cases := []struct {
		name    string
		cfg     Config
		persist PersistFunc
		want    error
	}{
		{"MaxItems 为零", Config{MaxItems: 0, MaxBytes: 8}, noop, ErrInvalidMaxItems},
		{"MaxItems 为负", Config{MaxItems: -1, MaxBytes: 8}, noop, ErrInvalidMaxItems},
		{"MaxBytes 为零", Config{MaxItems: 1, MaxBytes: 0}, noop, ErrInvalidMaxBytes},
		{"MaxBytes 为负", Config{MaxItems: 1, MaxBytes: -8}, noop, ErrInvalidMaxBytes},
		{"持久化函数为空", Config{MaxItems: 1, MaxBytes: 8}, nil, ErrNilPersister},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := NewCommitter(tc.cfg, tc.persist)
			if !errors.Is(err, tc.want) {
				t.Fatalf("期望错误 %v，实际: %v", tc.want, err)
			}
			if c != nil {
				t.Fatalf("参数非法时不应返回提交器")
			}
			t.Logf("输入: %+v -> 输出: %v（判定依据: 与期望哨兵错误 errors.Is 匹配）", tc.cfg, err)
		})
	}
}

// TestCloseDrainsInFlight 覆盖关闭语义：关闭时已入队的请求必须全部
// 得到结果后 Close 才返回。
func TestCloseDrainsInFlight(t *testing.T) {
	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	rec := &recorder{}
	rec.beforeRun = func(attempt int) {
		if attempt == 1 {
			entered <- struct{}{}
		}
		<-gate // 持久化阻塞，直到测试放行
	}
	c, err := NewCommitter(Config{MaxItems: 2, MaxBytes: 1 << 20, Logger: testLogger(t)}, rec.persist)
	if err != nil {
		t.Fatalf("NewCommitter: %v", err)
	}

	const n = 5
	results := make([]Result, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			seq, err := c.Write([]byte(fmt.Sprintf("w%d", i)))
			results[i] = Result{Seq: seq, Err: err}
		}(i)
	}
	<-entered // 第一批正在持久化，其余请求在队列中
	t.Logf("输入: %d 个在途请求（第一批持久化中，其余排队），此时调用 Close", n)

	closeDone := make(chan struct{})
	go func() {
		c.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
		t.Fatalf("存在未完成的在途请求，Close 不应返回")
	case <-time.After(100 * time.Millisecond):
		t.Logf("输出: 在途请求未完成时 Close 未返回（符合预期）")
	}

	close(gate) // 放行所有批次
	wg.Wait()
	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("所有请求完成后 Close 仍未返回")
	}

	seen := make(map[uint64]bool)
	for i, r := range results {
		if r.Err != nil {
			t.Fatalf("请求 %d 应成功，实际: %v", i, r.Err)
		}
		if r.Seq < 1 || r.Seq > n || seen[r.Seq] {
			t.Fatalf("请求 %d 序号异常: %d", i, r.Seq)
		}
		seen[r.Seq] = true
	}
	t.Logf("输出: 全部 %d 个在途请求在 Close 返回前得到结果，序号 1..%d 连续", n, n)
	t.Logf("判定依据: 持久化被阻塞期间 Close 不返回；放行后所有请求成功且 Close 才返回")
}

// TestDeterministicBatching 覆盖可复现性：给定相同的入队顺序与相同的
// 故障注入，两次运行的批次划分与序号完全相同。
func TestDeterministicBatching(t *testing.T) {
	errBoom := errors.New("injected persist failure")
	run := func() ([]int, []Result, []string) {
		rec := &recorder{failOn: map[int]error{2: errBoom}}
		c := newPausedCommitter(Config{MaxItems: 3, MaxBytes: 12}, rec.persist)
		payloads := makePayloads(3, 3, 3, 3, 4, 4, 4, 1, 2, 5)
		pw := enqueueInOrder(t, c, payloads)
		c.start()
		results := pw.wait()
		c.Close()
		return rec.batchSizes(), results, rec.persisted()
	}

	sizes1, results1, persisted1 := run()
	sizes2, results2, persisted2 := run()
	t.Logf("输入: 相同负载序列 [3 3 3 3 4 4 4 1 2 5]，MaxItems=3，MaxBytes=12，均在第 2 批注入失败")
	t.Logf("输出: 运行1 批次=%v 运行2 批次=%v", sizes1, sizes2)

	if !reflect.DeepEqual(sizes1, sizes2) {
		t.Fatalf("两次运行批次划分不同: %v vs %v", sizes1, sizes2)
	}
	if !reflect.DeepEqual(results1, results2) {
		t.Fatalf("两次运行结果不同: %v vs %v", results1, results2)
	}
	if !reflect.DeepEqual(persisted1, persisted2) {
		t.Fatalf("两次运行持久化内容不同: %v vs %v", persisted1, persisted2)
	}
	t.Logf("判定依据: 两次运行的批次划分、各请求序号与错误、持久化内容完全一致")
}
