package ontology

import (
	"bytes"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// 版本数超限：达到上限后演进被拒，且状态不变。
func TestEvolve_TooManyVersions(t *testing.T) {
	// 上限 3：v1 初始 + 两次演进。
	r, _ := newTestRegistry(t, 3)
	mustEvolve(t, r, Change{Kind: ChangeAdd, Name: "x", Default: ""}) // v2
	mustEvolve(t, r, Change{Kind: ChangeAdd, Name: "y", Default: ""}) // v3
	if r.VersionCount() != 3 {
		t.Fatalf("versions=%d, want 3", r.VersionCount())
	}

	_, err := r.Evolve([]Change{{Kind: ChangeAdd, Name: "z", Default: ""}})
	assertCode(t, err, ErrTooManyVersions)
	if r.VersionCount() != 3 {
		t.Fatalf("rejected evolve must not add version, got %d", r.VersionCount())
	}
}

// 改名不影响历史事件解码：标识不变，旧值仍按标识取到。
func TestDecode_AcrossRename(t *testing.T) {
	r, _ := newTestRegistry(t, DefaultMaxVersions)
	mustEvolve(t, r, Change{Kind: ChangeRename, ID: 2, Name: "renamed"}) // v2

	// v1 事件仍按 #2 取值，尽管该列当前名字已变。
	row, err := r.Decode(Event{Version: 1, Values: []string{"a1", "b1", "c1"}})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	cv := row.Ordered()
	if cv[1].Column.Name != "renamed" || cv[1].Value != "b1" || cv[1].Source != SourceEvent {
		t.Fatalf("renamed column = %+v, want name=renamed value=b1 from event", cv[1])
	}
}

// 并发：解码与演进同时进行，不发生 data race，且每个结果都自洽
// （要么基于 v3，要么基于 v4，绝不会出现撕裂结构）。
func TestConcurrent_DecodeAndEvolve(t *testing.T) {
	r, _ := newTestRegistry(t, 100000)

	const decoders = 16
	const iterations = 500

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 演进者：反复在“加列 / 删列 / 改名”之间推进版本。
	wg.Add(1)
	go func() {
		defer wg.Done()
		id := ColumnID(4)
		for i := 0; i < iterations; i++ {
			name := colName(i)
			_, err := r.Evolve([]Change{
				{Kind: ChangeAdd, Name: name, Default: "D" + name},
			})
			if err != nil {
				t.Errorf("evolve add: %v", err)
				return
			}
			// 对刚加的列改名（标识保持 id）。
			if _, err := r.Evolve([]Change{
				{Kind: ChangeRename, ID: id, Name: name + "_r"},
			}); err != nil {
				t.Errorf("evolve rename: %v", err)
				return
			}
			id++
		}
		close(stop)
	}()

	// 解码器：不断解码一个 v1 事件。#1/#3 始终来自事件；
	// 期间被删除的列不存在，其余新增列来自当前默认值。
	var (
		validVersionsMu sync.Mutex
		validVersions   = make(map[int]bool)
	)
	recordVersion := func(v int) {
		validVersionsMu.Lock()
		validVersions[v] = true
		validVersionsMu.Unlock()
	}
	for d := 0; d < decoders; d++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ev := Event{Version: 1, Values: []string{"a1", "b1", "c1"}}
			for {
				select {
				case <-stop:
					return
				default:
				}
				row, err := r.Decode(ev)
				if err != nil {
					t.Errorf("concurrent decode: %v", err)
					return
				}
				recordVersion(row.SchemaVersion)
				// 结果自洽性：#1 必来自事件，#3 必来自事件（二者从不删除）。
				if v, src, ok := row.Get(1); !ok || v != "a1" || src != SourceEvent {
					t.Errorf("id #1 inconsistent: %q %v %v", v, src, ok)
					return
				}
				if v, src, ok := row.Get(3); !ok || v != "c1" || src != SourceEvent {
					t.Errorf("id #3 inconsistent: %q %v %v", v, src, ok)
					return
				}
				// 行内列数必须与所声明的结构版本一致。
				snap, err := r.SchemaAt(row.SchemaVersion)
				if err != nil {
					t.Errorf("snapshot for row version: %v", err)
					return
				}
				if len(row.Order) != len(snap.Columns) {
					t.Errorf("torn row: order=%d schema=%d", len(row.Order), len(snap.Columns))
					return
				}
			}
		}()
	}

	wg.Wait()
	if len(validVersions) == 0 {
		t.Fatalf("decoders never produced results")
	}
}

// 并发批量解码与演进：所有成功批次必须原子、结果数等于输入数。
func TestConcurrent_DecodeBatch(t *testing.T) {
	r, _ := newTestRegistry(t, 100000)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_, _ = r.Evolve([]Change{{Kind: ChangeAdd, Name: colName(i), Default: "d"}})
		}
	}()

	for d := 0; d < 8; d++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			batch := []Event{
				{Version: 1, Values: []string{"a", "b", "c"}},
				{Version: 1, Values: []string{"", "", ""}},
			}
			for i := 0; i < 200; i++ {
				rows, err := r.DecodeBatch(batch)
				if err != nil {
					// v1 始终存在且始终 3 列，因此不应有错误。
					t.Errorf("concurrent batch: %v", err)
					return
				}
				if len(rows) != 2 {
					t.Errorf("batch rows=%d, want 2 (no partial results)", len(rows))
					return
				}
			}
		}()
	}
	wg.Wait()
}

// 日志必须打印输入、解码结果与判定依据。
func TestDecode_LogsInputResultAndBasis(t *testing.T) {
	var log bytes.Buffer
	r, err := NewRegistry(
		[]ColumnSpec{{Name: "a", Default: "da"}},
		WithLogWriter(&log),
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	mustEvolve(t, r, Change{Kind: ChangeAdd, Name: "b", Default: "db"}) // v2

	if _, err := r.Decode(Event{Version: 1, Values: []string{"hello"}}); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := log.String()
	wantSubstrings := []string{
		"decode accepted",
		`version=1`,                       // 输入版本
		`"hello"`,                         // 输入值
		`#1=a="hello"(from=event)`,        // 来自事件的判定依据
		`#2=b="db"(from=current-default)`, // 缺列取当前默认值
		"by-stable-id",                    // 按稳定标识
	}
	for _, s := range wantSubstrings {
		if !strings.Contains(out, s) {
			t.Errorf("log missing %q\nfull log:\n%s", s, out)
		}
	}
}

// 拒绝解码也要打印输入与原因。
func TestDecode_RejectionLogged(t *testing.T) {
	var log bytes.Buffer
	r, err := NewRegistry(
		[]ColumnSpec{{Name: "a", Default: ""}},
		WithLogWriter(&log),
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if _, err := r.Decode(Event{Version: 9, Values: []string{"x"}}); err == nil {
		t.Fatalf("expected rejection")
	}
	out := log.String()
	if !strings.Contains(out, "decode rejected") || !strings.Contains(out, "version_not_found") {
		t.Fatalf("rejection log missing reason:\n%s", out)
	}
}

func colName(i int) string {
	return "c" + strconv.Itoa(i)
}
