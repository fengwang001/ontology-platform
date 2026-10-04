package node_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"ontology/node"
)

func TestErrors(t *testing.T) {
	tests := []struct {
		name string
		fn   func() error
		want error
	}{
		{"new-water-bad", func() error { _, e := node.New(90, 80); return e }, node.ErrInvalid},
		{"new-water-zero", func() error { _, e := node.New(0, 80); return e }, node.ErrInvalid},
		{"new-water-over", func() error { _, e := node.New(1, 101); return e }, node.ErrInvalid},
		{"addnode-empty-id", func() error { c, _ := node.New(80, 90); return c.AddNode("", "z1", 100) }, node.ErrInvalid},
		{"addnode-long-id", func() error { c, _ := node.New(80, 90); return c.AddNode(strings.Repeat("a", 65), "z1", 100) }, node.ErrInvalid},
		{"addnode-total-zero", func() error { c, _ := node.New(80, 90); return c.AddNode("n1", "z1", 0) }, node.ErrInvalid},
		{"addnode-total-big", func() error {
			c, _ := node.New(80, 90)
			return c.AddNode("n1", "z1", node.MaxSize+1)
		}, node.ErrInvalid},
		{"addnode-dup", func() error {
			c, _ := node.New(80, 90)
			_ = c.AddNode("n1", "z1", 100)
			return c.AddNode("n1", "z2", 100)
		}, node.ErrExists},
		{"setother-bad-id", func() error { c, _ := node.New(80, 90); return c.SetOther("", 1) }, node.ErrInvalid},
		{"setother-missing", func() error { c, _ := node.New(80, 90); return c.SetOther("n9", 1) }, node.ErrNotFound},
		{"setother-overflow", func() error {
			c, _ := node.New(80, 90)
			_ = c.AddNode("n1", "z1", 100)
			return c.SetOther("n1", -1)
		}, node.ErrInvalid},
		{"setexclude-missing", func() error { c, _ := node.New(80, 90); return c.SetExclude("n9", true) }, node.ErrNotFound},
		{"create-bad", func() error { c, _ := node.New(80, 90); return c.CreateIndex("", 1, 0, 1) }, node.ErrInvalid},
		{"create-s65", func() error { c, _ := node.New(80, 90); return c.CreateIndex("x", 65, 0, 1) }, node.ErrInvalid},
		{"create-r6", func() error { c, _ := node.New(80, 90); return c.CreateIndex("x", 1, 6, 1) }, node.ErrInvalid},
		{"create-size0", func() error { c, _ := node.New(80, 90); return c.CreateIndex("x", 1, 0, 0) }, node.ErrInvalid},
		{"create-dup", func() error {
			c, _ := node.New(80, 90)
			_ = c.CreateIndex("x", 1, 0, 1)
			return c.CreateIndex("x", 2, 0, 1)
		}, node.ErrExists},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.fn(); !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestRejectionNoSideEffect(t *testing.T) {
	c, _ := node.New(80, 90)
	_ = c.AddNode("n1", "z1", 100)
	_ = c.CreateIndex("x", 1, 1, 10)

	// 非法 SetOther 不应改变用量
	before := c.SortedNodeIDs()
	if err := c.SetOther("n1", node.MaxSize+1); !errors.Is(err, node.ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
	if ids := c.SortedNodeIDs(); len(ids) != len(before) {
		t.Fatalf("state changed after rejected op")
	}
	// 重复 AddNode 后新参数不得生效（zone/总量）
	if err := c.AddNode("n1", "zz", 999); !errors.Is(err, node.ErrExists) {
		t.Fatalf("want ErrExists, got %v", err)
	}
	_, used, total, _, _ := c.NodeView("n1")
	if used != 0 || total != 100 {
		t.Fatalf("node mutated by rejected AddNode: used=%d total=%d", used, total)
	}
}

func TestInvalidBeforeExists(t *testing.T) {
	c, _ := node.New(80, 90)
	_ = c.AddNode("n1", "z1", 100)
	// id 非法且已存在：非法优先
	if err := c.AddNode("n1", "z1", 0); !errors.Is(err, node.ErrInvalid) {
		t.Fatalf("invalid must precede exists, got %v", err)
	}
}

func TestConcurrentOps(t *testing.T) {
	c, err := node.New(80, 90)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				id := "n" + itoa(g*50+k)
				if err := c.AddNode(id, "z"+itoa((g+k)%4), 1000); err != nil &&
					!errors.Is(err, node.ErrExists) {
					t.Errorf("add: %v", err)
					return
				}
				_ = c.SetOther(id, int64(k%100))
				_ = c.SetExclude(id, k%3 == 0)
				idx := "idx" + itoa(g*50+k)
				if err := c.CreateIndex(idx, 1+(k%4), k%6, int64(1+k%50)); err != nil &&
					!errors.Is(err, node.ErrExists) {
					t.Errorf("index: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
