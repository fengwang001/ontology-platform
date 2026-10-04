package stock_test

import (
	"errors"
	"testing"

	"ontology/stock"
)

func TestAddItem(t *testing.T) {
	cases := []struct {
		name                              string
		id                                []byte
		onHand, ss, lead, lotMin, lotMult uint64
		setup                             func(s *stock.Stock)
		want                              error
	}{
		{"合法登记", []byte("A"), 5, 0, 1, 1, 1, nil, nil},
		{"边界值全取上限", []byte("B"), 1_000_000_000, 1_000_000_000, 52, 1_000_000, 1_000_000, nil, nil},
		{"空物料号", []byte(""), 0, 0, 0, 1, 1, nil, stock.ErrInvalid},
		{"物料号超32字节", []byte("123456789012345678901234567890123"), 0, 0, 0, 1, 1, nil, stock.ErrInvalid},
		{"onHand超上限", []byte("C"), 1_000_000_001, 0, 0, 1, 1, nil, stock.ErrInvalid},
		{"ss超上限", []byte("D"), 0, 1_000_000_001, 0, 1, 1, nil, stock.ErrInvalid},
		{"lead超上限", []byte("E"), 0, 0, 53, 1, 1, nil, stock.ErrInvalid},
		{"lotMin为0", []byte("F"), 0, 0, 0, 0, 1, nil, stock.ErrInvalid},
		{"lotMin超上限", []byte("G"), 0, 0, 0, 1_000_001, 1, nil, stock.ErrInvalid},
		{"lotMult为0", []byte("H"), 0, 0, 0, 1, 0, nil, stock.ErrInvalid},
		{"lotMult超上限", []byte("I"), 0, 0, 0, 1, 1_000_001, nil, stock.ErrInvalid},
		{"重复登记冲突", []byte("J"), 0, 0, 0, 1, 1,
			func(s *stock.Stock) { _ = s.AddItem([]byte("J"), 1, 1, 1, 1, 1) }, stock.ErrConflict},
		{"非法且重复时报参数非法", []byte("K"), 0, 0, 0, 0, 1,
			func(s *stock.Stock) { _ = s.AddItem([]byte("K"), 1, 1, 1, 1, 1) }, stock.ErrInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := stock.New()
			if tc.setup != nil {
				tc.setup(s)
			}
			before := s.Snapshot()
			err := s.AddItem(tc.id, tc.onHand, tc.ss, tc.lead, tc.lotMin, tc.lotMult)
			if !errors.Is(err, tc.want) {
				t.Fatalf("AddItem 错误 = %v, 期望 %v", err, tc.want)
			}
			if err != nil {
				after := s.Snapshot()
				if len(after) != len(before) {
					t.Fatalf("被拒绝的 AddItem 改变了状态")
				}
				for k, v := range before {
					if after[k] != v {
						t.Fatalf("被拒绝的 AddItem 改变了物料 %q", k)
					}
				}
			}
		})
	}
}

func TestAddItemRejectedKeepsState(t *testing.T) {
	s := stock.New()
	if err := s.AddItem([]byte("A"), 1, 2, 3, 4, 5); err != nil {
		t.Fatal(err)
	}
	before := s.Snapshot()
	if err := s.AddItem([]byte("A"), 9, 9, 9, 9, 9); !errors.Is(err, stock.ErrConflict) {
		t.Fatalf("期望冲突, 得到 %v", err)
	}
	after := s.Snapshot()
	if len(after) != 1 || after["A"] != before["A"] {
		t.Fatalf("冲突的 AddItem 改变了已有物料: %+v -> %+v", before, after)
	}
}
