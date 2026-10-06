package battery_test

import (
	"errors"
	"testing"

	"ontology/battery"
)

func TestSampleRejectionLeavesNoTrace(t *testing.T) {
	m, _ := battery.New(testConfig())

	// Count mismatch takes precedence over an out-of-range value and time.
	badCount := mkSample(0, []int64{3300, 3300, 3300}, []int64{250, 250}, 0)
	if _, err := m.Submit(badCount); !errors.Is(err, battery.ErrInvalidSample) {
		t.Fatalf("want ErrInvalidSample, got %v", err)
	}

	// Value out of range takes precedence over time ordering.
	good := baseSample(100, 0)
	m.Submit(good)
	ovr := mkSample(50, []int64{99999, 3300, 3300, 3300}, []int64{250, 250}, 0)
	if _, err := m.Submit(ovr); !errors.Is(err, battery.ErrInvalidSample) {
		t.Fatalf("range error must outrank time error, got %v", err)
	}
	badCur := mkSample(50, []int64{3300, 3300, 3300, 3300}, []int64{250, 250}, 5000)
	if _, err := m.Submit(badCur); !errors.Is(err, battery.ErrInvalidSample) {
		t.Fatalf("current range error expected, got %v", err)
	}

	// Same timestamp rejected; rejected samples must not advance time.
	if _, err := m.Submit(baseSample(100, 0)); !errors.Is(err, battery.ErrTimeNotAdvancing) {
		t.Fatalf("want ErrTimeNotAdvancing, got %v", err)
	}
	if _, err := m.Submit(baseSample(99, 0)); !errors.Is(err, battery.ErrTimeNotAdvancing) {
		t.Fatalf("want ErrTimeNotAdvancing, got %v", err)
	}
	s, err := m.Submit(baseSample(101, 0))
	if err != nil {
		t.Fatalf("t=101 must still be accepted, got %v", err)
	}
	// Exactly one accepted sample so far: the t=100 one.
	if s.AcceptedSamples != 2 {
		t.Fatalf("rejected samples must leave no trace, accepted=%d", s.AcceptedSamples)
	}
	if s.TimeMS != 101 {
		t.Fatalf("time must not advance on rejection, got %d", s.TimeMS)
	}
}

func TestLoggerReceivesTraces(t *testing.T) {
	var lines []string
	m, _ := battery.New(testConfig(), battery.WithLogger(func(line string) { lines = append(lines, line) }))
	m.Submit(baseSample(0, 0))
	m.Submit(baseSample(0, 0)) // rejected
	if len(lines) != 2 {
		t.Fatalf("want one ACCEPT and one REJECT log line, got %v", lines)
	}
}
