package norm

import "testing"

func i64(v int64) *int64 { return &v }

func TestCvtDRounding(t *testing.T) {
	// 例三：sd=2, D=10^4
	cases := []struct {
		rm      int
		m, want int64
	}{
		// 135000: q=13 r=5000；125000: q=12 r=5000
		{RmHalfAway, 135000, 14}, {RmHalfAway, 125000, 13},
		{RmHalfEven, 135000, 14}, {RmHalfEven, 125000, 12},
		{RmTruncate, 135000, 13}, {RmTruncate, 125000, 12},
		// 负数恰半
		{RmHalfAway, -135000, -14}, {RmHalfEven, -135000, -14}, {RmTruncate, -135000, -13},
		// 例一：125000 半入 13；-125000 半入 -13、半偶 -12
		{RmHalfAway, 125000, 13}, {RmHalfAway, -125000, -13},
		{RmHalfEven, -125000, -12},
		// 5000: q=0 r=5000，半入 1；非恰半 4999 得 0
		{RmHalfAway, 5000, 1}, {RmHalfEven, 5000, 0}, {RmTruncate, 5000, 0},
		{RmHalfAway, 4999, 0}, {RmHalfAway, 15001, 2}, {RmHalfEven, 15001, 2},
		// sd=6 恒等
	}
	for _, tc := range cases {
		cfg, err := NewCfg(2, tc.rm, false)
		if err != nil {
			t.Fatalf("NewCfg: %v", err)
		}
		if got := cfg.CvtD(tc.m); got != tc.want {
			t.Errorf("rm=%d CvtD(%d) = %d, want %d", tc.rm, tc.m, got, tc.want)
		}
	}
}

func TestCvtDScale6Identity(t *testing.T) {
	for _, rm := range []int{RmHalfAway, RmHalfEven, RmTruncate} {
		cfg, _ := NewCfg(6, rm, false)
		for _, m := range []int64{0, 1, -1, MaxMant, -MaxMant, 125000, -135000} {
			if got := cfg.CvtD(m); got != m {
				t.Errorf("sd=6 rm=%d CvtD(%d) = %d, want identity", rm, m, got)
			}
		}
	}
}

func TestCvtDScales(t *testing.T) {
	// sd=0: D=10^6
	cfg, _ := NewCfg(0, RmHalfAway, false)
	if got := cfg.CvtD(1_500_000); got != 2 {
		t.Errorf("CvtD(1500000) sd=0 = %d, want 2", got)
	}
	if got := cfg.CvtD(-1_400_000); got != -1 {
		t.Errorf("CvtD(-1400000) sd=0 = %d, want -1", got)
	}
	// 半偶：2_500_000 q=2 偶 => 2；3_500_000 q=3 奇 => 4
	evencfg, _ := NewCfg(0, RmHalfEven, false)
	if got := evencfg.CvtD(2_500_000); got != 2 {
		t.Errorf("half-even CvtD(2500000) = %d, want 2", got)
	}
	if got := evencfg.CvtD(3_500_000); got != 4 {
		t.Errorf("half-even CvtD(3500000) = %d, want 4", got)
	}
}

func TestNormC(t *testing.T) {
	cfg, _ := NewCfg(2, RmHalfAway, false)
	if got := cfg.NormC([]byte("ab  ")); string(got) != "ab" {
		t.Errorf("NormC(ab  ) = %q", got)
	}
	// 只去右侧空格：左侧与制表符保留
	if got := cfg.NormC([]byte(" ab ")); string(got) != " ab" {
		t.Errorf("NormC(' ab ') = %q, want ' ab'", got)
	}
	if got := cfg.NormC([]byte("\tab\t  ")); string(got) != "\tab\t" {
		t.Errorf("NormC tab = %q", got)
	}
	if got := cfg.NormC([]byte("")); got == nil || len(got) != 0 {
		t.Errorf("ne=false empty -> %v, want empty non-nil", got)
	}
	if got := cfg.NormC(nil); got != nil {
		t.Errorf("nil -> %v, want nil", got)
	}

	necfg, _ := NewCfg(2, RmHalfAway, true)
	if got := necfg.NormC([]byte("   ")); got != nil {
		t.Errorf("ne=true spaces -> %v, want nil(NULL)", got)
	}
	if got := necfg.NormC([]byte("")); got != nil {
		t.Errorf("ne=true empty -> %v, want nil(NULL)", got)
	}
	if got := necfg.NormC(nil); got != nil {
		t.Errorf("ne=true nil -> %v, want nil", got)
	}
	if got := necfg.NormC([]byte(" x ")); string(got) != " x" {
		t.Errorf("ne=true ' x ' -> %q", got)
	}
}

func TestInvalidCfgAndRow(t *testing.T) {
	for _, sd := range []int{-1, 7} {
		if _, err := NewCfg(sd, RmHalfAway, false); err != ErrInvalid {
			t.Errorf("sd=%d want ErrInvalid", sd)
		}
	}
	for _, rm := range []int{-1, 3} {
		if _, err := NewCfg(2, rm, false); err != ErrInvalid {
			t.Errorf("rm=%d want ErrInvalid", rm)
		}
	}
	if err := CheckRow(Row{ID: 0, D: i64(1)}); err != ErrInvalid {
		t.Errorf("id=0 want ErrInvalid")
	}
	if err := CheckRow(Row{ID: MaxID + 1, D: i64(1)}); err != ErrInvalid {
		t.Errorf("id overflow want ErrInvalid")
	}
	if err := CheckRow(Row{ID: 1, D: i64(MaxMant + 1)}); err != ErrInvalid {
		t.Errorf("mantissa overflow want ErrInvalid")
	}
	if err := CheckRow(Row{ID: 1, D: i64(-MaxMant - 1)}); err != ErrInvalid {
		t.Errorf("mantissa underflow want ErrInvalid")
	}
	big := make([]byte, MaxBytes+1)
	if err := CheckRow(Row{ID: 1, C: big}); err != ErrInvalid {
		t.Errorf("c too long want ErrInvalid")
	}
	if err := CheckRow(Row{ID: MaxID, D: nil, C: nil}); err != nil {
		t.Errorf("boundary valid row: %v", err)
	}
	if err := CheckRow(Row{ID: 1, D: i64(MaxMant), C: make([]byte, MaxBytes)}); err != nil {
		t.Errorf("max values valid: %v", err)
	}
}

func TestCvtDNil(t *testing.T) {
	cfg, _ := NewCfg(2, RmHalfAway, false)
	if cfg.CvtDNil(nil) != nil {
		t.Errorf("NULL stays NULL")
	}
	if got := cfg.CvtDNil(i64(125000)); *got != 13 {
		t.Errorf("CvtDNil = %d, want 13", *got)
	}
}
