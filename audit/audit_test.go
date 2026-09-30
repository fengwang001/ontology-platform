package audit

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

const testMaxPayload = 1024

// newChain 构建一条含 n 条记录、锚点间隔为 interval 的链。
func newChain(t *testing.T, interval, n int) (*Verifier, *MemoryAnchorStore) {
	t.Helper()
	anchors := NewMemoryAnchorStore()
	v, err := NewVerifier(interval, testMaxPayload, SHA256Hash, anchors)
	if err != nil {
		t.Fatalf("创建校验器失败: %v", err)
	}
	for i := 1; i <= n; i++ {
		if _, err := v.Append([]byte(fmt.Sprintf("payload-%d", i))); err != nil {
			t.Fatalf("追加第 %d 条失败: %v", i, err)
		}
	}
	return v, anchors
}

func logResult(t *testing.T, input string, r Result) {
	t.Helper()
	if r.OK {
		t.Logf("输入: %s | 输出: 通过 | 判定依据: 逐条检查与锚点核对均无问题，已被锚点确认的前缀长度=%d", input, r.AnchoredPrefix)
	} else {
		t.Logf("输入: %s | 输出: 位置=%d 类别=%s | 判定依据: 两段检查中位置最小的问题", input, r.Position, r.Category)
	}
}

func expect(t *testing.T, r Result, cat Category, pos uint64) {
	t.Helper()
	if r.OK || r.Category != cat || r.Position != pos {
		t.Fatalf("期望 位置=%d 类别=%s，得到 %+v", pos, cat, r)
	}
}

func TestTamperedPayload(t *testing.T) {
	v, anchors := newChain(t, 2, 6)
	records := v.Snapshot()
	records[2].Payload = []byte("tampered")
	r := v.verify(records, anchors.Snapshot())
	logResult(t, "改动第 3 条负载", r)
	expect(t, r, ContentTampered, 3)
}

func TestDeletedMiddleRecord(t *testing.T) {
	v, anchors := newChain(t, 2, 6)
	records := v.Snapshot()
	records = append(records[:2], records[3:]...)
	r := v.verify(records, anchors.Snapshot())
	logResult(t, "删除第 3 条", r)
	expect(t, r, SeqDiscontinuous, 3)
}

func TestSwappedAdjacentRecords(t *testing.T) {
	v, anchors := newChain(t, 2, 6)
	records := v.Snapshot()
	records[2], records[3] = records[3], records[2]
	r := v.verify(records, anchors.Snapshot())
	logResult(t, "交换第 3、4 条", r)
	expect(t, r, SeqDiscontinuous, 3)
}

// TestRecomputedSuffixCaughtByAnchor 攻击者改动一条负载后重算整条后缀，
// 逐条检查全部通过，但外部锚点仍保留原摘要，被锚点核对抓住。
func TestRecomputedSuffixCaughtByAnchor(t *testing.T) {
	v, anchors := newChain(t, 2, 6)
	records := v.Snapshot()
	records[2].Payload = []byte("forged")
	for i := 2; i < len(records); i++ {
		records[i].Digest = SHA256Hash(records[i].Seq, records[i].Payload, records[i].Prev)
		if i+1 < len(records) {
			records[i+1].Prev = records[i].Digest
		}
	}
	r := v.verify(records, anchors.Snapshot())
	logResult(t, "改动第 3 条负载并重算后缀摘要", r)
	expect(t, r, AnchorMismatch, 4)
}

func TestTruncateBeforeMaxAnchor(t *testing.T) {
	v, anchors := newChain(t, 2, 7)
	records := v.Snapshot()
	r := v.verify(records[:5], anchors.Snapshot())
	logResult(t, "7 条截断到 5 条（最大锚点序号 6）", r)
	expect(t, r, TailTruncated, 6)
}

// TestTruncateAfterMaxAnchor 截断后链长仍不小于最大锚点序号，无法发现，按通过处理。
func TestTruncateAfterMaxAnchor(t *testing.T) {
	v, anchors := newChain(t, 2, 7)
	records := v.Snapshot()
	r := v.verify(records[:6], anchors.Snapshot())
	logResult(t, "7 条截断到 6 条（最大锚点序号 6）", r)
	if !r.OK || r.AnchoredPrefix != 6 {
		t.Fatalf("期望通过且 AnchoredPrefix=6，得到 %+v", r)
	}
}

// TestChainBrokenCategory 篡改前摘要并同步重算自报摘要，触发链断裂类别。
func TestChainBrokenCategory(t *testing.T) {
	v, anchors := newChain(t, 2, 6)
	records := v.Snapshot()
	records[3].Prev = "forged-prev"
	records[3].Digest = SHA256Hash(records[3].Seq, records[3].Payload, records[3].Prev)
	r := v.verify(records, anchors.Snapshot())
	logResult(t, "篡改第 4 条前摘要并重算其自报摘要", r)
	expect(t, r, ChainBroken, 4)
}

