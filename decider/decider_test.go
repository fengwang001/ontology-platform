package decider_test

import (
	"testing"

	"ontology/decider"
)

// fakeView 是可控的最小视图，便于逐规则构造边界。
type fakeView struct {
	excluded bool
	has      bool
	zc, z    int
	used     int64
	total    int64
}

func (f fakeView) Excluded(string) bool                  { return f.excluded }
func (f fakeView) HasShardCopy(string, string, int) bool { return f.has }
func (f fakeView) ZoneCopyCount(string, string, int) int { return f.zc }
func (f fakeView) ZoneCount() int                        { return f.z }
func (f fakeView) Used(string) int64                     { return f.used }
func (f fakeView) Total(string) int64                    { return f.total }
func (f fakeView) ZoneOf(string) string                  { return "z1" }

func TestEvaluate(t *testing.T) {
	base := func() decider.Params {
		return decider.Params{Index: "x", Shard: 0, Copies: 3, Size: 30,
			Primary: false, L: 80, H: 90}
	}
	tests := []struct {
		name string
		v    fakeView
		mut  func(*decider.Params)
		want decider.Reason
	}{
		{"D1 excluded first", fakeView{excluded: true, has: true, zc: 9, z: 1,
			used: 999, total: 1}, nil, decider.D1Excluded},
		{"D2 before D3/D4", fakeView{has: true, zc: 9, z: 1, used: 999, total: 1},
			nil, decider.D2SameShard},
		{"D3 ceil(3/2)=2 zone has2", fakeView{zc: 2, z: 2, used: 0, total: 100},
			nil, decider.D3ZoneAwareness},
		{"D3 at limit passes", fakeView{zc: 1, z: 2, used: 0, total: 100},
			nil, decider.Pass},
		{"D3 ceil rounding c=1 z=3", fakeView{zc: 1, z: 3, used: 0, total: 100},
			func(p *decider.Params) { p.Copies = 1 }, decider.D3ZoneAwareness},
		{"D4 replica low water 81>80", fakeView{zc: 0, z: 9, used: 51, total: 100},
			nil, decider.D4Disk},
		{"D4 replica exact 80 passes", fakeView{zc: 0, z: 9, used: 50, total: 100},
			nil, decider.Pass},
		{"D4 primary uses H: 85 passes", fakeView{zc: 0, z: 9, used: 55, total: 100},
			func(p *decider.Params) { p.Primary = true }, decider.Pass},
		{"D4 primary exact 90 passes", fakeView{zc: 0, z: 9, used: 60, total: 100},
			func(p *decider.Params) { p.Primary = true }, decider.Pass},
		{"D4 primary 91 rejected", fakeView{zc: 0, z: 9, used: 61, total: 100},
			func(p *decider.Params) { p.Primary = true }, decider.D4Disk},
		{"D4 relocation forces low for primary", fakeView{zc: 0, z: 9, used: 55, total: 100},
			func(p *decider.Params) { p.Primary = true; p.LowWater = true }, decider.D4Disk},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := base()
			if tc.mut != nil {
				tc.mut(&p)
			}
			if got := decider.Evaluate(tc.v, "n1", p); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}
