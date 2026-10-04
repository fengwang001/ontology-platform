package slot

import (
	"errors"
	"testing"
)

func constHash(v uint32) HashFunc {
	return func([]byte) uint32 { return v }
}

func TestSlotAndShard(t *testing.T) {
	cases := []struct {
		name        string
		n, r, p     int
		h           HashFunc
		id, routing []byte
		wantSlot    int
		wantShard   int
	}{
		// 题面示例：h(routing)=13, N=2,R=8,P=1 => slot=5 shard=1
		{"example", 2, 8, 1, constHash(13), []byte("d"), []byte("rt"), 5, 1},
		// routing 为空以 id 充当：所有哈希=13 => slot=5
		{"id-as-routing", 2, 8, 1, constHash(13), []byte("d"), nil, 5, 1},
		// N=4,R=8,P=3，h(routing)=6,h(x)=5：offset=2, slot=0, shard=0
		{"p3-x", 4, 8, 3, func(b []byte) uint32 {
			if string(b) == "x" {
				return 5
			}
			return 6
		}, []byte("x"), []byte("rt"), 0, 0},
		// 同例 h(y)=3：offset=0, slot=6, shard=3
		{"p3-y", 4, 8, 3, func(b []byte) uint32 {
			if string(b) == "y" {
				return 3
			}
			return 6
		}, []byte("y"), []byte("rt"), 6, 3},
		// h 接近 2^32：h(routing)=4294967295, h(id)=4294967294, P=3
		// offset=(2^32-2)%3=0；sum=2^32-1（uint64 不回绕）% R
		{"no-uint32-wrap", 2, 8, 3, constHash(0xFFFFFFFF), []byte("d"), []byte("rt"),
			int((uint64(0xFFFFFFFF) + uint64(0xFFFFFFFF)%3) % 8),
			int((uint64(0xFFFFFFFF)+uint64(0xFFFFFFFF)%3)%8) / 4},
		// h(routing) 接近 2^32，offset=2 使和超过 2^32：必须在 uint64 中计算
		{"no-uint32-wrap-sum", 2, 1000000, 3, func(b []byte) uint32 {
			if string(b) == "id" {
				return 0xFFFFFFFE // (2^32-2) % 3 = 2
			}
			return 0xFFFFFFFF
		}, []byte("id"), []byte("rt"),
			int((uint64(0xFFFFFFFF) + 2) % 1000000),
			int((uint64(0xFFFFFFFF)+2)%1000000) / 500000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pa := Params{N: c.n, R: c.r, P: c.p, H: c.h}
			if got := pa.Slot(c.id, c.routing); got != c.wantSlot {
				t.Fatalf("slot=%d want %d", got, c.wantSlot)
			}
			if got := pa.Shard(c.id, c.routing); got != c.wantShard {
				t.Fatalf("shard=%d want %d", got, c.wantShard)
			}
		})
	}
}

func TestSearchShards(t *testing.T) {
	cases := []struct {
		name    string
		n, r, p int
		h       HashFunc
		routing []byte
		want    []int
		wantErr error
	}{
		// 题面：N=4,R=8,P=3,h=6 => 槽 6,7,0 => 分片 3,3,0 => [0,3]
		{"wrap", 4, 8, 3, constHash(6), []byte("rt"), []int{0, 3}, nil},
		{"p1-single", 2, 8, 1, constHash(13), []byte("rt"), []int{1}, nil},
		{"no-wrap-distinct", 4, 16, 3, constHash(0), []byte("rt"), []int{0}, nil},
		{"empty-routing", 4, 8, 3, constHash(6), nil, nil, ErrMissingRouting},
		{"empty-routing-p1", 2, 8, 1, constHash(6), []byte{}, nil, ErrMissingRouting},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pa := Params{N: c.n, R: c.r, P: c.p, H: c.h}
			got, err := SearchShards(pa, c.routing)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err=%v want %v", err, c.wantErr)
			}
			if c.wantErr != nil {
				return
			}
			if len(got) != len(c.want) {
				t.Fatalf("got=%v want=%v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got=%v want=%v", got, c.want)
				}
			}
		})
	}
}

func TestEffectiveRouting(t *testing.T) {
	if string(EffectiveRouting([]byte("id"), nil)) != "id" {
		t.Fatal("empty routing should fall back to id")
	}
	if string(EffectiveRouting([]byte("id"), []byte("rt"))) != "rt" {
		t.Fatal("explicit routing should win")
	}
}
