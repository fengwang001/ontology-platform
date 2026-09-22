package record

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"ontology/digest"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestLifecycleSucceed(t *testing.T) {
	r := New(digest.Of([]byte("body")), base, time.Minute)
	if r.State() != Running || r.Attempts() != 1 {
		t.Fatal("新记录应为执行中且尝试次数为 1")
	}

	r.Succeed([]byte("ok"))
	<-r.Done()

	if r.State() != Succeeded || !bytes.Equal(r.Result(), []byte("ok")) || r.Err() != nil {
		t.Fatal("成功后应保存结果且无错误")
	}
	if got := r.Result(); cap(got) == 0 {
		t.Fatal("结果应为副本，不得为 nil")
	}
}

func TestLifecycleFailThenRetry(t *testing.T) {
	r := New(digest.Of([]byte("body")), base, time.Minute)
	boom := errors.New("boom")
	r.Fail(boom)

	if r.State() != Failed || !errors.Is(r.Err(), boom) || r.Result() != nil {
		t.Fatal("失败应原样保存错误且无结果")
	}

	r.Retry(digest.Of([]byte("body")), base.Add(time.Second), time.Minute)
	if r.State() != Running || r.Attempts() != 2 || r.Err() != nil {
		t.Fatal("重试后应回到执行中且尝试次数加一")
	}
	select {
	case <-r.Done():
		t.Fatal("新一次执行的等待通道不应提前关闭")
	default:
	}

	r.Succeed([]byte("fixed"))
	if !bytes.Equal(r.Result(), []byte("fixed")) || r.State() != Succeeded {
		t.Fatal("重试成功后结果应被固定")
	}
}

func TestRetryCanReplaceFingerprint(t *testing.T) {
	r := New(digest.Of([]byte("v1")), base, time.Minute)
	r.Fail(errors.New("e"))
	r.Retry(digest.Of([]byte("v2")), base, time.Minute)
	if r.Fingerprint().Equal(digest.Of([]byte("v1"))) {
		t.Fatal("重试应绑定新的请求体指纹")
	}
}

func TestExpiryIsLeftClosedRightOpen(t *testing.T) {
	r := New(digest.Of([]byte("b")), base, time.Minute)
	r.Succeed([]byte("r"))

	if r.ExpiredAt(base.Add(time.Minute - time.Nanosecond)) {
		t.Fatal("过期时刻前一刻不应过期")
	}
	if !r.ExpiredAt(base.Add(time.Minute)) {
		t.Fatal("恰好等于过期时刻应判为过期（左闭右开）")
	}
	if !r.ExpiredAt(base.Add(time.Hour)) {
		t.Fatal("过期时刻之后必须仍算过期")
	}
	if got := r.TTL(base.Add(2 * time.Minute)); got != 0 {
		t.Fatalf("过期后剩余存活应为 0，实际 %v", got)
	}
	if got := r.TTL(base); got != time.Minute {
		t.Fatalf("初始剩余存活应为 1m，实际 %v", got)
	}
}

func TestRunningRecordNeverExpires(t *testing.T) {
	r := New(digest.Of([]byte("b")), base, time.Nanosecond)
	if r.ExpiredAt(base.Add(time.Hour)) {
		t.Fatal("执行中的记录即使超过名义存活时长也不得过期")
	}
}
