package query

import (
	"sync"
	"testing"

	"ontology/role"
)

// TestConcurrentSmoke 在 -race 下验证所有操作并发安全：
// 不检查具体返回值，只要求无数据竞争、无 panic、无越界。
func TestConcurrentSmoke(t *testing.T) {
	s := NewStore()
	s.PutDoc("logs-1", "d1", map[string]role.Value{"team": "a", "level": int64(3), "secret": "k1"})
	s.PutDoc("logs-1", "d2", map[string]role.Value{"team": "b", "level": int64(5), "secret": "k2"})
	reg := s.Registry()
	r1 := role.Entry{Pattern: "logs-*", Filter: ePtr(eTerm(t, "team", "a")),
		Fields: role.FieldAuth{Grant: []string{"team", "level"}}}
	r2 := role.Entry{Pattern: "logs-1", Filter: ePtr(eRange(t, "level", 5, 9)),
		Fields: role.FieldAuth{Grant: []string{"*"}, Except: []string{"secret"}}}
	if err := reg.PutRole("R1", []role.Entry{r1}); err != nil {
		t.Fatal(err)
	}
	if err := reg.PutRole("R2", []role.Entry{r2}); err != nil {
		t.Fatal(err)
	}
	if err := reg.BindUser("u", []string{"R1", "R2"}); err != nil {
		t.Fatal(err)
	}

	q := eNot(t, eTerm(t, "secret", "k1"))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				switch (i + k) % 6 {
				case 0:
					s.Search("u", "logs-1", q, 10)
				case 1:
					s.Get("u", "logs-1", "d1")
				case 2:
					s.Agg("u", "logs-1", "level")
				case 3:
					s.PutDoc("logs-1", "d1", map[string]role.Value{"team": "a", "level": int64(3)})
				case 4:
					_ = reg.PutRole("R1", []role.Entry{r1})
				case 5:
					s.Search("ghost", "logs-1", q, 10)
				}
			}
		}(i)
	}
	wg.Wait()
}
