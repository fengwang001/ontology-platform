package auditlog

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

const testInterval = 4

// buildLog 追加 payloads 并返回日志与其一致快照。
func buildLog(t *testing.T, payloads ...[]byte) (*Log, []Record) {
	t.Helper()
	l, err := NewLog(testInterval, 1024, nil, nil)
	if err != nil {
		t.Fatalf("NewLog: %v", err)
	}
	for _, p := range payloads {
		if _, err := l.Append(p); err != nil {
			t.Fatalf("Append(%q): %v", p, err)
		}
	}
	return l, l.Snapshot()
}

func payloads(n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		out[i] = []byte(fmt.Sprintf("payload-%d", i+1))
	}
	return out
}

func logResult(t *testing.T, input string, r Result) {
	t.Helper()
	t.Logf("输入: %s", input)
	t.Logf("输出: OK=%v 类别=%s 位置=%d 已锚定前缀=%d", r.OK, r.Category, r.Position, r.AnchoredPrefix)
	t.Logf("判定依据: %s", r.Reason)
}

func TestAppendAndVerifyOK(t *testing.T) {
	l, _ := buildLog(t, payloads(10)...)
	r := l.Verify()
	logResult(t, "按序追加 10 条，A=4", r)
	if !r.OK {
		t.Fatalf("期望通过，得到 %s @ %d", r.Category, r.Position)
	}
	if r.AnchoredPrefix != 8 {
		t.Fatalf("已锚定前缀 = %d，期望 8（最大锚点序号）", r.AnchoredPrefix)
	}
	anchors := l.Anchors()
	if len(anchors) != 2 || anchors[0].Seq != 4 || anchors[1].Seq != 8 {
		t.Fatalf("锚点 = %+v，期望序号 4 与 8", anchors)
	}
}

func TestNewLogRejectsNonPositiveInterval(t *testing.T) {
	_, err := NewLog(0, 1024, nil, nil)
	t.Logf("输入: A=0；输出: err=%v", err)
	if !errors.Is(err, ErrNonPositiveInterval) {
		t.Fatalf("期望 ErrNonPositiveInterval，得到 %v", err)
	}
}

func TestAppendRejectedReasons(t *testing.T) {
	l, err := NewLog(testInterval, 4, nil, nil)
	if err != nil {
		t.Fatalf("NewLog: %v", err)
	}
	if _, err := l.Append(nil); !errors.Is(err, ErrEmptyPayload) {
		t.Fatalf("空负载：期望 ErrEmptyPayload，得到 %v", err)
	}
	// 同时为空与超长时只报第一个原因（负载为空）。
	if _, err := l.Append([]byte{}); !errors.Is(err, ErrEmptyPayload) {
		t.Fatalf("空负载（亦超长场景前置）：期望 ErrEmptyPayload，得到 %v", err)
	}
	if _, err := l.Append([]byte("12345")); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("超长负载：期望 ErrPayloadTooLarge，得到 %v", err)
	}
	t.Logf("输入: 空负载 / 超长负载；输出: %v / %v", ErrEmptyPayload, ErrPayloadTooLarge)
	if n := l.Len(); n != 0 {
		t.Fatalf("被拒绝的追加占用了序号：链长 = %d，期望 0", n)
	}
	rec, err := l.Append([]byte("ok"))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if rec.Seq != 1 {
		t.Fatalf("被拒绝的追加改变了链：首条序号 = %d，期望 1", rec.Seq)
	}
	if len(l.Anchors()) != 0 {
		t.Fatalf("被拒绝的追加改变了锚点")
	}
}

func TestTamperedPayload(t *testing.T) {
	_, records := buildLog(t, payloads(8)...)
	records[2].Payload = []byte("forged")
	r := VerifyChain(records, anchorsOf(8), nil)
	logResult(t, "第 3 条负载被篡改", r)
	if r.OK || r.Category != CatContentModified || r.Position != 3 {
		t.Fatalf("期望 内容被改 @ 3，得到 %s @ %d", r.Category, r.Position)
	}
}

