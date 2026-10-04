package lease

import (
	"errors"
	"testing"

	"ontology/history"
)

func TestNewSetInvalidArgs(t *testing.T) {
	if _, err := NewSet(nil, 0, 1); !errors.Is(err, history.ErrInvalidArgument) {
		t.Fatalf("E=0 want invalid, got %v", err)
	}
	if _, err := NewSet(nil, 1, 0); !errors.Is(err, history.ErrInvalidArgument) {
		t.Fatalf("Lmax=0 want invalid, got %v", err)
	}
	if _, err := NewSet(nil, 1_000_000_001, 1); !errors.Is(err, history.ErrInvalidArgument) {
		t.Fatalf("E too large want invalid, got %v", err)
	}
	if _, err := NewSet(nil, 1, 1001); !errors.Is(err, history.ErrInvalidArgument) {
		t.Fatalf("Lmax too large want invalid, got %v", err)
	}
}

func TestSetGlobalCheckpoint(t *testing.T) {
	h := history.NewHistory()
	if _, err := h.Index(0, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Index(0, []byte("b")); err != nil {
		t.Fatal(err)
	}
	s, err := NewSet(h, 100, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetGlobalCheckpoint(0, 2); err != nil {
		t.Fatalf("g=5 valid, got %v", err)
	}
	if s.GCPlocked() != 2 {
		t.Fatalf("gcp=%d want 2", s.GCPlocked())
	}
	// g > maxSeq: 参数非法（排在时钟回退之前）。
	if err := s.SetGlobalCheckpoint(0, 3); !errors.Is(err, history.ErrInvalidArgument) {
		t.Fatalf("g>maxSeq want invalid, got %v", err)
	}
	// g < gcp: 检查点回退，排在时钟回退之后。
	if err := s.SetGlobalCheckpoint(0, 1); !errors.Is(err, ErrCheckpointBack) {
		t.Fatalf("gcp back want ErrCheckpointBack, got %v", err)
	}
	// 时钟回退优先于检查点回退（当前时钟已是 0；用合法参数触发时钟回退：
	// 需先让时钟前进再回退）。
	if err := s.SetGlobalCheckpoint(0, 2); err != nil {
		t.Fatal(err)
	}
	// now 仍可等于 0；要测回退先前进到 1。
	if err := s.SetGlobalCheckpoint(1, 2); err != nil {
		t.Fatal(err)
	}
	// now=0 回退，同时 g=1<gcp=2 也是回退；时钟回退必须优先。
	if err := s.SetGlobalCheckpoint(0, 1); !errors.Is(err, history.ErrClockBack) {
		t.Fatalf("clock back precedence want ErrClockBack, got %v", err)
	}
	if s.GCPlocked() != 2 {
		t.Fatalf("rejected op must not change gcp, got %d", s.GCPlocked())
	}
}

type leaseStep struct {
	now  int64
	op   string // add/renew/remove/merge/gcp
	name string
	r    int64
	g    int64
}

type leaseCase struct {
	name       string
	setup      []setupOp
	steps      []leaseStep
	wantErr    []error // 与 steps 对齐，nil 表示成功；merge 成功也为 nil
	wantH      int64
	lmax       int
	wantLeases map[string]int64
}

type setupOp struct {
	kind string // index/delete
	id   string
}

func buildEnv(t *testing.T, setup []setupOp, E int64, lmax int) (*history.History, *Set) {
	t.Helper()
	h := history.NewHistory()
	var seq int64
	for _, so := range setup {
		seq++
		switch so.kind {
		case "index":
			if _, err := h.Index(0, []byte(so.id)); err != nil {
				t.Fatalf("setup index: %v", err)
			}
		case "delete":
			if _, err := h.Delete(0, []byte(so.id)); err != nil {
				t.Fatalf("setup delete: %v", err)
			}
		}
	}
	s, err := NewSet(h, E, lmax)
	if err != nil {
		t.Fatalf("new set: %v", err)
	}
	return h, s
}

func TestLeaseTable(t *testing.T) {
	cases := []leaseCase{
		{
			name:  "add basic and r=H",
			setup: []setupOp{{"index", "a"}},
			steps: []leaseStep{
				{0, "add", "L1", 1, 0},
			},
			wantErr:    []error{nil},
			wantH:      1,
			wantLeases: map[string]int64{"L1": 1},
		},
		{
			name:  "r equals H allowed; r below H rejected",
			setup: []setupOp{{"index", "a"}, {"index", "b"}},
			steps: []leaseStep{
				{0, "gcp", "", 0, 1},   // gcp=1 -> 下次 merge floor=2
				{0, "merge", "", 0, 0}, // H -> 2
				{0, "add", "L1", 2, 0}, // r==H==2
				{0, "add", "L2", 1, 0}, // r==H-1 history unavailable
			},
			wantErr:    []error{nil, nil, nil, ErrHistoryUnavail},
			wantH:      2,
			wantLeases: map[string]int64{"L1": 2},
		},
		{
			name: "add duplicate then notfound",
			steps: []leaseStep{
				{0, "add", "L1", 1, 0},
				{0, "add", "L1", 1, 0},
				{0, "renew", "X", 1, 0},
				{0, "remove", "X", 0, 0},
			},
			wantErr: []error{nil, ErrLeaseExists, ErrLeaseNotFound, ErrLeaseNotFound},
		},
		{
			name:  "renew back rejected",
			setup: []setupOp{{"index", "a"}, {"index", "b"}, {"index", "c"}},
			steps: []leaseStep{
				{0, "add", "L1", 3, 0},
				{0, "renew", "L1", 2, 0},
			},
			wantErr:    []error{nil, ErrLeaseBack},
			wantLeases: map[string]int64{"L1": 3},
		},
		{
			name:  "lmax enforced after unavail",
			setup: []setupOp{{"index", "a"}, {"index", "b"}, {"index", "c"}},
			steps: []leaseStep{
				{0, "add", "L1", 1, 0},
				{0, "add", "L2", 1, 0},
				{0, "add", "L3", 1, 0},  // lmax=2
				{0, "add", "L3", 99, 0}, // also over r>maxSeq -> invalid takes precedence
			},
			wantErr:    []error{nil, nil, ErrLeaseLimit, history.ErrInvalidArgument},
			wantLeases: map[string]int64{"L1": 1, "L2": 1},
		},
		{
			name:  "expiry exactly E not expired; E+1 purged",
			setup: []setupOp{{"index", "a"}, {"index", "b"}},
			steps: []leaseStep{
				{0, "add", "L1", 1, 0},
				{100, "merge", "", 0, 0}, // 100-0==100 not expired, stays
				{101, "merge", "", 0, 0}, // 101-0>100 expired, purged
				{101, "renew", "L1", 1, 0},
			},
			wantErr: []error{nil, nil, nil, ErrLeaseNotFound},
		},
		{
			name:  "expired but not purged can still renew",
			setup: []setupOp{{"index", "a"}},
			steps: []leaseStep{
				{0, "add", "L1", 1, 0},
				{101, "renew", "L1", 1, 0},
				{101, "remove", "L1", 0, 0},
			},
			wantErr: []error{nil, nil, nil},
		},
		{
			name:  "rejected add does not purge expired",
			setup: []setupOp{{"index", "a"}},
			steps: []leaseStep{
				{0, "add", "L1", 1, 0},
				{101, "add", "L1", 1, 0},   // duplicate + expired: exists first, no purge
				{101, "renew", "L1", 1, 0}, // still present -> renew ok
			},
			wantErr: []error{nil, ErrLeaseExists, nil},
		},
		{
			name: "gcp determines floor vs lease",
			setup: []setupOp{
				{"index", "a"}, {"index", "b"}, {"index", "c"},
				{"index", "d"}, {"index", "e"},
			},
			steps: []leaseStep{
				{0, "gcp", "", 0, 4},
				{0, "add", "hi", 2, 0}, // lease r=2 wins -> floor 2
				{0, "add", "lo", 5, 0},
				{0, "merge", "", 0, 0}, // H -> 2
				{0, "add", "H2", 2, 0}, // r==H
				{0, "add", "H1", 1, 0}, // r==H-1
			},
			wantErr: []error{nil, nil, nil, nil, nil, ErrHistoryUnavail},
			wantH:   2,
			lmax:    3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lmax := tc.lmax
			if lmax == 0 {
				lmax = 2
			}
			_, s := buildEnv(t, tc.setup, 100, lmax)
			for i, st := range tc.steps {
				var err error
				switch st.op {
				case "add":
					err = s.AddLease(st.now, st.name, st.r)
				case "renew":
					err = s.RenewLease(st.now, st.name, st.r)
				case "remove":
					err = s.RemoveLease(st.now, st.name)
				case "gcp":
					err = s.SetGlobalCheckpoint(st.now, st.g)
				case "merge":
					_, err = s.Merge(st.now)
				}
				want := tc.wantErr[i]
				if want == nil && err != nil {
					t.Fatalf("step %d (%s) want ok, got %v", i, st.op, err)
				}
				if want != nil && !errors.Is(err, want) {
					t.Fatalf("step %d (%s) want %v, got %v", i, st.op, want, err)
				}
			}
			s.hist.Lock()
			if got := s.hist.HLocked(); got != tc.wantH && tc.wantH != 0 {
				t.Errorf("H=%d want %d", got, tc.wantH)
			}
			s.hist.Unlock()
			for name, r := range tc.wantLeases {
				l, ok := s.ls[name]
				if !ok || l.r != r {
					t.Errorf("lease %s want r=%d, got %+v", name, r, l)
				}
			}
		})
	}
}
