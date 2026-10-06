package archive

import (
	"fmt"
	"io"
	"os"
	"testing"
)

// stepLogger 同时写入测试日志与 testdata 下的文件，
// 记录每步输入、输出与判定依据。
type stepLogger struct {
	t *testing.T
	f *os.File
}

func newStepLogger(t *testing.T) *stepLogger {
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create("testdata/trace." + t.Name() + ".log")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return &stepLogger{t: t, f: f}
}

func (l *stepLogger) log(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	l.t.Log(msg)
	if l.f != nil {
		_, _ = io.WriteString(l.f, msg+"\n")
	}
}

func (l *stepLogger) result(tag string, o Outcome) {
	l.log("%-30s -> ok=%-5v err=%-18s reason=%q", tag, o.OK, o.Err.Error(), o.Reason)
}

func testConfig() Config {
	return Config{
		LoanDays:           [4]int{10, 10, 10, 10},
		PickupDeadlineDays: 3,
		RenewWindowDays:    5,
		MaxRenewals:        1,
		OverdueThreshold:   5,
		CooldownDays:       3,
	}
}

func newServiceWith(t *testing.T) *Service {
	s := NewService(testConfig())
	s.AddUser("u0", ClassTopSecret)
	s.AddUser("u1", ClassTopSecret)
	s.AddUser("u2", ClassInternal)
	s.AddVolume("v0", ClassPublic)
	s.AddVolume("v1", ClassInternal)
	s.AddVolume("v2", ClassConfidential)
	s.AddVolume("v3", ClassTopSecret)
	return s
}