func TestDeletedMiddleRecord(t *testing.T) {
	_, records := buildLog(t, payloads(8)...)
	deleted := records[4]
	records = append(records[:4], records[5:]...)
	r := VerifyChain(records, anchorsOf(8), nil)
	logResult(t, fmt.Sprintf("删除第 5 条（序号 %d）", deleted.Seq), r)
	if r.OK || r.Category != CatSeqDiscontinuity || r.Position != 5 {
		t.Fatalf("期望 序号不连续 @ 5，得到 %s @ %d", r.Category, r.Position)
	}
}

func TestSwappedAdjacentRecords(t *testing.T) {
	_, records := buildLog(t, payloads(8)...)
	records[2], records[3] = records[3], records[2]
	r := VerifyChain(records, anchorsOf(8), nil)
	logResult(t, "交换第 3、4 条", r)
	if r.OK || r.Category != CatSeqDiscontinuity || r.Position != 3 {
		t.Fatalf("期望 序号不连续 @ 3，得到 %s @ %d", r.Category, r.Position)
	}
}

func TestRecomputedSuffixCaughtByAnchor(t *testing.T) {
	_, records := buildLog(t, payloads(8)...)
	// 篡改第 3 条负载并重算第 3 条起全部摘要与前摘要，
	// 逐条检查被绕过，但序号 4、8 的锚点仍记录原摘要。
	records[2].Payload = []byte("forged")
	for i := 2; i < len(records); i++ {
		prev := GenesisDigest
		if i > 0 {
			prev = records[i-1].Digest
		}
		records[i].Prev = prev
		records[i].Digest = DefaultDigestFunc(records[i].Seq, records[i].Payload, prev)
	}
	r := VerifyChain(records, anchorsOf(8), nil)
	logResult(t, "第 3 条负载被篡改且后缀摘要全部重算", r)
	if r.OK || r.Category != CatAnchorMismatch || r.Position != 4 {
		t.Fatalf("期望 与锚点不符 @ 4，得到 %s @ %d", r.Category, r.Position)
	}
}

func TestTruncationDetected(t *testing.T) {
	_, records := buildLog(t, payloads(10)...)
	truncated := records[:6] // 链长 6 < 最大锚点序号 8
	r := VerifyChain(truncated, anchorsOf(8), nil)
	logResult(t, "链从 10 条截断到 6 条（最大锚点序号 8）", r)
	if r.OK || r.Category != CatTruncated || r.Position != 7 {
		t.Fatalf("期望 尾部截断 @ 7（链长加一），得到 %s @ %d", r.Category, r.Position)
	}
}

func TestTruncationBeyondMaxAnchorUndetectable(t *testing.T) {
	_, records := buildLog(t, payloads(10)...)
	truncated := records[:9] // 链长 9 >= 最大锚点序号 8，无法发现
	r := VerifyChain(truncated, anchorsOf(8), nil)
	logResult(t, "链从 10 条截断到 9 条（最大锚点序号 8）", r)
	if !r.OK {
		t.Fatalf("截断到最大锚点之后应按通过处理，得到 %s @ %d", r.Category, r.Position)
	}
	if r.AnchoredPrefix != 8 {
		t.Fatalf("已锚定前缀 = %d，期望 8", r.AnchoredPrefix)
	}
}

func TestAnchorProblemAtSmallerPositionWins(t *testing.T) {
	_, records := buildLog(t, payloads(8)...)
	// 篡改第 4 条（锚点位）负载并重算其摘要，但不修第 5 条：
	// 逐条检查在第 5 条报链断裂，锚点在第 4 条不符，报位置较小者。
	records[3].Payload = []byte("forged")
	records[3].Digest = DefaultDigestFunc(records[3].Seq, records[3].Payload, records[3].Prev)
	r := VerifyChain(records, anchorsOf(8), nil)
	logResult(t, "第 4 条负载被篡改且仅重算第 4 条摘要", r)
	if r.OK || r.Category != CatAnchorMismatch || r.Position != 4 {
		t.Fatalf("期望 与锚点不符 @ 4，得到 %s @ %d", r.Category, r.Position)
	}
}

