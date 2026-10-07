package snapshot

import (
	"errors"
	"strings"
	"testing"
)

func strPtr(s string) *string { return &s }

func obj(id, typ string) Record {
	return Record{Kind: KindObject, ID: strPtr(id), Type: strPtr(typ)}
}

func link(src, dst, linkType string, dir Direction) Record {
	return Record{Kind: KindLink, SourceID: strPtr(src), TargetID: strPtr(dst),
		LinkType: strPtr(linkType), Direction: dir}
}

func runWithStats(t *testing.T, records []Record) (Result, validateStats) {
	t.Helper()
	var stats validateStats
	got := validate(NewSliceReader(records), &stats)
	return got, stats
}

func assertResult(t *testing.T, got Result, status Status, prefix, bad int, reason Reason) {
	t.Helper()
	if got.Status != status || got.PrefixLen != prefix || got.BadIndex != bad || got.Reason != reason {
		t.Fatalf("got %s, want status=%v prefix=%d bad=%d reason=%v", got, status, prefix, bad, reason)
	}
}

// 空序列必须判定为“完整”，而不是“长度为零的前缀”。
func TestEmptyStreamIsComplete(t *testing.T) {
	got, stats := runWithStats(t, nil)
	assertResult(t, got, StatusComplete, 0, -1, ReasonNone)
	if stats.recordsRead != 0 {
		t.Fatalf("recordsRead = %d, want 0", stats.recordsRead)
	}
}

// 损坏发生在第一条记录：前缀长度为零但原因仍须给出。
func TestCorruptionAtFirstRecord(t *testing.T) {
	cases := []struct {
		name   string
		record Record
		reason Reason
	}{
		{"object missing id", Record{Kind: KindObject, Type: strPtr("Person")}, ReasonObjectCorrupt},
		{"object empty type", obj("o1", ""), ReasonObjectCorrupt},
		{"link bad fields", Record{Kind: KindLink, SourceID: strPtr("a")}, ReasonLinkCorrupt},
		{"link bad direction", link("a", "b", "knows", DirUnknown), ReasonLinkCorrupt},
		{"link missing reference", link("a", "b", "knows", DirForward), ReasonLinkReferenceMissing},
		{"unknown kind", Record{}, ReasonObjectCorrupt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, stats := runWithStats(t, []Record{tc.record})
			assertResult(t, got, StatusTruncated, 0, 0, tc.reason)
			if stats.recordsRead != 1 {
				t.Fatalf("recordsRead = %d, want 1", stats.recordsRead)
			}
		})
	}
}

// 损坏发生在中间位置。
func TestCorruptionInMiddle(t *testing.T) {
	records := []Record{
		obj("a", "Person"),
		obj("b", "Person"),
		link("a", "b", "knows", DirForward),
		obj("c", ""), // 下标 3：对象字段损坏
		link("a", "b", "knows", DirReverse),
	}
	got, stats := runWithStats(t, records)
	assertResult(t, got, StatusTruncated, 3, 3, ReasonObjectCorrupt)
	if stats.recordsRead != 4 {
		t.Fatalf("recordsRead = %d, want 4 (prefix+1), later records must not be read", stats.recordsRead)
	}
}

// 损坏发生在最后一条记录：前缀几乎等于全长，但仍必须是截断而非完整。
func TestCorruptionAtLastRecordIsNotComplete(t *testing.T) {
	records := []Record{
		obj("a", "Person"),
		obj("b", "Person"),
		link("b", "a", "knows", DirReverse),
		link("a", "x", "knows", DirForward), // 末条引用缺失
	}
	got, _ := runWithStats(t, records)
	assertResult(t, got, StatusTruncated, 3, 3, ReasonLinkReferenceMissing)
}

// 完全自洽的序列判定为完整，前缀等于全长。
func TestFullyConsistentStream(t *testing.T) {
	records := []Record{
		obj("a", "Person"),
		obj("b", "Person"),
		link("a", "b", "knows", DirForward),
		obj("c", "Org"),
		link("c", "a", "employs", DirForward),
	}
	got, stats := runWithStats(t, records)
	assertResult(t, got, StatusComplete, 5, -1, ReasonNone)
	if stats.recordsRead != 5 {
		t.Fatalf("recordsRead = %d, want 5", stats.recordsRead)
	}
}

// 同一对象重复出现：同类型冗余不损坏；不同类型第二次出现自身损坏。
func TestDuplicateObjectTypes(t *testing.T) {
	t.Run("same type is redundant", func(t *testing.T) {
		records := []Record{obj("a", "Person"), obj("a", "Person")}
		got, _ := runWithStats(t, records)
		assertResult(t, got, StatusComplete, 2, -1, ReasonNone)
	})
	t.Run("different type corrupts second occurrence", func(t *testing.T) {
		records := []Record{
			obj("a", "Person"),
			obj("b", "Org"),
			obj("a", "Org"), // 下标 2 类型冲突
		}
		got, stats := runWithStats(t, records)
		assertResult(t, got, StatusTruncated, 2, 2, ReasonObjectCorrupt)
		if stats.recordsRead != 3 {
			t.Fatalf("recordsRead = %d, want 3", stats.recordsRead)
		}
	})
}

