// Package api 是列式变更日志增量字典编码的对外入口：New/Append/Tokens/
// Decode/SelfCheck。内部组合 enc（enc 再依赖 dict），依赖方向单向。
package api

import (
	"errors"
	"reflect"
	"sync"

	"ontology/enc"
)

// 三类可判定哨兵错误，两两互不相同：配置非法、值非法、token 非法。
var (
	ErrInvalidConfig = errors.New("api: invalid configuration: K must be > 0")
	ErrInvalidValue  = errors.New("api: empty value not allowed")
	ErrBadToken      = enc.ErrBadToken
)

// Coder 是并发安全的增量字典编码器；状态只存于进程内存。
type Coder struct {
	mu sync.RWMutex
	k  int
	e  *enc.Encoder
}

// New 创建容量 K（code 取值 0..K-1）的编码器；K<=0 返回 ErrInvalidConfig。
func New(k int) (*Coder, error) {
	if k <= 0 {
		return nil, ErrInvalidConfig
	}
	return &Coder{k: k, e: enc.NewEncoder(k)}, nil
}

// Append 增量追加非空值；空串先于任何状态变更被拒，故失败绝不留痕。
func (c *Coder) Append(value string) error {
	if value == "" {
		return ErrInvalidValue
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.e.Append(value)
	return nil
}

// Tokens 返回当前 token 流的快照拷贝，可被多 goroutine 并发调用。
func (c *Coder) Tokens() []enc.Token {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.e.Tokens()
}

// Decode 校验并展开 token 流；坏 token 返回 ErrBadToken 且整体失败。只读，
// 可并发调用，go test -race 干净。
func (c *Coder) Decode(tokens []enc.Token) ([]string, error) {
	c.mu.RLock()
	k := c.k
	c.mu.RUnlock()
	return enc.Decode(k, tokens)
}

// SelfCheck 对内置序列核验四条不变量：往返一致、code 不越界且 m>=1、
// RLE 展开无损、失败不留痕；全部成立返回 nil。
func (c *Coder) SelfCheck() error {
	seq := []string{"a", "a", "a", "b", "c", "d", "e", "a"}
	s, err := New(4)
	if err != nil {
		return err
	}
	for _, v := range seq {
		if err := s.Append(v); err != nil {
			return err
		}
	}
	toks := s.Tokens()
	// 不变量 2：put/ref 的 code∈[0,K)，ref 总次数 m>=1。
	for _, t := range toks {
		if (t.Kind == enc.KindPut || t.Kind == enc.KindRef) && (t.Code < 0 || t.Code >= s.k) {
			return errors.New("selfcheck: code out of bounds")
		}
		if t.Kind == enc.KindRef && t.Count < 1 {
			return errors.New("selfcheck: ref count < 1")
		}
	}
	// 不变量 1：往返逐元素一致。
	got, err := s.Decode(toks)
	if err != nil || !reflect.DeepEqual(got, seq) {
		return errors.New("selfcheck: round-trip mismatch")
	}
	// 不变量 3：展开 RLE 后解码仍逐元素等于原序列。
	if got2, err := s.Decode(expand(toks)); err != nil || !reflect.DeepEqual(got2, seq) {
		return errors.New("selfcheck: RLE expansion mismatch")
	}
	// 不变量 4：三类拒绝互不相同、被拒不留痕、之后仍可正常使用。
	if _, err := New(0); !errors.Is(err, ErrInvalidConfig) {
		return errors.New("selfcheck: config error missing")
	}
	before := len(s.Tokens())
	if err := s.Append(""); !errors.Is(err, ErrInvalidValue) || len(s.Tokens()) != before {
		return errors.New("selfcheck: empty value left a trace")
	}
	bad := [][]enc.Token{
		{enc.Ref(0, 1)},                  // ref 未知 code
		{enc.Put(4, "x")},                // put code 越界 [0,4)
		{enc.Put(0, "x"), enc.Ref(0, 0)}, // ref 的 m<1
	}
	for _, b := range bad {
		if _, err := s.Decode(b); !errors.Is(err, ErrBadToken) {
			return errors.New("selfcheck: bad token not rejected")
		}
	}
	if ErrInvalidConfig == ErrInvalidValue || ErrInvalidValue == ErrBadToken || ErrInvalidConfig == ErrBadToken {
		return errors.New("selfcheck: sentinel errors not distinct")
	}
	if err := s.Append("z"); err != nil {
		return errors.New("selfcheck: coder unusable after rejection")
	}
	return nil
}

func expand(toks []enc.Token) []enc.Token {
	var out []enc.Token
	for _, t := range toks {
		if t.Kind != enc.KindRef {
			out = append(out, t)
			continue
		}
		for i := 0; i < t.Count; i++ {
			out = append(out, enc.Ref(t.Code, 1))
		}
	}
	return out
}
