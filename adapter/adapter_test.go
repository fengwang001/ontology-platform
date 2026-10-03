package adapter

import (
	"sync"
	"testing"
)

type mutOp struct {
	name     string
	run      func(r *Registry) error
	wantKind Kind
	wantErr  bool
	wantVer  uint64 // 操作后期望的注册表版本号
}

func TestMutations(t *testing.T) {
	tests := []struct {
		name string
		h    int
		ops  []mutOp
	}{
		{
			name: "set adapter increments and overwrites",
			h:    4,
			ops: []mutOp{
				{name: "set 1", run: func(r *Registry) error { return r.SetAdapter(1, false, true) }, wantVer: 1},
				{name: "set 3", run: func(r *Registry) error { return r.SetAdapter(3, true, false) }, wantVer: 2},
				{name: "overwrite 1", run: func(r *Registry) error { return r.SetAdapter(1, true, true) }, wantVer: 3},
			},
		},
		{
			name: "set adapter invalid v",
			h:    4,
			ops: []mutOp{
				{name: "v=0", run: func(r *Registry) error { return r.SetAdapter(0, false, false) }, wantErr: true, wantKind: KindInvalidArgument, wantVer: 0},
				{name: "v=H", run: func(r *Registry) error { return r.SetAdapter(4, false, false) }, wantErr: true, wantKind: KindInvalidArgument, wantVer: 0},
				{name: "v negative", run: func(r *Registry) error { return r.SetAdapter(-1, false, false) }, wantErr: true, wantKind: KindInvalidArgument, wantVer: 0},
			},
		},
		{
			name: "remove adapter",
			h:    4,
			ops: []mutOp{
				{name: "set 2", run: func(r *Registry) error { return r.SetAdapter(2, false, false) }, wantVer: 1},
				{name: "remove 2", run: func(r *Registry) error { return r.RemoveAdapter(2) }, wantVer: 2},
				{name: "remove 2 again", run: func(r *Registry) error { return r.RemoveAdapter(2) }, wantErr: true, wantKind: KindNotFound, wantVer: 2},
				{name: "remove never registered", run: func(r *Registry) error { return r.RemoveAdapter(1) }, wantErr: true, wantKind: KindNotFound, wantVer: 2},
				{name: "remove invalid beats notfound", run: func(r *Registry) error { return r.RemoveAdapter(9) }, wantErr: true, wantKind: KindInvalidArgument, wantVer: 2},
			},
		},
		{
			name: "sunset validation and overwrite",
			h:    4,
			ops: []mutOp{
				{name: "sunset 3@100", run: func(r *Registry) error { return r.Sunset(3, 100) }, wantVer: 1},
				{name: "sunset 3@0 overwrite", run: func(r *Registry) error { return r.Sunset(3, 0) }, wantVer: 2},
				{name: "sunset v=H", run: func(r *Registry) error { return r.Sunset(4, 0) }, wantErr: true, wantKind: KindInvalidArgument, wantVer: 2},
				{name: "sunset t negative", run: func(r *Registry) error { return r.Sunset(1, -1) }, wantErr: true, wantKind: KindInvalidArgument, wantVer: 2},
				{name: "sunset t too large", run: func(r *Registry) error { return r.Sunset(1, MaxTime+1) }, wantErr: true, wantKind: KindInvalidArgument, wantVer: 2},
				{name: "sunset t max ok", run: func(r *Registry) error { return r.Sunset(1, MaxTime) }, wantVer: 3},
			},
		},
		{
			name: "preview validation",
			h:    4,
			ops: []mutOp{
				{name: "preview head ok", run: func(r *Registry) error { return r.Preview(4, "beta") }, wantVer: 1},
				{name: "preview v=0", run: func(r *Registry) error { return r.Preview(0, "beta") }, wantErr: true, wantKind: KindInvalidArgument, wantVer: 1},
				{name: "preview v=H+1", run: func(r *Registry) error { return r.Preview(5, "beta") }, wantErr: true, wantKind: KindInvalidArgument, wantVer: 1},
				{name: "preview empty scope", run: func(r *Registry) error { return r.Preview(2, "") }, wantErr: true, wantKind: KindInvalidArgument, wantVer: 1},
				{name: "preview overwrite", run: func(r *Registry) error { return r.Preview(4, "rc") }, wantVer: 2},
			},
		},
		{
			name: "h=1 rejects all adapter and sunset",
			h:    1,
			ops: []mutOp{
				{name: "set 1", run: func(r *Registry) error { return r.SetAdapter(1, false, false) }, wantErr: true, wantKind: KindInvalidArgument, wantVer: 0},
				{name: "sunset 1", run: func(r *Registry) error { return r.Sunset(1, 0) }, wantErr: true, wantKind: KindInvalidArgument, wantVer: 0},
				{name: "preview 1 ok", run: func(r *Registry) error { return r.Preview(1, "beta") }, wantVer: 1},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := New(tt.h)
			if err != nil {
				t.Fatalf("New(%d) err = %v", tt.h, err)
			}
			for _, op := range tt.ops {
				err := op.run(r)
				if op.wantErr {
					aerr, ok := err.(*Error)
					if !ok {
						t.Fatalf("%s: err = %v, want *Error kind %d", op.name, err, op.wantKind)
					}
					if aerr.Kind != op.wantKind {
						t.Fatalf("%s: kind = %d, want %d", op.name, aerr.Kind, op.wantKind)
					}
				} else if err != nil {
					t.Fatalf("%s: unexpected err = %v", op.name, err)
				}
				if got := r.Version(); got != op.wantVer {
					t.Fatalf("%s: version = %d, want %d", op.name, got, op.wantVer)
				}
			}
		})
	}
}

func TestNewInvalidH(t *testing.T) {
	for _, h := range []int{0, -1, 1001} {
		if _, err := New(h); err == nil {
			t.Fatalf("New(%d) expected error", h)
		}
	}
	if _, err := New(1); err != nil {
		t.Fatalf("New(1) err = %v", err)
	}
	if _, err := New(1000); err != nil {
		t.Fatalf("New(1000) err = %v", err)
	}
}

func TestSnapshotIsolation(t *testing.T) {
	r, _ := New(3)
	if err := r.SetAdapter(1, false, false); err != nil {
		t.Fatal(err)
	}
	snap := r.Snapshot()
	if snap.Version != 1 || len(snap.Steps) != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}
	// 快照之后的变更不得影响已取得的快照。
	if err := r.SetAdapter(2, true, true); err != nil {
		t.Fatal(err)
	}
	if err := r.Sunset(1, 10); err != nil {
		t.Fatal(err)
	}
	if len(snap.Steps) != 1 || len(snap.Sunsets) != 0 || snap.Version != 1 {
		t.Fatalf("snapshot mutated: %+v", snap)
	}
	if r.Version() != 3 {
		t.Fatalf("version = %d, want 3", r.Version())
	}
}

func TestConcurrent(t *testing.T) {
	r, _ := New(8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				v := (i+j)%7 + 1
				_ = r.SetAdapter(v, j%2 == 0, j%3 == 0)
				_ = r.RemoveAdapter(v)
				_ = r.Sunset(v, int64(j))
				_ = r.Preview(v, "s")
				_ = r.Snapshot()
				_ = r.Version()
			}
		}(i)
	}
	wg.Wait()
	// 每个成功的 SetAdapter 都使版本号 +1，最终版本号不得回退。
	if r.Version() == 0 {
		t.Fatal("version should have advanced")
	}
}
