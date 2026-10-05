package rule

import (
	"errors"
	"reflect"
	"testing"
)

func TestPutDrop(t *testing.T) {
	tests := []struct {
		name    string
		ops     []func(s *Store) error
		wantErr []error // 与 ops 一一对应，nil 表示成功
		wantRV  uint64
	}{
		{
			name: "新增与覆盖均使 rv 加 1",
			ops: []func(s *Store) error{
				func(s *Store) error { return s.Put(Rule{ID: "r1", Field: "amt", Lo: 0, Hi: 100, Severity: Block}) },
				func(s *Store) error { return s.Put(Rule{ID: "r1", Field: "amt", Lo: 0, Hi: 200, Severity: Warn}) },
			},
			wantErr: []error{nil, nil},
			wantRV:  2,
		},
		{
			name: "非法参数被拒绝且不占版本",
			ops: []func(s *Store) error{
				func(s *Store) error { return s.Put(Rule{ID: "", Field: "amt", Lo: 0, Hi: 1, Severity: Block}) },
				func(s *Store) error { return s.Put(Rule{ID: "r1", Field: "", Lo: 0, Hi: 1, Severity: Block}) },
				func(s *Store) error { return s.Put(Rule{ID: "r1", Field: "amt", Lo: 2, Hi: 1, Severity: Block}) },
				func(s *Store) error { return s.Put(Rule{ID: "r1", Field: "amt", Lo: 0, Hi: 1, Severity: Severity(9)}) },
			},
			wantErr: []error{ErrInvalid, ErrInvalid, ErrInvalid, ErrInvalid},
			wantRV:  0,
		},
		{
			name: "删除存在与不存在",
			ops: []func(s *Store) error{
				func(s *Store) error { return s.Put(Rule{ID: "r1", Field: "amt", Lo: 0, Hi: 1, Severity: Block}) },
				func(s *Store) error { return s.Drop("r1") },
				func(s *Store) error { return s.Drop("r1") },
				func(s *Store) error { return s.Drop("") },
			},
			wantErr: []error{nil, nil, ErrNoRule, ErrInvalid},
			wantRV:  2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewStore()
			for i, op := range tt.ops {
				err := op(s)
				if tt.wantErr[i] == nil {
					if err != nil {
						t.Fatalf("op %d: unexpected error %v", i, err)
					}
				} else if !errors.Is(err, tt.wantErr[i]) {
					t.Fatalf("op %d: got %v, want errors.Is %v", i, err, tt.wantErr[i])
				}
			}
			if s.RV() != tt.wantRV {
				t.Fatalf("rv = %d, want %d", s.RV(), tt.wantRV)
			}
		})
	}
}

func TestEval(t *testing.T) {
	s := NewStore()
	mustPut := func(r Rule) {
		t.Helper()
		if err := s.Put(r); err != nil {
			t.Fatalf("put %+v: %v", r, err)
		}
	}
	mustPut(Rule{ID: "r2", Field: "qty", Lo: 1, Hi: 10, Severity: Warn})
	mustPut(Rule{ID: "r1", Field: "amt", Lo: 0, Hi: 100, Severity: Block})

	tests := []struct {
		name      string
		fields    map[string]int64
		want      []string
		wantBlock bool
	}{
		{"全部通过", map[string]int64{"amt": 50, "qty": 5}, nil, false},
		{"闭区间边界", map[string]int64{"amt": 0, "qty": 10}, nil, false},
		{"越界与缺失，id 升序", map[string]int64{"amt": 150}, []string{"r1", "r2"}, true},
		{"仅 Warn 违规", map[string]int64{"amt": 50, "qty": 0}, []string{"r2"}, false},
		{"字段缺失即违规", map[string]int64{"qty": 5}, []string{"r1"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.Eval(tt.fields)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Eval(%v) = %v, want %v", tt.fields, got, tt.want)
			}
			if b := s.HasBlock(got); b != tt.wantBlock {
				t.Fatalf("HasBlock(%v) = %v, want %v", got, b, tt.wantBlock)
			}
		})
	}
}
