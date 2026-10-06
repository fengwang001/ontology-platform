package layerconfig_test

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"ontology/layerconfig"
)

// opLogger records every operation with its inputs, actual output and the
// verdict basis. When LC_TEST_VERBOSE=1 (or -v runs), entries also stream to
// stdout so a failure can be replayed step by step.
type opLogger struct {
	t      *testing.T
	sb     strings.Builder
	step   int
	stdout bool
}

func newLogger(t *testing.T) *opLogger {
	t.Helper()
	l := &opLogger{t: t, stdout: os.Getenv("LC_TEST_VERBOSE") == "1"}
	l.line("=== test %s start ===", t.Name())
	return l
}

func (l *opLogger) line(format string, args ...any) {
	fmt.Fprintf(&l.sb, format+"\n", args...)
	if l.stdout {
		fmt.Printf(format+"\n", args...)
	}
}

func scopeString(s layerconfig.Scope) string {
	return fmt.Sprintf("{env:%q region:%q instance:%q}", s.Env, s.Region, s.Instance)
}

func opString(op layerconfig.Op) string {
	switch op {
	case layerconfig.OpWrite:
		return "WRITE"
	case layerconfig.OpCancel:
		return "CANCEL"
	case layerconfig.OpClear:
		return "CLEAR"
	case layerconfig.OpLock:
		return "LOCK"
	case layerconfig.OpUnlock:
		return "UNLOCK"
	default:
		return fmt.Sprintf("OP(%d)", int(op))
	}
}

func valueString(v layerconfig.Value) string {
	return fmt.Sprintf("{str:%q int:%d bool:%t list:%#v}", v.Str, v.Int, v.Bool, v.List)
}

func resolvedString(r layerconfig.Resolved) string {
	if !r.Present {
		return "UNSET"
	}
	return "VALUE(" + valueString(r.Value) + ")"
}

// logPublish records a publish input, output and the decision basis.
func (l *opLogger) logPublish(changes []layerconfig.Change, gotVersion int, gotErr error, basis string) {
	l.step++
	l.line("[step %d] PUBLISH inputs:", l.step)
	for i, c := range changes {
		l.line("          change[%d]=%s key=%q scope=%s value=%s",
			i, opString(c.Op), c.Key, scopeString(c.Scope), valueString(c.Value))
	}
	l.line("          actual -> version=%d err=%v", gotVersion, errName(gotErr))
	l.line("          basis  : %s", basis)
}

// logResolve records a resolve input, output and the decision basis.
func (l *opLogger) logResolve(version int, target layerconfig.Scope, key string, got layerconfig.Resolved, gotErr error, basis string) {
	l.step++
	l.line("[step %d] RESOLVE version=%d target=%s key=%q", l.step, version, scopeString(target), key)
	l.line("          actual -> %s err=%v", resolvedString(got), errName(gotErr))
	l.line("          basis  : %s", basis)
}

func (l *opLogger) logRollback(target, gotVersion int, gotErr error, basis string) {
	l.step++
	l.line("[step %d] ROLLBACK target=%d actual -> version=%d err=%v", l.step, target, gotVersion, errName(gotErr))
	l.line("          basis  : %s", basis)
}

func (l *opLogger) logRegister(sc layerconfig.Schema, gotErr error, basis string) {
	l.step++
	l.line("[step %d] REGISTER key=%q type=%d required=%t range=[%d,%d] merge=%d err=%v",
		l.step, sc.Key, sc.Type, sc.Required, sc.Min, sc.Max, sc.Merge, errName(gotErr))
	l.line("          basis  : %s", basis)
}

func errName(err error) string {
	if err == nil {
		return "<nil>"
	}
	if e, ok := layerconfig.AsError(err); ok {
		return e.Kind.String()
	}
	return err.Error()
}

// finish dumps the full log on failure and always returns it via the test
// output when -v is on.
func (l *opLogger) finish() {
	l.line("=== test %s end ===", l.t.Name())
	if l.t.Failed() {
		l.t.Log("\n" + l.sb.String())
	} else if l.stdout {
		fmt.Print(l.sb.String())
	}
}

var _ io.Writer = (*strings.Builder)(nil)
