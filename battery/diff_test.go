package battery_test

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ontology/battery"
)

// TestNaiveDifferential drives both implementations through many random event
// sequences (samples plus reset attempts), comparing every result and every
// snapshot. A trace file with inputs, outputs and reasons is always produced;
// run with -v to see its path.
func TestNaiveDifferential(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "differential.log")
	logf, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logf.Close()
	logf.WriteString("differential trace: inputs, outputs and decision basis\n\n")

	const sequences, length = 60, 80
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(1000 + seq)))
		cfg := testConfig()
		mgr, err := battery.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		mgrLog := func(line string) { lines = append(lines, "    "+line) }
		// Recreate with logger per sequence.
		mgr, _ = battery.New(cfg, battery.WithLogger(mgrLog))
		ref := newNaive(cfg)

		fmt.Fprintf(logf, "=== sequence %d ===\n", seq)
		var tms int64
		for step := 0; step < length; step++ {
			if rng.Intn(8) == 0 {
				permitted := rng.Intn(2) == 0
				fmt.Fprintf(logf, "[%03d] RESET permitted=%v\n", step, permitted)
				g1, e1 := mgr.Reset(permitted)
				g2, e2 := ref.reset(permitted)
				if !sameErr(e1, e2) {
					writeAndFail(t, logf, lines, seq, step, "reset error", e1, e2, logPath)
				}
				if e1 == nil && !snapEqual(g1, g2) {
					writeAndFail(t, logf, lines, seq, step, "reset snapshot", g1, g2, logPath)
				}
				fmt.Fprintf(logf, "    -> err=%v snap=%+v\n", e1, g1)
				continue
			}

			// Occasionally inject illegal shapes/values/time regressions.
			s, reason := randomSample(rng, cfg, tms, step)
			fmt.Fprintf(logf, "[%03d] SUBMIT t=%d V=%v T=%v I=%d (%s)\n",
				step, s.TimeMS, s.CellVoltagesMV, s.Temperatures, s.CurrentMA, reason)
			g1, e1 := mgr.Submit(s)
			g2, e2 := ref.submit(s)
			if !sameErr(e1, e2) {
				writeAndFail(t, logf, lines, seq, step, "submit error", e1, e2, logPath)
			}
			if e1 == nil {
				tms = s.TimeMS
				if !snapEqual(g1, g2) {
					writeAndFail(t, logf, lines, seq, step, "snapshot", g1, g2, logPath)
				}
			}
			fmt.Fprintf(logf, "    -> err=%v snap=%+v\n", e1, g1)
			for _, l := range lines {
				fmt.Fprintln(logf, l)
			}
			lines = lines[:0]
		}
		// Final consistency check via the read-only query.
		if !snapEqual(mgr.CurrentSnapshot(), ref.snapshot(len(ref.accepted)-1)) {
			t.Fatalf("seq %d final CurrentSnapshot mismatch", seq)
		}
	}
	t.Logf("differential trace written to %s", logPath)
}

func randomSample(rng *rand.Rand, cfg battery.Config, lastT int64, step int) (battery.Sample, string) {
	nc, nt := cfg.CellCount, cfg.TempCount
	reason := "normal"

	if rng.Intn(10) == 0 {
		nc++
		reason = "wrong cell count"
	}
	volts := make([]int64, nc)
	mode := rng.Intn(6)
	for i := range volts {
		switch mode {
		case 0: // normal mid band
			volts[i] = int64(3000 + rng.Intn(600))
		case 1: // near over-voltage
			volts[i] = int64(4150 + rng.Intn(100))
		case 2: // near under-voltage
			volts[i] = int64(2750 + rng.Intn(100))
		case 3: // big spread
			if i == 0 {
				volts[i] = int64(3000 + rng.Intn(100))
			} else {
				volts[i] = int64(3500 + rng.Intn(60))
			}
		case 4: // recovery zone for OV
			volts[i] = int64(4050 + rng.Intn(51))
		default:
			volts[i] = int64(2850 + rng.Intn(51))
		}
	}
	if rng.Intn(12) == 0 {
		volts[rng.Intn(len(volts))] = cfg.MaxCellVoltageMV + 1
		reason = "voltage out of range"
	}

	temps := make([]int64, nt)
	for i := range temps {
		if rng.Intn(5) == 0 {
			temps[i] = battery.InvalidTemperature
			continue
		}
		switch rng.Intn(4) {
		case 0:
			temps[i] = int64(100 + rng.Intn(300))
		case 1:
			temps[i] = int64(-400 + rng.Intn(400))
		case 2:
			temps[i] = int64(450 + rng.Intn(350))
		default:
			temps[i] = cfg.MaxTemperature + int64(1+rng.Intn(5)) // treated invalid
		}
	}

	cur := int64(rng.Intn(int(cfg.MaxCurrentMA*2+1))) - cfg.MaxCurrentMA
	switch rng.Intn(6) {
	case 0:
		cur = 0
	case 1:
		cur = cfg.RestCurrentThresholdMA // exactly at rest threshold
	case 2:
		cur = cfg.RestCurrentThresholdMA + 1
	case 3:
		cur = int64(1100 + rng.Intn(700)) // charging overcurrent zone
	case 4:
		cur = -int64(900 + rng.Intn(800)) // discharging overcurrent zone
	}
	if rng.Intn(15) == 0 {
		cur = cfg.MaxCurrentMA + 1
		reason = "current out of range"
	}

	t := lastT + int64(1+rng.Intn(120))
	if rng.Intn(12) == 0 {
		t = lastT - int64(rng.Intn(5))
		reason = "time regression"
	}
	if step == 0 && t < 0 {
		t = 0
	}
	return battery.Sample{TimeMS: t, CellVoltagesMV: volts, Temperatures: temps, CurrentMA: cur}, reason
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	for _, target := range []error{
		battery.ErrInvalidConfig, battery.ErrInvalidSample, battery.ErrTimeNotAdvancing,
		battery.ErrNoPermission, battery.ErrNotLatched, battery.ErrNoSampleSinceLatch,
		battery.ErrRecoveryNotSatisfied,
	} {
		if errors.Is(a, target) {
			return errors.Is(b, target)
		}
	}
	return false
}

func snapEqual(a, b battery.Snapshot) bool {
	return a.TimeMS == b.TimeMS &&
		a.AcceptedSamples == b.AcceptedSamples &&
		a.AllowedChargeMA == b.AllowedChargeMA &&
		a.AllowedDischargeMA == b.AllowedDischargeMA &&
		a.BanCharge == b.BanCharge &&
		a.BanDischarge == b.BanDischarge &&
		a.BalanceRequest == b.BalanceRequest &&
		a.SensorFault == b.SensorFault &&
		a.Latched == b.Latched &&
		a.HasSampleSinceLatch == b.HasSampleSinceLatch &&
		reflect.DeepEqual(a.LatchCauses, b.LatchCauses)
}

func writeAndFail(t *testing.T, f *os.File, lines []string, seq, step int, what string, a, b any, path string) {
	t.Helper()
	fmt.Fprintf(f, "MISMATCH seq=%d step=%d %s\n  got:  %+v\n  want: %+v\n", seq, step, what, a, b)
	for _, l := range lines {
		fmt.Fprintln(f, l)
	}
	f.Sync()
	content, _ := os.ReadFile(path)
	t.Fatalf("mismatch seq=%d step=%d %s:\n  got:  %+v\n  want: %+v\nlog (%d chars): %s",
		seq, step, what, a, b, len(content), strings.TrimSpace(tail(string(content), 2000)))
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
