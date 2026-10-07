package snapshot_test

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/snapshot"
)

func sp(s string) *string { return &s }

// generateStream 生成一条“生成时自洽”的记录序列，保证所有链接引用都
// 指向此前已出现的对象标识。
func generateStream(rng *rand.Rand) []snapshot.Record {
	n := 1 + rng.Intn(20)
	var records []snapshot.Record
	var liveIDs []string
	types := []string{"Person", "Org", "Device", "Event"}
	linkTypes := []string{"knows", "owns", "partOf", "triggers"}

	for i := 0; i < n; i++ {
		isObject := len(liveIDs) < 2 || rng.Intn(2) == 0
		if isObject {
			id := fmt.Sprintf("o%d", rng.Intn(n+2))
			records = append(records, snapshot.Record{
				Kind: snapshot.KindObject,
				ID:   sp(id),
				Type: sp(types[rng.Intn(len(types))]),
			})
			// 朴素模型同样容忍“同标识同类型”的冗余重复；生成器只在
			// 新标识时扩充可引用集合，避免随机制造类型冲突。
			dup := false
			for _, x := range liveIDs {
				if x == id {
					dup = true
					break
				}
			}
			if !dup {
				liveIDs = append(liveIDs, id)
			}
		} else {
			src := liveIDs[rng.Intn(len(liveIDs))]
			dst := liveIDs[rng.Intn(len(liveIDs))]
			dir := snapshot.DirForward
			if rng.Intn(2) == 1 {
				dir = snapshot.DirReverse
			}
			records = append(records, snapshot.Record{
				Kind:      snapshot.KindLink,
				SourceID:  sp(src),
				TargetID:  sp(dst),
				LinkType:  sp(linkTypes[rng.Intn(len(linkTypes))]),
				Direction: dir,
			})
		}
	}
	return records
}

// injectCorruption 在 idx 处注入指定类别的损坏。
func injectCorruption(rng *rand.Rand, records []snapshot.Record, idx int, mode int) []snapshot.Record {
	out := make([]snapshot.Record, len(records))
	copy(out, records)
	r := out[idx]
	switch mode {
	case 0: // 对象字段损坏
		r.Kind = snapshot.KindObject
		r.ID = sp(fmt.Sprintf("x%d", idx))
		r.Type = nil
	case 1: // 链接字段损坏（方向非法）
		r.Kind = snapshot.KindLink
		r.SourceID = sp("a")
		r.TargetID = sp("b")
		r.LinkType = sp("lt")
		r.Direction = snapshot.DirUnknown
	case 2: // 链接字段损坏（字段缺失）
		r.Kind = snapshot.KindLink
		r.SourceID = nil
		r.TargetID = sp("b")
		r.LinkType = sp("lt")
		r.Direction = snapshot.DirForward
	case 3: // 引用前方缺失：指向从未出现的标识
		r.Kind = snapshot.KindLink
		r.SourceID = sp(fmt.Sprintf("ghost_%d_%d", idx, rng.Intn(1_000_000)))
		r.TargetID = sp(fmt.Sprintf("ghost2_%d_%d", idx, rng.Intn(1_000_000)))
		r.LinkType = sp("lt")
		r.Direction = snapshot.DirForward
	case 4: // 无法识别的记录种类
		r.Kind = snapshot.KindUnknown
		r.ID = sp("z")
	case 5: // 对象重复但类型冲突：使用此前出现过的标识 + 新类型
		r.Kind = snapshot.KindObject
		r.ID = sp(previousObjectID(records, idx))
		r.Type = sp("ConflictingType")
	}
	out[idx] = r
	return out
}

func previousObjectID(records []snapshot.Record, idx int) string {
	for j := idx - 1; j >= 0; j-- {
		if records[j].Kind == snapshot.KindObject && records[j].ID != nil {
			return *records[j].ID
		}
	}
	return "o0"
}

func toNaive(records []snapshot.Record) []snapshotRecord {
	out := make([]snapshotRecord, len(records))
	for i, r := range records {
		nr := snapshotRecord{
			id: r.ID, typ: r.Type, src: r.SourceID, dst: r.TargetID,
			linkType: r.LinkType, dir: r.Direction,
		}
		switch r.Kind {
		case snapshot.KindObject:
			nr.kind = "object"
		case snapshot.KindLink:
			nr.kind = "link"
		default:
			nr.kind = "unknown"
		}
		out[i] = nr
	}
	return out
}

