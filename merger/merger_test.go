package merger

import (
	"bytes"
	"fmt"
	"sort"
	"sync"
	"testing"
)

// naiveApply 是逐条应用原始事件的朴素参照：
// 先按相同规则逐条校验，然后直接改快照；任何一步非法则整批拒绝。
func naiveApply(columns []string, initial map[string]map[string]ColumnValue, events []Event) (map[string]map[string]ColumnValue, error) {
	tbl := NewTable(columns, nil)
	tbl.rows = cloneSnapshot(initial)
	for i, ev := range events {
		if ev.Key == "" {
			return nil, batchError(ErrorEmptyKey, i, ev.Key, "", "empty key")
		}
		switch ev.Type {
		case Insert:
			if _, ok := tbl.rows[ev.Key]; ok {
				return nil, batchError(ErrorKeyAlreadyExists, i, ev.Key, "", "exists")
			}
			if len(ev.Columns) != len(columns) {
				return nil, batchError(ErrorInsertMissingColumn, i, ev.Key, "", "missing columns")
			}
			for c := range ev.Columns {
				if !knownColumn(columns, c) {
					return nil, batchError(ErrorUnknownColumn, i, ev.Key, c, "unknown column")
				}
			}
			tbl.rows[ev.Key] = cloneRow(ev.Columns)
		case Update:
			row, ok := tbl.rows[ev.Key]
			if !ok {
				return nil, batchError(ErrorKeyNotFound, i, ev.Key, "", "not found")
			}
			if len(ev.Columns) == 0 {
				return nil, batchError(ErrorEmptyUpdate, i, ev.Key, "", "empty update")
			}
			if !sameKeys(ev.Columns, ev.Before) {
				return nil, batchError(ErrorChangeMirrorColumnMismatch, i, ev.Key, "", "mirror mismatch")
			}
			for c, v := range ev.Columns {
				if !knownColumn(columns, c) {
					return nil, batchError(ErrorUnknownColumn, i, ev.Key, c, "unknown column")
				}
				if !row[c].Equal(ev.Before[c]) {
					return nil, batchError(ErrorBeforeImageMismatch, i, ev.Key, c, "before mismatch")
				}
				row[c] = v
			}
		default:
			return nil, batchError(ErrorUnknownEventType, i, ev.Key, "", "bad type")
		}
	}
	return tbl.Snapshot(), nil
}

func knownColumn(columns []string, c string) bool {
	for _, x := range columns {
		if x == c {
			return true
		}
	}
	return false
}

func cloneSnapshot(in map[string]map[string]ColumnValue) map[string]map[string]ColumnValue {
	out := make(map[string]map[string]ColumnValue, len(in))
	for k, row := range in {
		out[k] = cloneRow(row)
	}
	return out
}

func TestSmoke(t *testing.T) {
	tbl := NewTable([]string{"a", "b"}, &bytes.Buffer{})
	res, err := tbl.Commit([]Event{{
		Type:    Insert,
		Key:     "k1",
		Columns: map[string]ColumnValue{"a": String("x"), "b": Null()},
	}})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if len(res.Outputs) != 1 {
		t.Fatalf("outputs = %d", len(res.Outputs))
	}
}

func mustRow(t *testing.T, tbl *Table, key string) map[string]ColumnValue {
	t.Helper()
	row, ok := tbl.Get(key)
	if !ok {
		t.Fatalf("key %q missing", key)
	}
	return row
}

func expectErrorCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	be, ok := err.(*BatchError)
	if !ok {
		t.Fatalf("want *BatchError, got %T: %v", err, err)
	}
	if be.Code != want {
		t.Fatalf("code = %s, want %s (%v)", be.Code, want, err)
	}
}

func reflectRows(a, b map[string]ColumnValue) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		w, ok := b[k]
		if !ok || !v.Equal(w) {
			return false
		}
	}
	return true
}

func stableRows(in map[string]map[string]ColumnValue) string {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s := ""
	for _, k := range keys {
		s += k + ":" + fmt.Sprintf("%v", in[k]) + "\n"
	}
	return s
}

