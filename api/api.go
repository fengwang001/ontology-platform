// Package api 对外门面：持有 schema，提供打包/解包/取字段与自检。
package api

import (
	"bytes"
	"fmt"
	"maps"
	"math/rand/v2"

	"ontology/layout"
	"ontology/pack"
)

// API 固定 schema 的打包器，构建后可并发使用。
type API struct {
	schema *layout.Schema
}

// New 以给定 schema 构造 API。
func New(s *layout.Schema) *API { return &API{schema: s} }

// Marshal 打包字段集合为定长字节串。
func (a *API) Marshal(fields map[string]int64) ([]byte, error) {
	return pack.Pack(a.schema, fields)
}

// Unmarshal 解包字节串为字段集合。
func (a *API) Unmarshal(buf []byte) (map[string]int64, error) {
	return pack.Unpack(a.schema, buf)
}

// GetField 只解出 buf 中指定字段的值。
func (a *API) GetField(buf []byte, name string) (int64, error) {
	f, ok := a.schema.Field(name)
	if !ok {
		return 0, fmt.Errorf("%w: %q", pack.ErrUnknownField, name)
	}
	if len(buf) < a.schema.Total() {
		return 0, fmt.Errorf("%w: have %d, need %d", pack.ErrShortBuffer, len(buf), a.schema.Total())
	}
	var u uint64
	for i := 0; i < f.Width; i++ {
		u |= uint64(buf[f.Offset+i]) << (8 * i)
	}
	if f.Signed && f.Width < 8 && u&(1<<(uint(8*f.Width)-1)) != 0 {
		u |= ^uint64(0) << uint(8*f.Width)
	}
	return int64(u), nil
}

// SelfCheck 用内置向量核验四条不变量：往返一致、偏移自洽、
// 与朴素参照字节级一致、失败不留痕。全部通过返回 nil。
func (a *API) SelfCheck() error {
	s := a.schema
	rng := rand.New(rand.NewPCG(1, 2))
	for trial := 0; trial < 64; trial++ { // 不变量 1 与 3
		in := map[string]int64{}
		for _, f := range s.Fields() {
			in[f.Name] = randInRange(rng, f)
		}
		buf, err := pack.Pack(s, in)
		if err != nil {
			return fmt.Errorf("selfcheck pack: %w", err)
		}
		if want := naive(s, in); !bytes.Equal(buf, want) {
			return fmt.Errorf("selfcheck naive: got %x want %x", buf, want)
		}
		out, err := pack.Unpack(s, buf)
		if err != nil {
			return fmt.Errorf("selfcheck unpack: %w", err)
		}
		if !maps.Equal(in, out) {
			return fmt.Errorf("selfcheck roundtrip: got %v want %v", out, in)
		}
	}
	off := 0 // 不变量 2：偏移=前缀和，紧密无重叠
	for _, f := range s.Fields() {
		if f.Offset != off {
			return fmt.Errorf("selfcheck offsets: %q at %d, want %d", f.Name, f.Offset, off)
		}
		off += f.Width
	}
	if off != s.Total() {
		return fmt.Errorf("selfcheck offsets: total %d, want %d", s.Total(), off)
	}
	return checkFailures(s) // 不变量 4
}

func randInRange(rng *rand.Rand, f layout.Field) int64 {
	bits := uint(8 * f.Width)
	if f.Signed {
		return int64(rng.Uint64()<<(64-bits)) >> (64 - bits) // 截断到值域并符号扩展
	}
	if bits >= 64 {
		return int64(rng.Uint64() >> 1)
	}
	return int64(rng.Uint64() & (1<<bits - 1))
}

// naive 教科书参照：按偏移逐字节写小端。
func naive(s *layout.Schema, fields map[string]int64) []byte {
	buf := make([]byte, s.Total())
	for _, f := range s.Fields() {
		u := uint64(fields[f.Name])
		for i := 0; i < f.Width; i++ {
			buf[f.Offset+i] = byte(u >> (8 * i))
		}
	}
	return buf
}

func checkFailures(s *layout.Schema) error {
	good := map[string]int64{}
	for _, f := range s.Fields() {
		good[f.Name] = 0
	}
	bad := maps.Clone(good)
	bad["no-such-field"] = 1
	if b, err := pack.Pack(s, bad); err == nil || b != nil {
		return fmt.Errorf("selfcheck failures: unknown field not rejected atomically")
	}
	over := maps.Clone(good)
	f0 := s.Fields()[0]
	over[f0.Name] = int64(1) << uint(8*f0.Width) // 必越界
	if b, err := pack.Pack(s, over); err == nil || b != nil {
		return fmt.Errorf("selfcheck failures: overflow not rejected atomically")
	}
	if m, err := pack.Unpack(s, make([]byte, s.Total()-1)); err == nil || m != nil {
		return fmt.Errorf("selfcheck failures: short buffer not rejected atomically")
	}
	if _, err := pack.Pack(s, good); err != nil { // 失败之后仍可正常使用
		return fmt.Errorf("selfcheck failures: unusable after rejection: %w", err)
	}
	return nil
}
