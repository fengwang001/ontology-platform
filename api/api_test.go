package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"ontology/api"
)

func apply(s *api.Service, step string) error {
	p := strings.SplitN(step, ":", 3)
	switch p[0] {
	case "L":
		return s.Left(p[1], p[2])
	case "U":
		return s.UpsertRight(p[1], p[2])
	}
	return s.DeleteRight(p[1])
}
func batchViews(steps []string) (map[string]string, map[string]bool) {
	right, keyOf := map[string]string{}, map[string]string{}
	val, match := map[string]string{}, map[string]bool{}
	scan := func(key string) {
		for id, k := range keyOf {
			if k == key {
				val[id], match[id] = right[key]
			}
		}
	}
	for _, st := range steps {
		p := strings.SplitN(st, ":", 3)
		switch p[0] {
		case "L":
			keyOf[p[1]] = p[2]
			val[p[1]], match[p[1]] = right[p[2]]
		case "U":
			right[p[1]] = p[2]
			scan(p[1])
		case "D":
			delete(right, p[1])
			for id, k := range keyOf {
				if k == p[1] {
					delete(keyOf, id)
					delete(val, id)
					delete(match, id)
				}
			}
		}
	}
	return val, match
}

// TestBatchConsistency 钉不变量 1：随机操作序列后，视图逐 leftID 等于批量重算。
func TestBatchConsistency(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 4, 5} {
		svc := api.New()
		r := rand.New(rand.NewSource(seed))
		var steps []string
		for i := 0; i < 300; i++ {
			k := fmt.Sprintf("k%d", r.Intn(5))
			steps = append(steps, []string{fmt.Sprintf("L:L%d:%s", i, k), fmt.Sprintf("U:%s:v%d", k, i), "D:" + k}[r.Intn(3)])
		}
		for _, st := range steps {
			if err := apply(svc, st); err != nil {
				t.Fatalf("seed=%d %s: %v", seed, st, err)
			}
		}
		val, match := batchViews(steps)
		for id, want := range val {
			if v, ok := svc.GetView(id); v != want || ok != match[id] {
				t.Fatalf("seed=%d %s: got (%q,%v) want (%q,%v)", seed, id, v, ok, want, match[id])
			}
		}
	}
}

// TestUpdateDeletePropagation 钉不变量 3：增改删立即反映到受影响左事件。
func TestUpdateDeletePropagation(t *testing.T) {
	cases := []struct{ name, seq, want string }{
		{"补join", "L:L:k;U:k:v", "v"},
		{"更新立即生效", "U:k:v1;L:L:k;U:k:v2", "v2"},
		{"删除置空", "U:k:v;L:L:k;D:k", ""},
		{"删除幂等", "D:k;L:L:k", ""},
	}
	for _, c := range cases {
		s := api.New()
		for _, st := range strings.Split(c.seq, ";") {
			if err := apply(s, st); err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
		}
		if v, ok := s.GetView("L"); v != c.want || ok != (c.want != "") {
			t.Fatalf("%s: got (%q,%v) want (%q,%v)", c.name, v, ok, c.want, c.want != "")
		}
	}
}

// TestFailureAtomicity 钉不变量 4：四类拒绝互不相同、不留痕、拒绝后可用。
func TestFailureAtomicity(t *testing.T) {
	cases := []struct {
		name, step string
		want       error
	}{
		{"Left空leftID", "L::k1", api.ErrEmptyLeftID},
		{"Left空key", "L:Lx:", api.ErrEmptyLeftKey},
		{"leftID重复", "L:L1:k1", api.ErrDupLeftID},
		{"Upsert空key", "U::v", api.ErrEmptyRightKey},
		{"Delete空key", "D:", api.ErrEmptyRightKey},
	}
	s := api.New()
	_ = s.UpsertRight("k1", "v1")
	_ = s.Left("L1", "k1")
	for _, c := range cases {
		if err := apply(s, c.step); !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v want %v", c.name, err, c.want)
		}
		if v, ok := s.GetView("L1"); v != "v1" || !ok {
			t.Fatalf("%s: 被拒操作改变了状态", c.name)
		}
	}
	if err := s.UpsertRight("k1", "v2"); err != nil {
		t.Fatalf("拒绝后不可用: %v", err)
	}
	if v, _ := s.GetView("L1"); v != "v2" {
		t.Fatal("拒绝后视图不更新")
	}
	if len(map[error]bool{api.ErrEmptyLeftID: true, api.ErrEmptyLeftKey: true, api.ErrDupLeftID: true, api.ErrEmptyRightKey: true}) != 4 {
		t.Fatal("四类哨兵错误不互异")
	}
}
func TestSelfCheckConcurrent(t *testing.T) {
	s := api.New()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.SelfCheck(); err != nil {
				t.Error(err)
			}
			_, _ = s.GetView("L")
		}()
	}
	wg.Wait()
}
