// Package api 是亲和键分区存储的对外入口。依赖方向单向：route ← shard ← api。
package api

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/internal/route"
	"ontology/internal/shard"
)

// 三类可判定且互不相同的哨兵错误。
var (
	ErrInvalidN            = errors.New("partition count must be positive") // New 的 n / Rebalance 的 newN 非正
	ErrPartitionOutOfRange = errors.New("partition index out of range")     // GetPartition 的 p 不在 [0,N)
	ErrEmptyKey            = errors.New("key must not be empty")            // Put/Get/GetPartition 的 key 为空串
)

// Store 是并发安全的进程内亲和键分区存储。
type Store struct{ sh *shard.Shard }

// New 创建 n 个分区；n 非正时整体失败，不产生任何状态。
func New(n int) (*Store, error) {
	if n <= 0 {
		return nil, ErrInvalidN
	}
	return &Store{sh: shard.New(n)}, nil
}

// Put 写入 key；key 为空串时拒绝且状态不变。
func (s *Store) Put(key, val string) error {
	if key == "" {
		return ErrEmptyKey
	}
	s.sh.Put(key, val)
	return nil
}

// Get 按算术 home 直达分区；found=false、err=nil 即全局 not found。
func (s *Store) Get(key string) (string, bool, error) {
	if key == "" {
		return "", false, ErrEmptyKey
	}
	v, ok := s.sh.Get(key)
	return v, ok, nil
}

// PartitionResult 三情形互斥：Moved=true 表示路由过期（MovedTo=home，Value/Found 无意义）；
// Moved=false 时 Found+Value 是 home 分区的真实存在性。
type PartitionResult struct {
	Value   string
	Found   bool
	Moved   bool
	MovedTo int
}

// GetPartition 处理客户端可能持有的过期路由：p != home 时绝不查 map，直接给
// moved 提示，因此不可能把"别处有此 key"误报成本分区 not found。
func (s *Store) GetPartition(p int, key string) (PartitionResult, error) {
	if key == "" {
		return PartitionResult{}, ErrEmptyKey
	}
	n := s.sh.N()
	if p < 0 || p >= n {
		return PartitionResult{}, ErrPartitionOutOfRange
	}
	home := route.Home(key, n)
	if p != home {
		s.sh.Routed() // 纯算术提示：未检查任何分区内容
		return PartitionResult{Moved: true, MovedTo: home}, nil
	}
	v, ok := s.sh.At(p, key)
	return PartitionResult{Value: v, Found: ok}, nil
}

// Rebalance 同步一次性迁移；newN 非正时拒绝，分区数与内容均不变。
func (s *Store) Rebalance(newN int) error {
	if newN <= 0 {
		return ErrInvalidN
	}
	s.sh.Rebalance(newN)
	return nil
}

// Dump 返回各分区内容的深拷贝快照。
func (s *Store) Dump() []map[string]string { return s.sh.Dump() }

// SelfCheck 在独立实例上跑一组内置操作序列，逐条核验四条不变量；
// 不触碰接收者自身状态，任一不成立返回带说明的错误。
func (s *Store) SelfCheck() error {
	t, err := New(2)
	if err != nil {
		return err
	}
	kv := map[string]string{}
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		v := string(k[0] - 32) // 单字节 ASCII：小写→大写
		if err := t.Put(k, v); err != nil {
			return err
		}
		kv[k] = v
	}
	if err := t.Rebalance(3); err != nil {
		return err
	}
	// 不变量 1（每 key 恰好一份且在 home）+ 2（等于朴素重建）。
	want := make([]map[string]string, 3)
	for i := range want {
		want[i] = map[string]string{}
	}
	for k, v := range kv {
		want[route.Home(k, 3)][k] = v
	}
	if got := t.Dump(); !reflect.DeepEqual(got, want) {
		return fmt.Errorf("invariant 1/2 violated: got %v want %v", got, want)
	}
	// 不变量 3：直读 c="C"；b 持旧路由(0,98%2=0) 必须得到 movedTo=2。
	if v, ok, _ := t.Get("c"); !ok || v != "C" {
		return fmt.Errorf("invariant 3 Get(c)=(%q,%v)", v, ok)
	}
	if r, err := t.GetPartition(0, "b"); err != nil || !r.Moved || r.MovedTo != 2 {
		return fmt.Errorf("invariant 3 GetPartition(0,b)=%+v err=%v", r, err)
	}
	// 不变量 4：拒绝操作可判定（三类互异哨兵），且前后状态逐字节一致。
	before := t.Dump()
	if _, err := New(0); !errors.Is(err, ErrInvalidN) {
		return fmt.Errorf("invariant 4 New(0): %v", err)
	}
	if err := t.Rebalance(0); !errors.Is(err, ErrInvalidN) {
		return fmt.Errorf("invariant 4 Rebalance(0): %v", err)
	}
	if _, err := t.GetPartition(9, "a"); !errors.Is(err, ErrPartitionOutOfRange) {
		return fmt.Errorf("invariant 4 GetPartition(9,a): %v", err)
	}
	if err := t.Put("", "X"); !errors.Is(err, ErrEmptyKey) {
		return fmt.Errorf("invariant 4 Put(empty): %v", err)
	}
	if !reflect.DeepEqual(before, t.Dump()) {
		return errors.New("invariant 4 violated: state changed after rejected ops")
	}
	return nil
}
