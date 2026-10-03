package pull_test

import (
	"errors"
	"testing"

	"ontology/pull"
)

// 规格例一：稳定延迟 + 回看窗口，晚提交行在窗口内被补回。
func TestSpecExample1(t *testing.T) {
	src := &memSource{rows: []srcRow{
		{1, 100, 1, 1, 100},
		{2, 100, 1, 1, 100},
		{3, 100, 1, 1, 100},
		{4, 103, 1, 1, 112},
		{5, 102, 1, 1, 125},
	}}
	p, err := pull.New(src, 10, 5, 2, 1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	check := func(name string, now int64, want pull.Result, wantCurTs, wantCurID int64) {
		t.Helper()
		src.reset()
		got, gerr := p.Pull(now)
		if gerr != nil {
			t.Fatalf("%s: %v", name, gerr)
		}
		if got != want {
			t.Fatalf("%s: got %+v want %+v", name, got, want)
		}
		if ts, id := p.Cur(); ts != wantCurTs || id != wantCurID {
			t.Fatalf("%s: cur=(%d,%d) want (%d,%d)", name, ts, id, wantCurTs, wantCurID)
		}
	}

	check("Pull(111)", 111, pull.Result{Applied: 3, Queries: 2}, 100, 3)
	check("Pull(113)", 113, pull.Result{Applied: 1, Dup: 3, Queries: 3}, 103, 4)

	// r5 的 ts=102 落在回看窗口（cur.ts-B=98）内，虽 (102,5)<cur 仍被补回，
	// 读到顺序为 r1,r2,r3,r5,r4；r5 应用，其余 Dup；本轮末行仍是 r4=(103,4)，
	// 游标取 max 后保持 (103,4) 不回退、不误进到 id=5。
	check("Pull(130)", 130, pull.Result{Applied: 1, Dup: 4, Queries: 3}, 103, 4)
}

// 例一补充：晚提交行 ts 早于回看起点则永久漏拉。
func TestSpecExample1MissedOutsideWindow(t *testing.T) {
	src := &memSource{rows: []srcRow{
		{1, 100, 1, 1, 100},
		{2, 100, 1, 1, 100},
		{3, 100, 1, 1, 100},
		{4, 103, 1, 1, 112},
		{5, 97, 1, 1, 125}, // ts=97 < Pull(130) 起点 98
	}}
	p, _ := pull.New(src, 10, 5, 2, 1)
	for _, now := range []int64{111, 113, 130} {
		src.reset()
		if _, err := p.Pull(now); err != nil {
			t.Fatalf("Pull(%d): %v", now, err)
		}
	}
	if p.Applied(5) != 0 {
		t.Fatalf("id=5 应永久漏拉，applied=%d", p.Applied(5))
	}
}

// 规格例二：B=0 时与游标同 ts 的行仍被重读，全部 Dup，游标不变。
func TestSpecExample2(t *testing.T) {
	src := &memSource{rows: []srcRow{
		{1, 50, 1, 1, 50},
		{2, 50, 1, 1, 50},
		{3, 50, 1, 1, 50},
	}}
	p, _ := pull.New(src, 0, 0, 2, 1)

	src.reset()
	r1, err := p.Pull(50)
	if err != nil {
		t.Fatal(err)
	}
	if r1 != (pull.Result{Applied: 3, Queries: 2}) {
		t.Fatalf("Pull(50)=%+v", r1)
	}

	src.reset()
	r2, err := p.Pull(51)
	if err != nil {
		t.Fatal(err)
	}
	if r2 != (pull.Result{Dup: 3, Queries: 2}) {
		t.Fatalf("Pull(51)=%+v", r2)
	}
	if ts, id := p.Cur(); ts != 50 || id != 3 {
		t.Fatalf("cur=(%d,%d) 应保持 (50,3)", ts, id)
	}
}

// 规格例三：超 schema 行进死信不重复计数，升级后在回看窗口内补应用。
func TestSpecExample3DLQAndUpgrade(t *testing.T) {
	src := &memSource{rows: []srcRow{{9, 200, 2, 2, 200}}}
	p, _ := pull.New(src, 0, 100, 10, 1)

	src.reset()
	r1, err := p.Pull(200)
	if err != nil {
		t.Fatal(err)
	}
	if r1 != (pull.Result{NewDLQ: 1, Queries: 1}) {
		t.Fatalf("first pull=%+v", r1)
	}

	src.reset()
	r2, _ := p.Pull(201)
	if r2.NewDLQ != 0 || r2.Applied != 0 {
		t.Fatalf("second pull=%+v, 死信不应重复入列", r2)
	}
	if _, ok := p.DLQ(9, 2); !ok {
		t.Fatal("死信中应存在 (9,2)")
	}

	if err := p.SetSchema(2, 2); err != nil {
		t.Fatalf("SetSchema: %v", err)
	}
	src.reset()
	r3, _ := p.Pull(202)
	if r3.Applied != 1 || r3.Dup != 0 {
		t.Fatalf("third pull=%+v, 升级后应补应用", r3)
	}
	if p.Applied(9) != 2 {
		t.Fatalf("applied[9]=%d want 2", p.Applied(9))
	}
}

// 规格例四：某页 Query 出错 → ErrSource，已读页作废，状态零改动。
func TestSpecExample4SourceErrorZeroChange(t *testing.T) {
	src := &memSource{
		rows: []srcRow{
			{1, 100, 1, 1, 100},
			{2, 100, 1, 1, 100},
			{3, 100, 1, 1, 100},
		},
		failAtQueries: 2,
	}
	p, _ := pull.New(src, 0, 0, 2, 1)
	src.reset()
	if _, err := p.Pull(100); !errors.Is(err, pull.ErrSource) {
		t.Fatalf("err=%v want ErrSource", err)
	}
	if ts, id := p.Cur(); ts != 0 || id != 0 {
		t.Fatalf("出错后游标=(%d,%d) 应为 (0,0)", ts, id)
	}
	if p.Applied(1) != 0 || p.Applied(2) != 0 || p.DLQSize() != 0 {
		t.Fatal("出错后 sink 应零改动")
	}
	if p.MaxNow() != 0 {
		t.Fatalf("出错后 maxNow=%d 应为 0", p.MaxNow())
	}

	// 去掉故障后重放，应完整成功。
	src.failAtQueries = 0
	src.reset()
	r, err := p.Pull(100)
	if err != nil {
		t.Fatal(err)
	}
	if r.Applied != 3 || r.Queries != 2 {
		t.Fatalf("重放=%+v", r)
	}
}