// 连锁情形：损坏的对象记录不进入可信对象表，后续引用它的链接判引用缺失，
// 且截断点恰恰是损坏对象记录本身（对象损坏优先于引用缺失）。
func TestReferenceToCorruptObjectChain(t *testing.T) {
	records := []Record{
		obj("a", "Person"),
		{Kind: KindObject, ID: strPtr("b")}, // 下标 1：对象字段损坏（缺 type）
		link("a", "b", "knows", DirForward), // 若 b 完好本可通过
	}
	got, stats := runWithStats(t, records)
	assertResult(t, got, StatusTruncated, 1, 1, ReasonObjectCorrupt)
	// 命中即停：链接记录不得被读取。
	if stats.recordsRead != 2 {
		t.Fatalf("recordsRead = %d, want 2", stats.recordsRead)
	}
}

// 三类原因的判定次序：同一条链接既字段损坏又引用不存在时，报链接字段损坏。
func TestReasonPrecedenceOnSameLink(t *testing.T) {
	bad := Record{Kind: KindLink, SourceID: strPtr("a"), Direction: DirForward}
	records := []Record{obj("a", "Person"), bad}
	got, _ := runWithStats(t, records)
	assertResult(t, got, StatusTruncated, 1, 1, ReasonLinkCorrupt)
}

// 对象损坏优先于链接损坏：无法识别 Kind 的记录按对象字段损坏归类。
func TestObjectReasonTakesPrecedenceOverLink(t *testing.T) {
	records := []Record{obj("a", "Person"), {SourceID: strPtr("a")}}
	got, _ := runWithStats(t, records)
	assertResult(t, got, StatusTruncated, 1, 1, ReasonObjectCorrupt)
}

// 内部度量恒满足 recordsRead <= prefixLen + 1，且完整时等于全长。
func TestRecordsReadBound(t *testing.T) {
	records := []Record{
		obj("a", "Person"),
		obj("b", "Person"),
		link("a", "b", "knows", DirForward),
		link("a", "z", "knows", DirForward),
		obj("c", "Person"),
		obj("d", "Person"),
	}
	got, stats := runWithStats(t, records)
	if got.Status != StatusTruncated || got.PrefixLen != 3 {
		t.Fatalf("unexpected result %s", got)
	}
	if stats.recordsRead > got.PrefixLen+1 {
		t.Fatalf("recordsRead %d exceeds prefix+1 %d", stats.recordsRead, got.PrefixLen+1)
	}
	if stats.recordsRead != got.PrefixLen+1 {
		t.Fatalf("recordsRead = %d, want %d", stats.recordsRead, got.PrefixLen+1)
	}
}

// 校验不得修改输入切片。
func TestValidationDoesNotMutateInput(t *testing.T) {
	records := []Record{
		obj("a", "Person"),
		link("a", "a", "self", DirForward), // 自引用合法
		obj("a", "Person"),                 // 冗余重复
	}
	snapshot := make([]Record, len(records))
	copy(snapshot, records)
	_ = ValidateSlice(records)
	for i := range records {
		if records[i] != snapshot[i] {
			t.Fatalf("record %d mutated: %+v vs %+v", i, records[i], snapshot[i])
		}
	}
}

func TestResultStringForms(t *testing.T) {
	if s := (Result{Status: StatusComplete, PrefixLen: 2, BadIndex: -1}).String(); !strings.Contains(s, "complete") {
		t.Fatalf("complete string = %q", s)
	}
	if s := (Result{Status: StatusTruncated, PrefixLen: 1, BadIndex: 1, Reason: ReasonLinkCorrupt}).String(); !strings.Contains(s, "truncated") || !strings.Contains(s, "link_record_field_corrupt") {
		t.Fatalf("truncated string = %q", s)
	}
}

type errorReader struct{ n int }

func (e *errorReader) Read() (Record, error) {
	if e.n > 0 {
		e.n--
		return obj("a", "Person"), nil
	}
	return Record{}, errBoom
}

var errBoom = errors.New("boom")

// Reader 在读出若干合法记录后报错：无法取得可信字段的当前记录按对象
// 字段损坏归类，前缀保留此前已确认的部分。
func TestReaderErrorTruncates(t *testing.T) {
	var stats validateStats
	got := validate(&errorReader{n: 2}, &stats)
	assertResult(t, got, StatusTruncated, 2, 2, ReasonObjectCorrupt)
	if stats.recordsRead != 3 {
		t.Fatalf("recordsRead = %d, want 3", stats.recordsRead)
	}
}

func TestAllReasonNames(t *testing.T) {
	names := map[Reason]string{
		ReasonNone:                 "none",
		ReasonObjectCorrupt:        "object_record_corrupt",
		ReasonLinkCorrupt:          "link_record_field_corrupt",
		ReasonLinkReferenceMissing: "link_reference_missing",
	}
	for reason, want := range names {
		if reason.String() != want {
			t.Fatalf("%v.String() = %q, want %q", reason, reason.String(), want)
		}
	}
	kinds := map[Kind]string{KindUnknown: "unknown", KindObject: "object", KindLink: "link"}
	for kind, want := range kinds {
		if kind.String() != want {
			t.Fatalf("%v.String() = %q, want %q", kind, kind.String(), want)
		}
	}
}