func reasonName(r snapshot.Reason) string {
	switch r {
	case snapshot.ReasonObjectCorrupt:
		return "object_corrupt"
	case snapshot.ReasonLinkCorrupt:
		return "link_corrupt"
	case snapshot.ReasonLinkReferenceMissing:
		return "reference_missing"
	default:
		return "none"
	}
}

// TestRandomDifferential 以随机生成、随机注入损坏位置的序列对照两个独立实现，
// 并逐次记录输入、输出、最大可恢复前缀与损坏原因分类。
func TestRandomDifferential(t *testing.T) {
	const trials = 2000
	for trial := 0; trial < trials; trial++ {
		rng := rand.New(rand.NewSource(int64(trial)*1000003 + 7))
		base := generateStream(rng)

		var records []snapshot.Record
		injectedAt := -1
		injectedMode := -1
		if rng.Intn(5) != 0 { // 80% 试验注入一处损坏
			injectedAt = rng.Intn(len(base))
			injectedMode = rng.Intn(6)
			records = injectCorruption(rng, base, injectedAt, injectedMode)
		} else {
			records = base
		}

		got := snapshot.ValidateSlice(records)
		want := naiveValidate(toNaive(records))

		if (got.Status == snapshot.StatusTruncated) != want.truncated ||
			got.PrefixLen != want.prefix || got.BadIndex != want.bad ||
			reasonName(got.Reason) != want.reason {
			t.Fatalf("trial %d mismatch:\ninput=%v\ngot = status(trunc=%v) prefix=%d bad=%d reason=%s\nwant=%+v\ninjectAt=%d mode=%d",
				trial, records, got.Status == snapshot.StatusTruncated,
				got.PrefixLen, got.BadIndex, reasonName(got.Reason), want,
				injectedAt, injectedMode)
		}

		// 审计记录：输入长度、注入点、输出、前缀长度、原因分类。
		t.Logf("trial=%d len=%d injectAt=%d mode=%d result=%s prefix=%d bad=%d reason=%s",
			trial, len(records), injectedAt, injectedMode,
			map[bool]string{true: "truncated", false: "complete"}[got.Status == snapshot.StatusTruncated],
			got.PrefixLen, got.BadIndex, reasonName(got.Reason))
	}
}

// TestDeterministicAndConcurrent：同一不变序列重复校验结果完全相同，
// 且多个独立流的并发校验互不干扰。
func TestDeterministicAndConcurrent(t *testing.T) {
	records := []snapshot.Record{
		{Kind: snapshot.KindObject, ID: sp("a"), Type: sp("Person")},
		{Kind: snapshot.KindObject, ID: sp("b"), Type: sp("Org")},
		{Kind: snapshot.KindLink, SourceID: sp("a"), TargetID: sp("b"),
			LinkType: sp("knows"), Direction: snapshot.DirForward},
		{Kind: snapshot.KindLink, SourceID: sp("a"), TargetID: sp("ghost"),
			LinkType: sp("knows"), Direction: snapshot.DirReverse},
	}
	first := snapshot.ValidateSlice(records)
	for i := 0; i < 10; i++ {
		if got := snapshot.ValidateSlice(records); got != first {
			t.Fatalf("repeated validation differs: %v vs %v", got, first)
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := snapshot.Validate(snapshot.NewSliceReader(records))
			if got != first {
				errs <- fmt.Errorf("concurrent validation mismatch: %v", got)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestEmptyVsZeroPrefix 区分“空序列完整”与“零长度前缀”。
func TestEmptyVsZeroPrefix(t *testing.T) {
	empty := snapshot.ValidateSlice(nil)
	if empty.Status != snapshot.StatusComplete || empty.PrefixLen != 0 || empty.Reason != snapshot.ReasonNone {
		t.Fatalf("empty stream = %v, want complete", empty)
	}
	firstBad := snapshot.ValidateSlice([]snapshot.Record{
		{Kind: snapshot.KindObject, Type: sp("Person")},
	})
	if firstBad.Status != snapshot.StatusTruncated || firstBad.PrefixLen != 0 ||
		firstBad.BadIndex != 0 || firstBad.Reason != snapshot.ReasonObjectCorrupt {
		t.Fatalf("first-record corruption = %v, want truncated zero-length prefix with reason", firstBad)
	}
	if empty.Status == firstBad.Status {
		t.Fatal("empty-stream-complete must not share the status of zero-length prefix")
	}
}