// TestCategoryPriority 同一条记录多项问题成立时按优先级取类别。
func TestCategoryPriority(t *testing.T) {
	v, anchors := newChain(t, 2, 6)

	records := v.Snapshot()
	records[2].Payload = []byte("tampered")
	records[2].Seq = 99
	r := v.verify(records, anchors.Snapshot())
	logResult(t, "第 3 条负载被改且序号错误", r)
	expect(t, r, ContentTampered, 3)

	records = v.Snapshot()
	records[2].Seq = 99
	records[2].Prev = "forged-prev"
	records[2].Digest = SHA256Hash(records[2].Seq, records[2].Payload, records[2].Prev)
	r = v.verify(records, anchors.Snapshot())
	logResult(t, "第 3 条序号错误且前摘要错误", r)
	expect(t, r, SeqDiscontinuous, 3)
}

// TestMergeChainAndAnchorFindings 两段都有问题时报位置较小者，位置相同取逐条检查的类别。
func TestMergeChainAndAnchorFindings(t *testing.T) {
	v, anchors := newChain(t, 2, 6)

	// 链问题在 3，锚点问题在 4：报 3。
	records := v.Snapshot()
	records[2].Payload = []byte("tampered")
	records[3].Digest = "forged"
	r := v.verify(records, anchors.Snapshot())
	logResult(t, "第 3 条内容被改且第 4 条与锚点不符", r)
	expect(t, r, ContentTampered, 3)

	// 链问题在 5，锚点问题在 4：报 4。
	// 先改动第 4 条负载并重算后缀（纯锚点不符），再单独改动第 5 条负载。
	records = v.Snapshot()
	records[3].Payload = []byte("forged")
	for i := 3; i < len(records); i++ {
		records[i].Digest = SHA256Hash(records[i].Seq, records[i].Payload, records[i].Prev)
		if i+1 < len(records) {
			records[i+1].Prev = records[i].Digest
		}
	}
	records[4].Payload = []byte("tampered")
	r = v.verify(records, anchors.Snapshot())
	logResult(t, "第 5 条内容被改且第 4 条与锚点不符", r)
	expect(t, r, AnchorMismatch, 4)

	// 位置相同（都在 4）：取逐条检查的类别。
	records = v.Snapshot()
	records[3].Payload = []byte("tampered")
	records[3].Digest = "forged"
	r = v.verify(records, anchors.Snapshot())
	logResult(t, "第 4 条内容被改且同位置与锚点不符", r)
	expect(t, r, ContentTampered, 4)
}

func TestOKReportsAnchoredPrefix(t *testing.T) {
	v, _ := newChain(t, 2, 7)
	r := v.Verify()
	logResult(t, "完好链 7 条，A=2", r)
	if !r.OK || r.AnchoredPrefix != 6 {
		t.Fatalf("期望通过且 AnchoredPrefix=6，得到 %+v", r)
	}

	v2, _ := newChain(t, 10, 5)
	r2 := v2.Verify()
	logResult(t, "完好链 5 条，A=10（无锚点）", r2)
	if !r2.OK || r2.AnchoredPrefix != 0 {
		t.Fatalf("期望通过且 AnchoredPrefix=0，得到 %+v", r2)
	}
}

func TestAppendRejected(t *testing.T) {
	v, anchors := newChain(t, 2, 3)
	before := v.Snapshot()

	if _, err := v.Append(nil); !errors.Is(err, ErrEmptyPayload) {
		t.Fatalf("空负载应报 ErrEmptyPayload，得到 %v", err)
	}
	tooLarge := make([]byte, testMaxPayload+1)
	if _, err := v.Append(tooLarge); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("超长负载应报 ErrPayloadTooLarge，得到 %v", err)
	}

	after := v.Snapshot()
	if len(after) != len(before) {
		t.Fatalf("被拒绝的追加不得占用序号：链长 %d -> %d", len(before), len(after))
	}
	if got := len(anchors.Snapshot()); got != 1 {
		t.Fatalf("被拒绝的追加不得改变锚点：锚点数=%d", got)
	}
	r := v.Verify()
	logResult(t, "空负载与超长负载被拒绝后的链", r)
	if !r.OK {
		t.Fatalf("链应保持完好，得到 %+v", r)
	}
}

func TestNonPositiveIntervalRejected(t *testing.T) {
	for _, a := range []int{0, -1, -100} {
		if _, err := NewVerifier(a, testMaxPayload, SHA256Hash, NewMemoryAnchorStore()); err == nil {
			t.Fatalf("A=%d 应拒绝创建", a)
		}
	}
}

