package snapshot

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// naiveResult 是朴素逐条校验模型的输出，与 Validate 的 Result 对应。
type naiveResult struct {
	complete   bool
	prefixLen  int
	corruption Corruption
	badIndex   int
}

// naiveValidate 是独立实现的朴素校验模型：
// 对每条记录，重新从头扫描它之前的所有记录来判定其合法性，
// 找到第一条损坏记录即停止。时间复杂度 O(n^2)，用于对照验证。
func naiveValidate(records []Record) naiveResult {
	for i := range records {
		if c, bad := naiveCheckRecord(records, i); bad {
			return naiveResult{complete: false, prefixLen: i, corruption: c, badIndex: i}
		}
	}
	return naiveResult{complete: true, prefixLen: len(records), corruption: CorruptionNone, badIndex: -1}
}

// naiveCheckRecord 判定 records[i] 是否损坏，只依赖 records[:i] 重新构建的状态。
func naiveCheckRecord(records []Record, i int) (Corruption, bool) {
	declared := make(map[string]string)
	for _, rec := range records[:i] {
		if obj, ok := rec.(ObjectRecord); ok {
			declared[obj.ObjectID] = obj.ObjectType
		}
	}
	switch r := records[i].(type) {
	case ObjectRecord:
		if r.ObjectID == "" || r.ObjectType == "" {
			return CorruptionObjectFields, true
		}
		if prev, ok := declared[r.ObjectID]; ok && prev != r.ObjectType {
			return CorruptionObjectFields, true
		}
	case LinkRecord:
		if r.SourceID == "" || r.TargetID == "" || r.LinkType == "" || !r.Direction.valid() {
			return CorruptionLinkFields, true
		}
		if _, ok := declared[r.SourceID]; !ok {
			return CorruptionMissingReference, true
		}
		if _, ok := declared[r.TargetID]; !ok {
			return CorruptionMissingReference, true
		}
	default:
		return CorruptionObjectFields, true
	}
	return CorruptionNone, false
}

// assertResult 校验结果与朴素模型一致，且内部读取度量不超过前缀长度加一。
func assertResult(t *testing.T, records []Record, got Result) {
	t.Helper()
	want := naiveValidate(records)
	if got.Complete != want.complete ||
		got.PrefixLen != want.prefixLen ||
		got.Corruption != want.corruption ||
		got.BadIndex != want.badIndex {
		t.Fatalf("结果与朴素模型不一致: got %+v, want %+v", got, want)
	}
	if got.recordsRead > got.PrefixLen+1 {
		t.Fatalf("读取度量越界: recordsRead=%d > PrefixLen+1=%d", got.recordsRead, got.PrefixLen+1)
	}
	if got.Complete && got.Corruption != CorruptionNone {
		t.Fatalf("完整序列不得携带损坏原因: %+v", got)
	}
	if !got.Complete && got.Corruption == CorruptionNone {
		t.Fatalf("截断结果必须给出损坏原因: %+v", got)
	}
}

func obj(id, typ string) ObjectRecord { return ObjectRecord{ObjectID: id, ObjectType: typ} }

func link(src, dst string) LinkRecord {
	return LinkRecord{SourceID: src, TargetID: dst, LinkType: "owns", Direction: DirectionForward}
}

func TestBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		records []Record
	}{
		{"空序列判定为完整", nil},
		{"第一条记录即对象损坏", []Record{obj("", "Person"), obj("a", "Person")}},
		{"第一条记录即链接字段损坏", []Record{LinkRecord{SourceID: "a", TargetID: "b"}}},
		{"第一条记录即引用缺失", []Record{link("a", "b")}},
		{"中间位置对象类型冲突", []Record{obj("a", "Person"), obj("b", "Company"), obj("a", "Robot")}},
		{"中间位置链接方向非法", []Record{obj("a", "Person"), LinkRecord{SourceID: "a", TargetID: "a", LinkType: "knows", Direction: "sideways"}}},
		{"中间位置引用缺失", []Record{obj("a", "Person"), link("a", "ghost")}},
		{"最后一条记录损坏", []Record{obj("a", "Person"), obj("b", "Company"), link("a", "ghost")}},
		{"完整序列", []Record{obj("a", "Person"), obj("b", "Company"), link("a", "b"), link("b", "a")}},
		{"单条完好对象记录", []Record{obj("a", "Person")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertResult(t, tc.records, Validate(tc.records))
		})
	}
}

func TestEmptySequenceIsCompleteNotZeroPrefix(t *testing.T) {
	got := Validate(nil)
	if !got.Complete || got.Corruption != CorruptionNone || got.BadIndex != -1 {
		t.Fatalf("空序列必须判定为完整: %+v", got)
	}
	corrupt := Validate([]Record{obj("", "Person")})
	if corrupt.Complete || corrupt.PrefixLen != 0 || corrupt.Corruption != CorruptionObjectFields {
		t.Fatalf("首条损坏须返回零长前缀且保留原因: %+v", corrupt)
	}
}