func TestConcurrentReadsDuringCommit(t *testing.T) {
	tbl := NewTable([]string{"a", "b"}, &bytes.Buffer{})
	_, err := tbl.Commit([]Event{{
		Type:    Insert,
		Key:     "seed",
		Columns: map[string]ColumnValue{"a": String("0"), "b": String("0")},
	}})
	if err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if _, ok := tbl.Get("seed"); !ok {
						t.Error("seed vanished")
						return
					}
					if err := tbl.SelfCheck(); err != nil {
						t.Errorf("self-check: %v", err)
						return
					}
					_ = tbl.Snapshot()
				}
			}
		}()
	}

	for b := 0; b < 50; b++ {
		key := fmt.Sprintf("k%d", b)
		events := []Event{{
			Type:    Insert,
			Key:     key,
			Columns: map[string]ColumnValue{"a": String("0"), "b": String("0")},
		}}
		for i := 0; i < 5; i++ {
			events = append(events, Event{
				Type:    Update,
				Key:     key,
				Columns: map[string]ColumnValue{"a": String(fmt.Sprintf("%d", i+1))},
				Before:  map[string]ColumnValue{"a": String(fmt.Sprintf("%d", i))},
			})
		}
		if _, err := tbl.Commit(events); err != nil {
			t.Fatalf("batch %d: %v", b, err)
		}
	}
	close(stop)
	wg.Wait()

	if err := tbl.SelfCheck(); err != nil {
		t.Fatalf("final self-check: %v", err)
	}
}