func TestSamePositionPrefersRecordCategory(t *testing.T) {
	_, records := buildLog(t, payloads(8)...)
	// 直接改第 4 条（锚点位）的自报摘要：逐条检查与锚点核对同在第 4 条发现问题。
	records[3].Digest[0] ^= 0xff
	r := VerifyChain(records, anchorsOf(8), nil)
	logResult(t, "第 4 条自报摘要被改写", r)
	if r.OK || r.Category != CatContentModified || r.Position != 4 {
		t.Fatalf("位置相同应取逐条检查类别（内容被改 @ 4），得到 %s @ %d", r.Category, r.Position)
	}
}

func TestDeterministicAppendSequence(t *testing.T) {
	l1, r1 := buildLog(t, payloads(12)...)
	l2, r2 := buildLog(t, payloads(12)...)
	if !reflect.DeepEqual(r1, r2) {
		t.Fatalf("相同追加序列得到不同的链")
	}
	if !reflect.DeepEqual(l1.Anchors(), l2.Anchors()) {
		t.Fatalf("相同追加序列得到不同的锚点")
	}
	t.Logf("输入: 两个日志各按序追加相同 12 条；输出: 链与锚点完全一致")
}

func TestConcurrentAppendAndVerify(t *testing.T) {
	const writers = 8
	const perWriter = 50
	l, err := NewLog(testInterval, 1024, nil, nil)
	if err != nil {
		t.Fatalf("NewLog: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 并发校验：任一时刻看到的链都必须是某个自洽前缀，
	// 且链长以内每个 A 的倍数序号都已有锚点。
	var checkErr sync.Map
	var checkerWg sync.WaitGroup
	checkerWg.Add(1)
	go func() {
		defer checkerWg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			n := l.Len()
			anchorSeqs := map[uint64]bool{}
			for _, a := range l.Anchors() {
				anchorSeqs[a.Seq] = true
			}
			for seq := uint64(testInterval); seq <= uint64(n); seq += testInterval {
				if !anchorSeqs[seq] {
					checkErr.Store("anchor", fmt.Sprintf("链长 %d 时序号 %d 无锚点", n, seq))
					return
				}
			}
			if r := l.Verify(); !r.OK {
				checkErr.Store("verify", fmt.Sprintf("并发校验失败: %s @ %d", r.Category, r.Position))
				return
			}
		}
	}()

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				if _, err := l.Append([]byte(fmt.Sprintf("w%d-%d", id, i))); err != nil {
					t.Errorf("Append: %v", err)
					return
				}
			}
		}(w)
	}

	wg.Wait()
	close(stop)
	checkerWg.Wait()

	total := writers * perWriter
	if n := l.Len(); n != total {
		t.Fatalf("链长 = %d，期望 %d", n, total)
	}
	// 序号连续无重复无空洞。
	seen := make(map[uint64]bool, total)
	for _, rec := range l.Snapshot() {
		if rec.Seq < 1 || rec.Seq > uint64(total) || seen[rec.Seq] {
			t.Fatalf("序号异常: %d", rec.Seq)
		}
		seen[rec.Seq] = true
	}
	// 链长以内每个 A 的倍数都有锚点。
	anchorSeqs := map[uint64]bool{}
	for _, a := range l.Anchors() {
		anchorSeqs[a.Seq] = true
	}
	for seq := uint64(testInterval); seq <= uint64(total); seq += testInterval {
		if !anchorSeqs[seq] {
			t.Fatalf("序号 %d 缺少锚点", seq)
		}
	}
	r := l.Verify()
	logResult(t, fmt.Sprintf("%d 个 goroutine 各追加 %d 条", writers, perWriter), r)
	if !r.OK {
		t.Fatalf("并发追加后校验失败: %s @ %d", r.Category, r.Position)
	}
	checkErr.Range(func(k, v any) bool {
		t.Fatalf("并发检查失败(%v): %v", k, v)
		return true
	})
}

// anchorsOf 返回一条未经篡改、长度为 total 的链应发布的锚点，
// 通过对相同负载序列重新追加获得（确定性保证一致）。
func anchorsOf(total int) []Anchor {
	l, err := NewLog(testInterval, 1024, nil, nil)
	if err != nil {
		panic(err)
	}
	for _, p := range payloads(total) {
		if _, err := l.Append(p); err != nil {
			panic(err)
		}
	}
	return l.Anchors()
}
