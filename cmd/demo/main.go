// Command demo exercises the chunked transfer-encoding decoder and
// prints one OK/FAIL verdict per scenario. It takes no arguments and
// exits 0 when every scenario passes.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/chunked"
	"ontology/frame"
	"ontology/hexline"
)

var failures int

func report(name string, ok bool) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", verdict, name)
}

const message = "4;q=\"a;b=c\";x=1\r\nWiki\r\n5\r\npedia\r\n" +
	"0;done=yes\r\nX-T: 1\r\n\r\n"

func decode(msg string, seg int) ([]byte, error) {
	d := chunked.New(chunked.Config{})
	for i := 0; i < len(msg); i += seg {
		j := i + seg
		if j > len(msg) {
			j = len(msg)
		}
		if _, err := d.Write([]byte(msg[i:j])); err != nil {
			return nil, err
		}
	}
	if !d.Done() {
		return nil, errors.New("not done")
	}
	return d.Body(), nil
}

func checkSplits() {
	want, err := decode(message, len(message))
	ok := err == nil && string(want) == "Wikipedia"
	for seg := 1; ok && seg <= len(message); seg++ {
		got, err := decode(message, seg)
		ok = err == nil && bytes.Equal(got, want)
	}
	report("任意切分点结果一致", ok)
}

func checkQuotedExt() {
	// ';', '=' and an escaped quote inside the quoted extension must
	// not confuse the size-line parser.
	got, err := decode(`3;a="x;y=z";b="q\"w"`+"\r\nabc\r\n0\r\n\r\n", 1)
	report("带引号扩展被正确跳过", err == nil && string(got) == "abc")
}

func checkTrailerDone() {
	d := chunked.New(chunked.Config{})
	d.Write([]byte("1\r\na\r\n0\r\nT: v\r\n"))
	before := d.Done()
	d.Write([]byte("\r\n"))
	report("trailer 空行前不算完成", !before && d.Done())
}

func checkWriteAfterDone() {
	d := chunked.New(chunked.Config{})
	d.Write([]byte("0\r\n\r\n"))
	_, err := d.Write([]byte("x"))
	report("完成后再写入被拒", errors.Is(err, chunked.ErrClosed))
}

func checkSixErrors() {
	cases := []struct {
		msg  string
		sent error
	}{
		{"Z\r\n", hexline.ErrNotHex},
		{"1\r\naX", frame.ErrMissingCRLF},
		{"1;a=\"x\r\n", hexline.ErrUnterminatedQuote},
	}
	ok := true
	for _, c := range cases {
		_, err := chunked.New(chunked.Config{}).Write([]byte(c.msg))
		ok = ok && errors.Is(err, c.sent)
	}
	d := chunked.New(chunked.Config{MaxLineLen: 2, MaxTrailers: 0})
	_, err := d.Write([]byte("abcd"))
	ok = ok && errors.Is(err, hexline.ErrLineTooLong)
	d2 := chunked.New(chunked.Config{MaxTrailers: 1})
	_, err = d2.Write([]byte("0\r\nA: 1\r\nB: 2\r\n\r\n"))
	ok = ok && errors.Is(err, chunked.ErrTooManyTrailers)
	d3 := chunked.New(chunked.Config{})
	d3.Write([]byte("1\r\na\r"))
	err = d3.Close()
	ok = ok && errors.Is(err, chunked.ErrHalfCRLF)
	// Distinct: no sentinel matches another.
	sents := []error{hexline.ErrNotHex, hexline.ErrLineTooLong, frame.ErrMissingCRLF,
		chunked.ErrHalfCRLF, hexline.ErrUnterminatedQuote, chunked.ErrTooManyTrailers}
	for i, a := range sents {
		for j, b := range sents {
			ok = ok && (i == j || !errors.Is(a, b))
		}
	}
	report("六类错误各自可判定且互不相同", ok)
}

func checkBoundary() {
	ok := true
	for _, msg := range []string{"2\r\nabc\r\n0\r\n\r\n", "3\r\nab\r\n0\r\n\r\n", "1\r\na\n0\r\n\r\n"} {
		_, err := chunked.New(chunked.Config{}).Write([]byte(msg))
		ok = ok && errors.Is(err, frame.ErrMissingCRLF)
	}
	report("块边界多一字节/少一字节/单LF 均被拒", ok)
}

func checkLimits() {
	ok := true
	bodyKept := func(cfg chunked.Config, msg string, sent error, want string) bool {
		d := chunked.New(cfg)
		_, err := d.Write([]byte(msg))
		_, err2 := d.Write([]byte("0\r\n\r\n"))
		return errors.Is(err, sent) && errors.Is(err2, sent) && string(d.Body()) == want
	}
	ok = ok && bodyKept(chunked.Config{MaxLineLen: 3}, "1234\r\n", hexline.ErrLineTooLong, "")
	ok = ok && bodyKept(chunked.Config{MaxChunkSize: 3}, "3\r\nabc\r\n4\r\n", chunked.ErrChunkTooLarge, "abc")
	ok = ok && bodyKept(chunked.Config{MaxBodySize: 5}, "3\r\nabc\r\n3\r\n", chunked.ErrBodyTooLarge, "abc")
	ok = ok && bodyKept(chunked.Config{MaxTrailers: 1}, "1\r\nz\r\n0\r\nA: 1\r\nB: 2\r\n\r\n", chunked.ErrTooManyTrailers, "z")
	report("四类上限超限即拒且 Body 不变", ok)
}

func checkCloseStates() {
	cases := []struct {
		msg   string
		state chunked.State
	}{
		{"2", chunked.StateSizeLine},
		{"2\r\na", chunked.StateData},
		{"2\r\nab", chunked.StateAwaitCRLF},
		{"0\r\nT: v\r\n", chunked.StateTrailer},
	}
	ok := true
	for _, c := range cases {
		d := chunked.New(chunked.Config{})
		d.Write([]byte(c.msg))
		var cerr *chunked.Error
		err := d.Close()
		ok = ok && errors.Is(err, chunked.ErrIncomplete) &&
			errors.As(err, &cerr) && cerr.State == c.state
	}
	report("四个状态下提前终止可区分", ok)
}

func checkConcurrent() {
	const n = 16
	var wg sync.WaitGroup
	bad := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload := bytes.Repeat([]byte{byte('a' + i)}, 50+i)
			msg := fmt.Sprintf("%x\r\n%s\r\n0\r\nK: %d\r\n\r\n", len(payload), payload, i)
			got, err := decode(msg, 1)
			bad <- err != nil || !bytes.Equal(got, payload)
		}(i)
	}
	wg.Wait()
	close(bad)
	ok := true
	for b := range bad {
		ok = ok && !b
	}
	report("多实例并发解码互不串扰", ok)
}

func main() {
	checkSplits()
	checkQuotedExt()
	checkTrailerDone()
	checkWriteAfterDone()
	checkSixErrors()
	checkBoundary()
	checkLimits()
	checkCloseStates()
	checkConcurrent()
	fmt.Printf("TOTAL %d checks, %d failed\n", 9, failures)
	if failures > 0 {
		os.Exit(1)
	}
}
