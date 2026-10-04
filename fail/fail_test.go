package fail

import "testing"

func TestFine(t *testing.T) {
	cases := []struct {
		name          string
		remain, price int64
		rate, want    int64
	}{
		{"example seller 600x11x10/1e4 ceil6.6", 600, 11, 10, 7},
		{"example buyer 400x11x5/1e4 ceil2.2", 400, 11, 5, 3},
		{"B seller 400x11x10 ceil4.4", 400, 11, 10, 5},
		{"exact integer no padding", 1000, 10, 100, 100}, // 1000*10*100/1e4=100
		{"rate zero", 600, 11, 0, 0},
		{"remain zero", 0, 11, 10, 0},
		{"ceil just over integer", 1, 1, 1, 1}, // ceil(1/1e4)=1
		{"below one still rounds up to one", 1, 10000, 1, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Fine(c.remain, c.price, c.rate); got != c.want {
				t.Fatalf("Fine(%d,%d,%d)=%d want %d", c.remain, c.price, c.rate, got, c.want)
			}
		})
	}
}

func TestAttribute(t *testing.T) {
	cases := []struct {
		name          string
		a, b, r       int64
		seller, buyer bool
	}{
		{"both deliver fully", 1000, 1000, 1000, false, false},
		{"seller short only", 400, 1000, 1000, true, false},
		{"buyer short only", 600, 200, 600, false, true},
		{"both short", 400, 200, 600, true, true},
		{"zero delivery both liable", 0, 0, 600, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Attribute(c.a, c.b, c.r)
			if got.Seller != c.seller || got.Buyer != c.buyer {
				t.Fatalf("Attribute(%d,%d,%d)=%+v want seller=%v buyer=%v",
					c.a, c.b, c.r, got, c.seller, c.buyer)
			}
		})
	}
}

func TestBuyInComp(t *testing.T) {
	cases := []struct {
		name                  string
		remain, price, amount int64
		paid, want            int64
	}{
		{"i1 example", 600, 11, 10005, 4002, 597},
		{"i2 example", 400, 11, 6100, 2033, 333},
		{"zero comp when market <= unpaid", 100, 5, 1000, 400, 0},
		{"exact equality is zero", 100, 6, 1000, 400, 0}, // 100*6==600
		{"nothing paid yet", 1000, 100, 50000, 0, 50000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := BuyInComp(c.remain, c.price, c.amount, c.paid); got != c.want {
				t.Fatalf("BuyInComp=%d want %d", got, c.want)
			}
		})
	}
}
