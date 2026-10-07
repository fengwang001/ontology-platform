package link

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// memObjects 是测试用的对象实例注册表，支持逻辑删除。
type memObjects struct {
	mu    sync.Mutex
	types map[ObjectInstanceID]ObjectTypeID
}

func newMemObjects() *memObjects {
	return &memObjects{types: map[ObjectInstanceID]ObjectTypeID{}}
}

func (m *memObjects) add(id ObjectInstanceID, typ ObjectTypeID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.types[id] = typ
}

func (m *memObjects) delete(id ObjectInstanceID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.types, id)
}

func (m *memObjects) Lookup(id ObjectInstanceID) (ObjectTypeID, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	typ, ok := m.types[id]
	return typ, ok
}

func disc(k, v string) map[string]string {
	return map[string]string{k: v}
}

func mustCreate(t *testing.T, s *Store, req CreateRequest) *Link {
	t.Helper()
	l, err := s.Create(req)
	if err != nil {
		t.Fatalf("unexpected create error: %v", err)
	}
	return l
}

// TestBidirectionalCapsMatrix 覆盖双方向上限为 0/1/不限的组合边界。
func TestBidirectionalCapsMatrix(t *testing.T) {
	caps := []Cardinality{Limited(0), Limited(1), Unlimited()}

	for fi, fc := range caps {
		for bi, bc := range caps {
			t.Run(fmt.Sprintf("f%d_b%d", fi, bi), func(t *testing.T) {
				objs := newMemObjects()
				objs.add("a", "A")
				objs.add("b", "B")
				s := NewStore(objs)
				lt := LinkType{
					ID: "lt", SourceType: "A", TargetType: "B",
					ForwardCap: fc, BackwardCap: bc,
				}
				if err := s.RegisterType(lt); err != nil {
					t.Fatal(err)
				}

				check := func(req CreateRequest, wantErr error) {
					t.Helper()
					_, err := s.Create(req)
					if !errors.Is(err, wantErr) {
						t.Fatalf("cap f=%v b=%v req %s->%s: got %v want %v",
							fc, bc, req.SourceID, req.TargetID, err, wantErr)
					}
				}

				check(CreateRequest{"lt", "a", "b", disc("k", "1")}, expectOK(fc))
				check(CreateRequest{"lt", "a", "b", disc("k", "2")}, expectSecond(fc))
				check(CreateRequest{"lt", "b", "a", disc("k", "1")}, expectOK(bc))
				check(CreateRequest{"lt", "b", "a", disc("k", "2")}, expectSecond(bc))

				if expectOK(fc) == nil {
					_, err := s.Create(CreateRequest{"lt", "a", "b", disc("k", "1")})
					if !errors.Is(err, ErrDuplicateLink) {
						t.Fatalf("duplicate: got %v", err)
					}
				}

				wantFwd := effectiveCount(fc, 2)
				wantBwd := effectiveCount(bc, 2)
				if got := s.CountDirection("lt", "a", DirectionForward); got != wantFwd {
					t.Fatalf("fwd count: got %d want %d", got, wantFwd)
				}
				if got := s.CountDirection("lt", "b", DirectionBackward); got != wantBwd {
					t.Fatalf("bwd count: got %d want %d", got, wantBwd)
				}
			})
		}
	}
}

func expectOK(c Cardinality) error {
	if !c.Unlimited && c.Limit == 0 {
		return ErrCardinalityExceeded
	}
	return nil
}

func expectSecond(c Cardinality) error {
	if c.Unlimited {
		return nil
	}
	if c.Limit <= 1 {
		return ErrCardinalityExceeded
	}
	return nil
}

func effectiveCount(c Cardinality, offered int) int {
	if c.Unlimited {
		return offered
	}
	if offered < c.Limit {
		return offered
	}
	return c.Limit
}