// TestConcurrentAppendAndVerify 并发追加与并发校验：
// 序号连续无重复无空洞，链长以内每个 A 的倍数都有锚点，校验恒看到自洽前缀。
func TestConcurrentAppendAndVerify(t *testing.T) {
	const writers = 8
	const perWriter = 50
	anchors := NewMemoryAnchorStore()
	v, err := NewVerifier(3, testMaxPayload, SHA256Hash, anchors)
	if err != nil {
		t.Fatalf("创建校验器失败: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	verifyErrs := make(chan error, 64)

	// 并发校验：任何时刻都必须通过（看到自洽前缀）。
	var verifyWg sync.WaitGroup
	for w := 0; w < 2; w++ {
		verifyWg.Add(1)
		go func() {
			defer verifyWg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if r := v.Verify(); !r.OK {
					verifyErrs <- fmt.Errorf("并发校验失败: %+v", r)
					return
				}
			}
		}()
	}

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				if _, err := v.Append([]byte(fmt.Sprintf("w%d-%d", id, i))); err != nil {
					verifyErrs <- fmt.Errorf("追加失败: %w", err)
					return
				}
			}
		}(w)
	}

	wg.Wait()
	close(stop)
	verifyWg.Wait()
	close(verifyErrs)
	for err := range verifyErrs {
		t.Fatal(err)
	}

	total := writers * perWriter
	records := v.Snapshot()
	if len(records) != total {
		t.Fatalf("链长应为 %d，得到 %d", total, len(records))
	}
	for i, rec := range records {
		if rec.Seq != uint64(i)+1 {
			t.Fatalf("位置 %d 序号为 %d，存在重复或空洞", i+1, rec.Seq)
		}
	}
	snap := anchors.Snapshot()
	wantAnchors := total / 3
	if len(snap) != wantAnchors {
		t.Fatalf("锚点数应为 %d，得到 %d", wantAnchors, len(snap))
	}
	for i, a := range snap {
		wantSeq := uint64(i+1) * 3
		if a.Seq != wantSeq {
			t.Fatalf("第 %d 个锚点序号应为 %d，得到 %d", i, wantSeq, a.Seq)
		}
		if records[a.Seq-1].Digest != a.Digest {
			t.Fatalf("锚点 %d 与链中记录摘要不符", a.Seq)
		}
	}
	r := v.Verify()
	logResult(t, fmt.Sprintf("%d 个写者并发追加共 %d 条", writers, total), r)
	if !r.OK || r.AnchoredPrefix != uint64(wantAnchors)*3 {
		t.Fatalf("并发追加后校验应通过，得到 %+v", r)
	}
}

// TestDeterministic 相同的追加序列得到相同的链与锚点。
func TestDeterministic(t *testing.T) {
	v1, a1 := newChain(t, 2, 10)
	v2, a2 := newChain(t, 2, 10)
	r1, r2 := v1.Snapshot(), v2.Snapshot()
	for i := range r1 {
		if r1[i].Seq != r2[i].Seq || r1[i].Digest != r2[i].Digest ||
			r1[i].Prev != r2[i].Prev || string(r1[i].Payload) != string(r2[i].Payload) {
			t.Fatalf("第 %d 条不一致: %+v != %+v", i+1, r1[i], r2[i])
		}
	}
	s1, s2 := a1.Snapshot(), a2.Snapshot()
	if len(s1) != len(s2) {
		t.Fatalf("锚点数不一致: %d != %d", len(s1), len(s2))
	}
	for i := range s1 {
		if s1[i] != s2[i] {
			t.Fatalf("第 %d 个锚点不一致: %+v != %+v", i, s1[i], s2[i])
		}
	}
	t.Logf("输入: 相同追加序列两次 | 输出: 链与锚点完全一致 | 判定依据: 确定性哈希函数")
}

// TestAnchorStoreMonotonic 锚点只增不改。
func TestAnchorStoreMonotonic(t *testing.T) {
	s := NewMemoryAnchorStore()
	if err := s.Publish(Anchor{Seq: 2, Digest: "d2"}); err != nil {
		t.Fatalf("首次发布失败: %v", err)
	}
	if err := s.Publish(Anchor{Seq: 2, Digest: "dx"}); err == nil {
		t.Fatal("重复序号应被拒绝")
	}
	if err := s.Publish(Anchor{Seq: 1, Digest: "d1"}); err == nil {
		t.Fatal("回退序号应被拒绝")
	}
	if got := s.Snapshot(); len(got) != 1 || got[0].Digest != "d2" {
		t.Fatalf("锚点只增不改，得到 %+v", got)
	}
}
