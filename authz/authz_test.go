package authz

import (
	"errors"
	"testing"

	"ontology/adjust"
	"ontology/count"
)

type fixture struct {
	db  *adjust.DB
	eng *count.Engine
	m   *Manager
}

func newFixture(t *testing.T, tabs, tpct, lim int64) *fixture {
	t.Helper()
	db := adjust.New(tabs, tpct, lim)
	if err := db.AddLoc([]byte("Y"), 200, 10); err != nil {
		t.Fatal(err)
	}
	eng := count.New(db)
	if err := eng.Open([]byte("t"), [][]byte{[]byte("Y")}); err != nil {
		t.Fatal(err)
	}
	// u1 初盘 180；Move -30；u2 复盘 150，一致 -> Pending(diff=-20)。
	if err := eng.Submit([]byte("t"), []byte("Y"), 180, []byte("u1")); err != nil {
		t.Fatal(err)
	}
	if err := db.Move([]byte("Y"), -30); err != nil {
		t.Fatal(err)
	}
	if err := eng.Submit([]byte("t"), []byte("Y"), 150, []byte("u2")); err != nil {
		t.Fatal(err)
	}
	m := New(eng)
	return &fixture{db: db, eng: eng, m: m}
}

func TestApproveDiffNotCounted(t *testing.T) {
	// 申请 diff=-20；Pending 期间 Move +5 -> book=175；批准后 155，不是 150。
	f := newFixture(t, 2, 5, 500)
	if err := f.db.Move([]byte("Y"), 5); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Grant([]byte("u3"), true, false); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Approve([]byte("t"), []byte("Y"), []byte("u3")); err != nil {
		t.Fatal(err)
	}
	book, _ := f.db.Book([]byte("Y"))
	if book != 155 {
		t.Fatalf("book=%d want 155", book)
	}
	if ph, _ := f.eng.Phase([]byte("t"), []byte("Y")); ph != count.Done {
		t.Fatalf("phase=%v want Done", ph)
	}
	// mv 只含被接受的 Move：-30+5=-25，调整不计入。
	mv, _ := f.db.Moved([]byte("Y"))
	if mv != -25 {
		t.Fatalf("mv=%d want -25", mv)
	}
	if err := f.eng.Close([]byte("t")); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestReject(t *testing.T) {
	f := newFixture(t, 2, 5, 500)
	_ = f.m.Grant([]byte("u3"), true, false)
	if err := f.m.Reject([]byte("t"), []byte("Y"), []byte("u3")); err != nil {
		t.Fatal(err)
	}
	book, _ := f.db.Book([]byte("Y"))
	if book != 170 {
		t.Fatalf("book=%d want 170 (no adjustment)", book)
	}
	if ph, _ := f.eng.Phase([]byte("t"), []byte("Y")); ph != count.Done {
		t.Fatalf("phase=%v want Done", ph)
	}
}

func TestCounterCannotApprove(t *testing.T) {
	// 三盘路径产生三个盘点人 u1/u2/u3，谁都不能审批。
	db := adjust.New(2, 5, 500)
	_ = db.AddLoc([]byte("Y"), 200, 10)
	eng := count.New(db)
	_ = eng.Open([]byte("t"), [][]byte{[]byte("Y")})
	_ = eng.Submit([]byte("t"), []byte("Y"), 180, []byte("u1"))
	_ = db.Move([]byte("Y"), -30)
	_ = eng.Submit([]byte("t"), []byte("Y"), 152, []byte("u2")) // 不一致 -> Third
	_ = eng.Submit([]byte("t"), []byte("Y"), 151, []byte("u3")) // Pending(-19)
	m := New(eng)
	for _, u := range []string{"u1", "u2", "u3"} {
		_ = m.Grant([]byte(u+"s"), false, false)
		_ = m.Grant([]byte(u), true, true)
		if err := m.Approve([]byte("t"), []byte("Y"), []byte(u)); !errors.Is(err, ErrRotate) {
			t.Fatalf("%s approve: %v want ErrRotate", u, err)
		}
	}
	// 申请仍在 Pending。
	if ph, _ := eng.Phase([]byte("t"), []byte("Y")); ph != count.Pending {
		t.Fatalf("phase=%v still Pending", ph)
	}
}

func TestAmountBoundary(t *testing.T) {
	// price=10：|diff|=50 -> 500 恰等 Lim，不需 Senior；51 -> 510 需 Senior。
	cases := []struct {
		name    string
		diff    int64
		senior  bool
		wantErr error
	}{
		{"equal lim no senior", -50, false, nil},
		{"over lim no senior", -51, false, ErrNoSenior},
		{"over lim with senior", -51, true, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := adjust.New(0, 0, 500)
			_ = db.AddLoc([]byte("Y"), 1000, 10)
			eng := count.New(db)
			_ = eng.Open([]byte("t"), [][]byte{[]byte("Y")})
			_ = eng.Submit([]byte("t"), []byte("Y"), 1000+tc.diff, []byte("u1")) // First 超差
			_ = eng.Submit([]byte("t"), []byte("Y"), 1000+tc.diff, []byte("u2")) // Second 一致 -> Pending
			if ph, _ := eng.Phase([]byte("t"), []byte("Y")); ph != count.Pending {
				t.Fatalf("setup phase=%v want Pending", ph)
			}
			m := New(eng)
			_ = m.Grant([]byte("boss"), true, tc.senior)
			err := m.Approve([]byte("t"), []byte("Y"), []byte("boss"))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil {
				book, _ := db.Book([]byte("Y"))
				if book != 1000+tc.diff {
					t.Fatalf("book=%d want %d", book, 1000+tc.diff)
				}
			}
		})
	}
}