func TestThreeWayValues(t *testing.T) {
	// 缺席、显式空值、空串三者在相等性与日志渲染上都必须可区分。
	if Absent().Equal(Null()) || Null().Equal(String("")) || Absent().Equal(String("")) {
		t.Fatal("absent / null / empty-string must be pairwise distinct")
	}
	if Absent().Present() || Null().Present() != true || String("").Present() != true {
		t.Fatal("Present() semantics wrong")
	}
	if Null().IsNull() != true || String("").IsNull() != false {
		t.Fatal("IsNull() semantics wrong")
	}
	if s, ok := String("").StringValue(); !ok || s != "" {
		t.Fatal("empty string must be a valid string value")
	}
	if got := []string{Absent().String(), Null().String(), String("").String(), String("x").String()}; got[0] != "<absent>" || got[1] != "<null>" || got[2] != `""` || got[3] != `"x"` {
		t.Fatalf("rendering = %v", got)
	}

	// 落表后：显式空值与空串是不同的真实值。
	var log bytes.Buffer
	tbl := NewTable([]string{"a", "b"}, &log)
	_, err := tbl.Commit([]Event{{
		Type:    Insert,
		Key:     "k",
		Columns: map[string]ColumnValue{"a": Null(), "b": String("")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	row := mustRow(t, tbl, "k")
	if !row["a"].IsNull() || !row["b"].IsString() {
		t.Fatalf("null vs empty-string not preserved: %#v %#v", row["a"], row["b"])
	}
}

func TestInsertThenUpdate(t *testing.T) {
	var log bytes.Buffer
	tbl := NewTable([]string{"a", "b", "c"}, &log)
	res, err := tbl.Commit([]Event{
		{Type: Insert, Key: "k", Columns: map[string]ColumnValue{"a": String("1"), "b": String("2"), "c": Null()}},
		{Type: Update, Key: "k", Columns: map[string]ColumnValue{"b": Null()},
			Before: map[string]ColumnValue{"b": String("2")}},
		{Type: Update, Key: "k", Columns: map[string]ColumnValue{"c": String("9")},
			Before: map[string]ColumnValue{"c": Null()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Outputs) != 1 || res.Outputs[0].Type != Insert {
		t.Fatalf("want one insert output, got %+v", res.Outputs)
	}
	row := mustRow(t, tbl, "k")
	want := map[string]ColumnValue{"a": String("1"), "b": Null(), "c": String("9")}
	if !reflectRows(row, want) {
		t.Fatalf("row = %v, want %v", row, want)
	}
	if res.Applied != 1 {
		t.Fatalf("applied = %d, want 1", res.Applied)
	}
}

func TestUpdateUnionAndLastWins(t *testing.T) {
	var log bytes.Buffer
	tbl := NewTable([]string{"a", "b", "c"}, &log)
	_, err := tbl.Commit([]Event{{
		Type:    Insert,
		Key:     "k",
		Columns: map[string]ColumnValue{"a": String("0"), "b": String("0"), "c": String("0")},
	}})
	if err != nil {
		t.Fatal(err)
	}

	res, err := tbl.Commit([]Event{
		{Type: Update, Key: "k",
			Columns: map[string]ColumnValue{"a": String("1"), "b": Null()},
			Before:  map[string]ColumnValue{"a": String("0"), "b": String("0")}},
		{Type: Update, Key: "k",
			Columns: map[string]ColumnValue{"a": String("2"), "c": String("")},
			Before:  map[string]ColumnValue{"a": String("1"), "c": String("0")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Outputs) != 1 {
		t.Fatalf("outputs = %d", len(res.Outputs))
	}
	out := res.Outputs[0]
	if out.Type != Update || len(out.Columns) != 3 {
		t.Fatalf("merged = %+v", out)
	}
	if v := out.Columns["a"]; !v.Equal(String("2")) {
		t.Fatalf("last value for a = %s", v)
	}
	if v := out.Columns["b"]; !v.Equal(Null()) {
		t.Fatalf("b = %s, want null", v)
	}
	if v := out.Columns["c"]; !v.Equal(String("")) {
		t.Fatalf("c = %s, want empty string", v)
	}
	if v := out.Before["a"]; !v.Equal(String("0")) {
		t.Fatalf("before a must be first-update image %s, got %s", String("0"), v)
	}
	if v := out.Before["b"]; !v.Equal(String("0")) {
		t.Fatalf("before b = %s, want \"0\"", v)
	}
	if v := out.Before["c"]; !v.Equal(String("0")) {
		t.Fatalf("before c = %s", v)
	}

	row := mustRow(t, tbl, "k")
	want := map[string]ColumnValue{"a": String("2"), "b": Null(), "c": String("")}
	if !reflectRows(row, want) {
		t.Fatalf("row = %v want %v", row, want)
	}
}

func TestNoChangeDropping(t *testing.T) {
	var log bytes.Buffer
	tbl := NewTable([]string{"a", "b", "c"}, &log)
	_, err := tbl.Commit([]Event{{
		Type:    Insert,
		Key:     "k",
		Columns: map[string]ColumnValue{"a": String("x"), "b": Null(), "c": String("z")},
	}})
	if err != nil {
		t.Fatal(err)
	}

	// 同列先改成别的值再改回原值 -> 合并后该列被剔除；全部剔空 -> 该键不输出应用。
	res, err := tbl.Commit([]Event{
		{Type: Update, Key: "k",
			Columns: map[string]ColumnValue{"a": String("y")},
			Before:  map[string]ColumnValue{"a": String("x")}},
		{Type: Update, Key: "k",
			Columns: map[string]ColumnValue{"a": String("x")},
			Before:  map[string]ColumnValue{"a": String("y")}},
		{Type: Update, Key: "k",
			Columns: map[string]ColumnValue{"b": Null()},
			Before:  map[string]ColumnValue{"b": Null()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Outputs) != 1 {
		t.Fatalf("want one (empty) merged output, got %d", len(res.Outputs))
	}
	if len(res.Outputs[0].Columns) != 0 || res.Applied != 0 {
		t.Fatalf("expected fully dropped update: %+v applied=%d", res.Outputs[0], res.Applied)
	}
	if !res.Skipped["k"] {
		t.Fatal("key should be marked skipped")
	}

	// 混合：一列剔空、一列仍有变化，只保留变化列。
	res2, err := tbl.Commit([]Event{
		{Type: Update, Key: "k",
			Columns: map[string]ColumnValue{"a": String("y"), "c": String("z")},
			Before:  map[string]ColumnValue{"a": String("x"), "c": String("z")}},
		{Type: Update, Key: "k",
			Columns: map[string]ColumnValue{"a": String("x")},
			Before:  map[string]ColumnValue{"a": String("y")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := res2.Outputs[0]
	if len(out.Columns) != 0 {
		t.Fatalf("c no-change and a restored => empty, got %v", out.Columns)
	}

	res3, err := tbl.Commit([]Event{
		{Type: Update, Key: "k",
			Columns: map[string]ColumnValue{"a": String("y"), "c": String("q")},
			Before:  map[string]ColumnValue{"a": String("x"), "c": String("z")}},
		{Type: Update, Key: "k",
			Columns: map[string]ColumnValue{"a": String("x")},
			Before:  map[string]ColumnValue{"a": String("y")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cols := res3.Outputs[0].Columns; len(cols) != 1 || !cols["c"].Equal(String("q")) {
		t.Fatalf("only c should survive: %v", cols)
	}
	if res3.Applied != 1 {
		t.Fatalf("applied = %d", res3.Applied)
	}

	if !bytes.Contains(log.Bytes(), []byte("dropped")) {
		t.Fatal("log should explain dropping decisions")
	}
}

func TestRejectionsLeaveNoTrace(t *testing.T) {
	cols := []string{"a", "b"}
	var log bytes.Buffer
	tbl := NewTable(cols, &log)
	initial := map[string]map[string]ColumnValue{
		"old": {"a": String("1"), "b": String("2")},
	}
	tbl.rows = cloneSnapshot(initial)

	bad := [][]Event{
		{ // 空键
			{Type: Insert, Key: "", Columns: map[string]ColumnValue{"a": String("1"), "b": String("2")}},
		},
		{ // 未知事件类型
			{Type: "delete", Key: "old", Columns: map[string]ColumnValue{"a": String("9")},
				Before: map[string]ColumnValue{"a": String("1")}},
		},
		{ // 插入缺列
			{Type: Insert, Key: "k", Columns: map[string]ColumnValue{"a": String("1")}},
		},
		{ // 更新空变更
			{Type: Update, Key: "old", Columns: map[string]ColumnValue{}, Before: map[string]ColumnValue{}},
		},
		{ // 变更列集合与镜像列集合不同
			{Type: Update, Key: "old",
				Columns: map[string]ColumnValue{"a": String("9")},
				Before:  map[string]ColumnValue{"b": String("2")}},
		},
		{ // 更新不存在的键
			{Type: Update, Key: "ghost",
				Columns: map[string]ColumnValue{"a": String("9")},
				Before:  map[string]ColumnValue{"a": Null()}},
		},
		{ // 插入已存在的键
			{Type: Insert, Key: "old", Columns: map[string]ColumnValue{"a": String("1"), "b": String("2")}},
		},
		{ // 变更前镜像与实际不符
			{Type: Update, Key: "old",
				Columns: map[string]ColumnValue{"a": String("9")},
				Before:  map[string]ColumnValue{"a": String("WRONG")}},
		},
		{ // 批内第二条才出错：第一条的插入也必须回滚不留痕
			{Type: Insert, Key: "new", Columns: map[string]ColumnValue{"a": String("1"), "b": String("2")}},
			{Type: Update, Key: "old",
				Columns: map[string]ColumnValue{"a": String("9")},
				Before:  map[string]ColumnValue{"a": String("WRONG")}},
		},
		{ // 批内重复插入同一键
			{Type: Insert, Key: "new", Columns: map[string]ColumnValue{"a": String("1"), "b": String("2")}},
			{Type: Insert, Key: "new", Columns: map[string]ColumnValue{"a": String("3"), "b": String("4")}},
		},
		{ // 重复更新镜像不符（引用上一条的后到值才能通过，这里故意写错）
			{Type: Update, Key: "old",
				Columns: map[string]ColumnValue{"a": String("9")},
				Before:  map[string]ColumnValue{"a": String("1")}},
			{Type: Update, Key: "old",
				Columns: map[string]ColumnValue{"a": String("8")},
				Before:  map[string]ColumnValue{"a": String("1")}},
		},
		{ // 非法列出现在变更中
			{Type: Insert, Key: "new", Columns: map[string]ColumnValue{"a": String("1"), "b": String("2"), "zzz": String("x")}},
		},
	}

	for n, batch := range bad {
		before := tbl.Snapshot()
		_, err := tbl.Commit(batch)
		if err == nil {
			t.Fatalf("case %d: expected rejection", n)
		}
		after := tbl.Snapshot()
		if stableRows(after) != stableRows(before) {
			t.Fatalf("case %d: table changed after rejection\nbefore=%v\nafter=%v", n, before, after)
		}
	}

	if !bytes.Contains(log.Bytes(), []byte("REJECTED")) {
		t.Fatal("log must record rejections")
	}
	if err := tbl.SelfCheck(); err != nil {
		t.Fatalf("self-check after rejections: %v", err)
	}
}

func TestDistinctErrorCodes(t *testing.T) {
	codes := []ErrorCode{
		ErrorUnknownColumn,
		ErrorInsertMissingColumn,
		ErrorUnknownEventType,
		ErrorEmptyKey,
		ErrorEmptyUpdate,
		ErrorChangeMirrorColumnMismatch,
		ErrorKeyNotFound,
		ErrorKeyAlreadyExists,
		ErrorBeforeImageMismatch,
	}
	seen := map[ErrorCode]bool{}
	for _, c := range codes {
		if seen[c] {
			t.Fatalf("duplicate code %s", c)
		}
		seen[c] = true
	}

	cols := []string{"a", "b"}
	tbl := NewTable(cols, &bytes.Buffer{})
	tbl.rows = map[string]map[string]ColumnValue{
		"old": {"a": String("1"), "b": String("2")},
	}

	_, err := tbl.Commit([]Event{{Type: Update, Key: "old",
		Columns: map[string]ColumnValue{"nope": String("x")},
		Before:  map[string]ColumnValue{"nope": String("x")}}})
	expectErrorCode(t, err, ErrorUnknownColumn)

	_, err = tbl.Commit([]Event{{Type: Insert, Key: "k", Columns: map[string]ColumnValue{"a": String("x")}}})
	expectErrorCode(t, err, ErrorInsertMissingColumn)

	_, err = tbl.Commit([]Event{{Type: "wipe", Key: "old"}})
	expectErrorCode(t, err, ErrorUnknownEventType)

	_, err = tbl.Commit([]Event{{Type: Update, Key: "", Columns: map[string]ColumnValue{"a": String("x")}}})
	expectErrorCode(t, err, ErrorEmptyKey)

	_, err = tbl.Commit([]Event{{Type: Update, Key: "old", Columns: map[string]ColumnValue{}, Before: map[string]ColumnValue{}}})
	expectErrorCode(t, err, ErrorEmptyUpdate)

	_, err = tbl.Commit([]Event{{Type: Update, Key: "old",
		Columns: map[string]ColumnValue{"a": String("x")},
		Before:  map[string]ColumnValue{"b": String("2")}}})
	expectErrorCode(t, err, ErrorChangeMirrorColumnMismatch)

	_, err = tbl.Commit([]Event{{Type: Update, Key: "ghost",
		Columns: map[string]ColumnValue{"a": String("x")},
		Before:  map[string]ColumnValue{"a": Null()}}})
	expectErrorCode(t, err, ErrorKeyNotFound)

	_, err = tbl.Commit([]Event{{Type: Insert, Key: "old",
		Columns: map[string]ColumnValue{"a": String("1"), "b": String("2")}}})
	expectErrorCode(t, err, ErrorKeyAlreadyExists)

	_, err = tbl.Commit([]Event{{Type: Update, Key: "old",
		Columns: map[string]ColumnValue{"a": String("x")},
		Before:  map[string]ColumnValue{"a": Null()}}})
	expectErrorCode(t, err, ErrorBeforeImageMismatch)
}

func TestMergeMatchesNaive(t *testing.T) {
	cols := []string{"a", "b", "c"}
	rng := newRng(20260929)
	values := func() []ColumnValue {
		switch rng.intn(3) {
		case 0:
			return []ColumnValue{Null()}
		case 1:
			return []ColumnValue{String("")}
		default:
			return []ColumnValue{String(fmt.Sprintf("v%d", rng.intn(4)))}
		}
	}

	for iter := 0; iter < 300; iter++ {
		keys := []string{"k0", "k1", "k2"}
		initial := map[string]map[string]ColumnValue{}
		for _, k := range keys {
			if rng.intn(2) == 0 {
				initial[k] = map[string]ColumnValue{"a": values()[0], "b": values()[0], "c": values()[0]}
			}
		}

		// 生成一批对逐条应用与合并应用都应给出相同结果的事件。
		sim := cloneSnapshot(initial)
		var events []Event
		insertedThisBatch := map[string]bool{}
		firstAppearance := map[string]int{}
		n := rng.intn(8) + 1
		for i := 0; i < n; i++ {
			key := keys[rng.intn(len(keys))]
			row, exists := sim[key]
			if !exists && !insertedThisBatch[key] {
				// 不存在的键：必须先插入。
				ev := Event{Type: Insert, Key: key, Columns: map[string]ColumnValue{
					"a": values()[0], "b": values()[0], "c": values()[0],
				}}
				events = append(events, ev)
				sim[key] = cloneRow(ev.Columns)
				insertedThisBatch[key] = true
				firstAppearance[key] = len(events) - 1
				continue
			}
			if exists && !insertedThisBatch[key] && rng.intn(4) == 0 {
				// 已存在的键不能再插入；小概率构造重复插入以验证双方都拒绝。
				events = append(events, Event{Type: Insert, Key: key,
					Columns: map[string]ColumnValue{"a": values()[0], "b": values()[0], "c": values()[0]}})
				continue
			}
			// 随机挑选 1-2 个存在的列做更新，镜像严格取模拟表现值。
			pick := rng.pickColumns(cols)
			change := map[string]ColumnValue{}
			before := map[string]ColumnValue{}
			for _, c := range pick {
				before[c] = row[c]
				change[c] = values()[0]
			}
			events = append(events, Event{Type: Update, Key: key, Columns: change, Before: before})
			if _, ok := firstAppearance[key]; !ok {
				firstAppearance[key] = len(events) - 1
			}
			for c, v := range change {
				row[c] = v
			}
		}

		want, naiveErr := naiveApply(cols, initial, events)

		var log bytes.Buffer
		tbl := NewTable(cols, &log)
		tbl.rows = cloneSnapshot(initial)
		res, err := tbl.Commit(events)

		if (naiveErr == nil) != (err == nil) {
			t.Fatalf("iter %d: naive err=%v merge err=%v\nevents=%+v", iter, naiveErr, err, events)
		}
		if naiveErr != nil {
			nb := naiveErr.(*BatchError)
			mb := err.(*BatchError)
			if nb.Code != mb.Code {
				t.Fatalf("iter %d: naive code=%s merge code=%s", iter, nb.Code, mb.Code)
			}
			if got := tbl.Snapshot(); stableRows(got) != stableRows(cloneSnapshot(initial)) {
				t.Fatalf("iter %d: rejected batch changed table", iter)
			}
			continue
		}

		got := tbl.Snapshot()
		if stableRows(got) != stableRows(want) {
			t.Fatalf("iter %d: final state mismatch\n got=%v\nwant=%v\nevents=%+v",
				iter, got, want, events)
		}

		// 输出顺序与去重约束。
		seenOut := map[string]bool{}
		var prevOrder int = -1
		var prevIdx int = -1
		for _, o := range res.Outputs {
			if seenOut[o.Key] {
				t.Fatalf("iter %d: key %q appears twice in outputs", iter, o.Key)
			}
			seenOut[o.Key] = true
			if o.Order <= prevOrder {
				t.Fatalf("iter %d: outputs not in first-appearance order", iter)
			}
			prevOrder = o.Order
			if firstAppearance[o.Key] <= prevIdx {
				t.Fatalf("iter %d: first-appearance index not increasing", iter)
			}
			prevIdx = firstAppearance[o.Key]
		}
	}
}

type rng struct{ state uint64 }

func newRng(seed uint64) *rng { return &rng{state: seed} }

func (r *rng) intn(n int) int {
	r.state ^= r.state << 13
	r.state ^= r.state >> 7
	r.state ^= r.state << 17
	return int(r.state%uint64(n)+uint64(n)) % n
}

func (r *rng) pickColumns(cols []string) []string {
	count := r.intn(2) + 1
	idx := map[int]bool{}
	for len(idx) < count {
		idx[r.intn(len(cols))] = true
	}
	out := make([]string, 0, count)
	for i := range idx {
		out = append(out, cols[i])
	}
	sort.Strings(out)
	return out
}
