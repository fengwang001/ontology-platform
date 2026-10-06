package surge

import "testing"

func TestSkeletonCompiles(t *testing.T) {
	cfg := testCfg()
	s, err := NewSystem(cfg)
	if err != nil || s == nil {
		t.Fatalf("skeleton system: %v %v", s, err)
	}
	n, err := NewNaiveModel(cfg)
	if err != nil || n == nil {
		t.Fatalf("skeleton naive: %v %v", n, err)
	}
}

func testCfg() Config {
	return Config{
		Thresholds:        []float64{1.0, 2.0, 4.0},
		DownConfirmations: 2,
		MaxHeld:           2,
		Subsidies:         []int64{0, 10, 20, 40},
		MinEvalInterval:   5,
	}
}