func TestApproveUnderflowRetry(t *testing.T) {
	// diff=-20，审批前 Move 使 book=15：批准报库存不足，申请保持 Pending，可重试。
	f := newFixture(t, 2, 5, 500) // book=170, Pending(-20)
	if err := f.db.Move([]byte("Y"), -155); err != nil {
		t.Fatal(err)
	}
	book, _ := f.db.Book([]byte("Y"))
	if book != 15 {
		t.Fatalf("book=%d want 15", book)
	}
	_ = f.m.Grant([]byte("u3"), true, false)
	if err := f.m.Approve([]byte("t"), []byte("Y"), []byte("u3")); !errors.Is(err, ErrStock) {
		t.Fatalf("err=%v want ErrStock", err)
	}
	if ph, _ := f.eng.Phase([]byte("t"), []byte("Y")); ph != count.Pending {
		t.Fatalf("phase=%v want Pending", ph)
	}
	book, _ = f.db.Book([]byte("Y"))
	if book != 15 {
		t.Fatalf("book unchanged=%d", book)
	}
	// 补货后重试成功。
	if err := f.db.Move([]byte("Y"), 20); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Approve([]byte("t"), []byte("Y"), []byte("u3")); err != nil {
		t.Fatalf("retry: %v", err)
	}
	book, _ = f.db.Book([]byte("Y"))
	if book != 15 { // 35-20
		t.Fatalf("book=%d want 15", book)
	}
}

func TestPermissionAndOrder(t *testing.T) {
	f := newFixture(t, 2, 5, 500)
	// 参数非法最先
	if err := f.m.Approve(nil, []byte("Y"), []byte("u3")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid: %v", err)
	}
	// 不存在先于状态：任务不存在
	if err := f.m.Approve([]byte("nope"), []byte("Y"), []byte("u3")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no task: %v", err)
	}
	// 库位不在任务中
	if err := f.m.Approve([]byte("t"), []byte("Z"), []byte("u3")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("loc not in task: %v", err)
	}
	// 状态不符先于权限：另开 X 未 Pending
	_ = f.db.AddLoc([]byte("X"), 100, 10)
	_ = f.eng.Open([]byte("t2"), [][]byte{[]byte("X")})
	if err := f.m.Approve([]byte("t2"), []byte("X"), []byte("u3")); !errors.Is(err, ErrState) {
		t.Fatalf("not pending: %v", err)
	}
	// 无 Approve 权限先于换人
	if err := f.m.Approve([]byte("t"), []byte("Y"), []byte("u1")); !errors.Is(err, ErrNoApprove) {
		t.Fatalf("no perm: %v", err)
	}
	// 授权后 u1 撞换人约束
	_ = f.m.Grant([]byte("u1"), true, true)
	if err := f.m.Approve([]byte("t"), []byte("Y"), []byte("u1")); !errors.Is(err, ErrRotate) {
		t.Fatalf("rotate: %v", err)
	}
}
