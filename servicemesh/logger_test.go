package servicemesh

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// opLogger 在测试中打印每次操作的输入、实际输出与判定依据。
// 设置环境变量 SM_LOG=0 可关闭。
type opLogger struct {
	t       *testing.T
	enabled bool
	step    int
}

func newLogger(t *testing.T) *opLogger {
	return &opLogger{t: t, enabled: os.Getenv("SM_LOG") != "0"}
}

func (l *opLogger) log(format string, args ...any) {
	l.step++
	msg := fmt.Sprintf(format, args...)
	l.t.Logf("[step %03d] %s", l.step, msg)
}

func (l *opLogger) publish(service string, cfg *ServiceConfig, base uint64, gotVer uint64, gotErr *RouteError, wantErr ErrClass) {
	rules, fallbacks := 0, 0
	if cfg != nil {
		rules, fallbacks = len(cfg.Rules), len(cfg.Fallbacks)
	}
	l.log("PUBLISH service=%q base=%d rules=%d fallbacks=%d => version=%d err=%v | 判定: 期望错误类别=%d",
		service, base, rules, fallbacks, gotVer, errText(gotErr), wantErr)
}

func (l *opLogger) route(service string, req Request, res *RouteResult, gotErr *RouteError, basis string) {
	l.log("ROUTE service=%q path=%q headers=%v bucket=%d => subset=%q rule=%d endpoint=%q policy=%s err=%v | 判定依据: %s",
		service, req.Path, req.HeaderValues, req.Bucket, resSubset(res), resRule(res), resEndpoint(res),
		policyText(res), errText(gotErr), basis)
}

func errText(e *RouteError) any {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("{class=%d kind=%d rule=%d msg=%q}", e.Class, e.Kind, e.RuleIdx, e.Msg)
}

func resSubset(r *RouteResult) string {
	if r == nil {
		return ""
	}
	return r.Subset
}

func resRule(r *RouteResult) int {
	if r == nil {
		return -2
	}
	return r.RuleIdx
}

func resEndpoint(r *RouteResult) string {
	if r == nil {
		return ""
	}
	return r.Endpoint
}

func policyText(r *RouteResult) string {
	if r == nil {
		return "-"
	}
	d := func(p *time.Duration) string {
		if p == nil {
			return "∅"
		}
		return p.String()
	}
	ret := "∅"
	if r.Policy.MaxRetries != nil {
		ret = fmt.Sprintf("%d", *r.Policy.MaxRetries)
	}
	return fmt.Sprintf("timeout=%s perAttempt=%s retries=%s",
		d(r.Policy.Timeout), d(r.Policy.PerAttemptTimeout), ret)
}

func dur(v time.Duration) *time.Duration { return &v }
func intPtr(v int) *int                  { return &v }