func TestCorruptAtLastDiffersFromComplete(t *testing.T) {
	good := []Record{obj("a", "Person"), obj("b", "Company"), link("a", "b")}
	bad := []Record{obj("a", "Person"), obj("b", "Company"), link("a", "ghost")}
	if got := Validate(good); !got.Complete || got.PrefixLen != 3 {
		t.Fatalf("完整序列判定错误: %+v", got)
	}
	got := Validate(bad)
	if got.Complete || got.PrefixLen != 2 || got.Corruption != CorruptionMissingReference {
		t.Fatalf("末条损坏与完整混淆: %+v", got)
	}
}

func TestDuplicateObjectRecords(t *testing.T) {
	same := []Record{obj("a", "Person"), obj("a", "Person"), link("a", "a")}
	if got := Validate(same); !got.Complete {
		t.Fatalf("同类型重复声明应视为冗余: %+v", got)
	}
	diff := []Record{obj("a", "Person"), obj("a", "Robot")}
	got := Validate(diff)
	if got.Complete || got.PrefixLen != 1 || got.Corruption != CorruptionObjectFields {
		t.Fatalf("类型冲突的重复声明应判对象损坏: %+v", got)
	}
}

func TestChainFromCorruptObject(t *testing.T) {
	// 第二条对象记录自身损坏，其后的链接虽然引用了它，
	// 但截断原因必须归属对象记录本身，且扫描不得继续。
	records := []Record{obj("a", "Person"), obj("a", "Robot"), link("a", "a")}
	got := Validate(records)
	if got.Complete || got.BadIndex != 1 || got.Corruption != CorruptionObjectFields {
		t.Fatalf("连锁情形截断位置或原因错误: %+v", got)
	}
	if got.recordsRead != 2 {
		t.Fatalf("不得在截断后继续读取: recordsRead=%d", got.recordsRead)
	}
}

func TestCorruptionPriority(t *testing.T) {
	// 链接记录同时字段损坏且引用缺失：只报字段损坏。
	records := []Record{obj("a", "Person"), LinkRecord{SourceID: "ghost", TargetID: "", LinkType: "", Direction: "bad"}}
	got := Validate(records)
	if got.Corruption != CorruptionLinkFields {
		t.Fatalf("字段损坏应优先于引用缺失: %+v", got)
	}
}

func TestRepeatAndConcurrentValidation(t *testing.T) {
	records := []Record{obj("a", "Person"), obj("b", "Company"), link("a", "b"), link("a", "ghost")}
	snapshot := append([]Record(nil), records...)
	want := Validate(records)

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if got := Validate(records); got != want {
					t.Errorf("并发校验结果不一致: got %+v, want %+v", got, want)
					return
				}
			}
		}()
	}
	wg.Wait()

	if !reflect.DeepEqual(records, snapshot) {
		t.Fatalf("校验不得修改输入: got %v, want %v", records, snapshot)
	}
}

// genValidSequence 随机生成一段自洽的记录序列。
func genValidSequence(rng *rand.Rand, n int) []Record {
	records := make([]Record, 0, n)
	var ids []string
	for i := 0; i < n; i++ {
		if len(ids) == 0 || rng.Intn(2) == 0 {
			id := fmt.Sprintf("obj-%d", len(ids))
			records = append(records, obj(id, fmt.Sprintf("Type%d", rng.Intn(4))))
			ids = append(ids, id)
			continue
		}
		src := ids[rng.Intn(len(ids))]
		dst := ids[rng.Intn(len(ids))]
		dir := DirectionForward
		if rng.Intn(2) == 0 {
			dir = DirectionReverse
		}
		records = append(records, LinkRecord{SourceID: src, TargetID: dst, LinkType: "rel", Direction: dir})
	}
	return records
}

// injectCorruption 在随机位置注入一种随机损坏。
func injectCorruption(rng *rand.Rand, records []Record) {
	if len(records) == 0 {
		return
	}
	i := rng.Intn(len(records))
	switch rng.Intn(4) {
	case 0: // 对象记录字段缺失
		records[i] = obj("", "Person")
	case 1: // 对象类型冲突（引用的标识未必出现过，朴素模型同样处理）
		records[i] = obj(fmt.Sprintf("obj-%d", rng.Intn(len(records)+1)), "ConflictType")
	case 2: // 链接记录字段损坏
		records[i] = LinkRecord{SourceID: "x", Direction: "diagonal"}
	case 3: // 引用缺失
		records[i] = link("never-declared", "also-never")
	}
}

func TestRandomizedDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	for trial := 0; trial < 500; trial++ {
		n := rng.Intn(24)
		records := genValidSequence(rng, n)
		if rng.Intn(3) > 0 { // 2/3 的用例注入损坏
			injectCorruption(rng, records)
		}
		got := Validate(records)
		t.Logf("trial=%d input=%v complete=%v prefix=%d corruption=%v badIndex=%d",
			trial, records, got.Complete, got.PrefixLen, got.Corruption, got.BadIndex)
		assertResult(t, records, got)
	}
}
