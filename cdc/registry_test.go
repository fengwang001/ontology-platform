package cdc

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

// TestRegisterValidation 非法类型、空列名、重名列在注册时被拒，
// 且失败不改变注册表。
func TestRegisterValidation(t *testing.T) {
	cases := []struct {
		name string
		cols []Column
		want error
	}{
		{"invalid type", []Column{{Name: "a", Type: ColumnType(42)}}, ErrInvalidColumnType},
		{"empty name", []Column{{Name: "", Type: TypeInt}}, ErrEmptyColumnName},
		{"duplicate name", []Column{
			{Name: "a", Type: TypeInt},
			{Name: "a", Type: TypeString},
		}, ErrDuplicateColumn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRegistry()
			_, err := r.Register(tc.cols)
			t.Logf("register %v -> %v", tc.cols, err)
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
			if _, ok := r.Get(1); ok {
				t.Fatal("failed register must not change the registry")
			}
		})
	}
}

// TestEvolutionAddDropAlter 增删改列各生成新版本，旧版本快照保持不变。
func TestEvolutionAddDropAlter(t *testing.T) {
	r := NewRegistry()
	v1, err := r.Register([]Column{
		{Name: "id", Type: TypeInt, Required: true},
		{Name: "name", Type: TypeString},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	v2, err := r.AddColumn(v1, Column{Name: "age", Type: TypeInt})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	v3, err := r.DropColumn(v2, "name")
	if err != nil {
		t.Fatalf("drop: %v", err)
	}
	v4, err := r.AlterColumn(v3, Column{Name: "age", Type: TypeString, Required: true})
	if err != nil {
		t.Fatalf("alter: %v", err)
	}
	t.Logf("evolution chain: v%d -> add age -> v%d -> drop name -> v%d -> alter age -> v%d", v1, v2, v3, v4)

	want := map[int][]Column{
		v1: {{Name: "id", Type: TypeInt, Required: true}, {Name: "name", Type: TypeString}},
		v2: {{Name: "id", Type: TypeInt, Required: true}, {Name: "name", Type: TypeString}, {Name: "age", Type: TypeInt}},
		v3: {{Name: "id", Type: TypeInt, Required: true}, {Name: "age", Type: TypeInt}},
		v4: {{Name: "id", Type: TypeInt, Required: true}, {Name: "age", Type: TypeString, Required: true}},
	}
	for v, cols := range want {
		s, ok := r.Get(v)
		if !ok {
			t.Fatalf("version %d missing", v)
		}
		if !reflect.DeepEqual(s.Columns, cols) {
			t.Fatalf("version %d columns = %v, want %v", v, s.Columns, cols)
		}
		t.Logf("version %d snapshot intact: %s", v, formatColumns(s.Columns))
	}
}

// TestEvolutionRejects 增删改不存在的列、版本未注册等场景被拒，
// 且失败不产生新版本、不改变已有快照。
func TestEvolutionRejects(t *testing.T) {
	r := NewRegistry()
	v1, err := r.Register([]Column{{Name: "id", Type: TypeInt, Required: true}})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	cases := []struct {
		name string
		op   func() (int, error)
		want error
	}{
		{"add existing column", func() (int, error) {
			return r.AddColumn(v1, Column{Name: "id", Type: TypeString})
		}, ErrColumnExists},
		{"add invalid column", func() (int, error) {
			return r.AddColumn(v1, Column{Name: "x", Type: ColumnType(-1)})
		}, ErrInvalidColumnType},
		{"add empty name", func() (int, error) {
			return r.AddColumn(v1, Column{Name: "", Type: TypeInt})
		}, ErrEmptyColumnName},
		{"drop missing column", func() (int, error) {
			return r.DropColumn(v1, "nope")
		}, ErrColumnNotFound},
		{"alter missing column", func() (int, error) {
			return r.AlterColumn(v1, Column{Name: "nope", Type: TypeInt})
		}, ErrColumnNotFound},
		{"evolve unregistered version", func() (int, error) {
			return r.AddColumn(999, Column{Name: "x", Type: TypeInt})
		}, ErrVersionNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.op()
			t.Logf("op rejected: %v", err)
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
			if _, ok := r.Get(v1 + 1); ok {
				t.Fatal("failed evolution must not create a new version")
			}
			s, _ := r.Get(v1)
			if !reflect.DeepEqual(s.Columns, []Column{{Name: "id", Type: TypeInt, Required: true}}) {
				t.Fatalf("failed evolution mutated v1: %v", s.Columns)
			}
		})
	}
}

// TestConcurrentProjectionDeterministic 模式持续演进期间并发投影：
// 每次投影基于某一完整版本快照，同版本同输入的结果完全确定。
func TestConcurrentProjectionDeterministic(t *testing.T) {
	r := NewRegistry()
	v1, err := r.Register([]Column{
		{Name: "id", Type: TypeInt, Required: true},
		{Name: "name", Type: TypeString},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	e := Event{Version: v1, Fields: map[string]any{
		"id":   int64(1),
		"name": "alice",
		"tag":  "dropped-later",
	}}

	want, err := r.Project(e, v1)
	if err != nil {
		t.Fatalf("baseline project: %v", err)
	}

	const workers = 8
	const rounds = 200

	var wg sync.WaitGroup
	errs := make(chan error, workers*rounds)

	// 投影方：持续把同一事件投影到 v1，结果必须与基线一致。
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				got, err := r.Project(e, v1)
				if err != nil {
					errs <- err
					return
				}
				if !reflect.DeepEqual(got, want) {
					errs <- &mismatchError{got: got, want: want}
					return
				}
			}
		}()
	}

	// 演进方：持续增删改列，产生大量新版本。
	wg.Add(1)
	go func() {
		defer wg.Done()
		cur := v1
		for i := 0; i < rounds; i++ {
			next, err := r.AddColumn(cur, Column{Name: "extra", Type: TypeString})
			if err != nil {
				errs <- err
				return
			}
			if next, err = r.AlterColumn(next, Column{Name: "name", Type: TypeInt}); err != nil {
				errs <- err
				return
			}
			if cur, err = r.DropColumn(next, "extra"); err != nil {
				errs <- err
				return
			}
		}
	}()

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent projection: %v", err)
	}
	t.Logf("all %d goroutines x %d rounds projected deterministically during %d schema evolutions", workers, rounds, rounds*3)
}

type mismatchError struct {
	got, want map[string]any
}

func (e *mismatchError) Error() string {
	return "non-deterministic projection: got " + formatFields(e.got) + " want " + formatFields(e.want)
}
