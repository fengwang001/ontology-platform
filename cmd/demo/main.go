package main

import (
	"bytes"
	"errors"
	"fmt"

	"ontology/partscan"
)

func encode(boundary string, parts [][]byte) []byte {
	var b bytes.Buffer
	b.WriteString("--" + boundary + "\r\n")
	for i, p := range parts {
		b.Write(p)
		if i < len(parts)-1 {
			b.WriteString("\r\n--" + boundary + "\r\n")
		}
	}
	b.WriteString("\r\n--" + boundary + "--")
	return b.Bytes()
}

func feedChunks(boundary string, data []byte, at ...int) ([][]byte, error) {
	s := partscan.New(boundary)
	var got [][]byte
	pos, i := 0, 0
	for pos < len(data) {
		n := at[i%len(at)]
		i++
		if n > len(data)-pos {
			n = len(data) - pos
		}
		ps, err := s.Feed(append([]byte(nil), data[pos:pos+n]...))
		if err != nil {
			return nil, err
		}
		got = append(got, ps...)
		pos += n
	}
	return got, s.Close()
}

func flatten(parts [][]byte) [][]byte {
	out := make([][]byte, len(parts))
	for i, p := range parts {
		out[i] = append([]byte(nil), p...)
	}
	return out
}

func report(name string, ok bool) {
	tag := "OK"
	if !ok {
		tag = "FAIL"
	}
	fmt.Printf("%s  %s\n", tag, name)
}

func main() {
	boundary := "b"
	parts := [][]byte{
		[]byte("hello"),
		{},
		[]byte("\r\n--bou"),
		[]byte("x\r\n-b\r\n--bZ"),
		[]byte("tail"),
	}
	stream := encode(boundary, parts)
	want := flatten(parts)

	// 1. 切法不变性（含把 "\r\n--b" 切成 "\r\n-"/"-b"）。
	one, err1 := feedChunks(boundary, stream, len(stream))
	byteAt, err2 := feedChunks(boundary, stream, 1)
	halfIdx := len("--b\r\n") + len("hello") + 3
	halved, err3 := feedChunks(boundary, stream, halfIdx, 2, 1, 9)
	splitOK := err1 == nil && err2 == nil && err3 == nil &&
		len(one) == len(want) && len(byteAt) == len(want) && len(halved) == len(want)
	if splitOK {
		for i := range want {
			if !bytes.Equal(one[i], want[i]) || !bytes.Equal(byteAt[i], want[i]) ||
				!bytes.Equal(halved[i], want[i]) {
				splitOK = false
			}
		}
	}
	report("1. 切法不变性（整块/逐字节/分隔符切半结果一致）", splitOK)

	// 2. 段内分隔符前缀原样保留。
	prefixOK := splitOK &&
		bytes.Contains(byteAt[2], []byte("\r\n--bou")) &&
		bytes.Contains(byteAt[3], []byte("\r\n--bZ"))
	report("2. 段内部分匹配前缀原样保留", prefixOK)

	// 3. 空段返回长度 0 的非 nil 切片。
	emptyOK := len(byteAt) > 1 && byteAt[1] != nil && len(byteAt[1]) == 0
	report("3. 空段合法（非 nil 的零长度切片）", emptyOK)

	// 4. 开头校验。
	s4 := partscan.New(boundary)
	_, e4 := s4.Feed([]byte("--nope\r\n"))
	_, e4b := s4.Feed([]byte("--b\r\n"))
	preambleOK := errors.Is(e4, partscan.ErrNoPreamble) && errors.Is(e4b, partscan.ErrNoPreamble)
	report("4. 开头非法即 ErrNoPreamble 且错误粘滞", preambleOK)

	// 5. 结束分隔符后的处理。
	s5 := partscan.New(boundary)
	ps5, e5 := s5.Feed([]byte("--b\r\nonly\r\n--b--"))
	_, eEmpty := s5.Feed(nil)
	_, eAfter := s5.Feed([]byte("junk"))
	closeOK := e5 == nil && len(ps5) == 1 && string(ps5[0]) == "only" &&
		s5.Done() && eEmpty == nil && errors.Is(eAfter, partscan.ErrAfterClose) &&
		s5.Close() == nil
	report("5. 结束分隔符后 Done；非空数据 ErrAfterClose，空数据/Close 无错", closeOK)

	// 6. 未结束就 Close。
	s6 := partscan.New(boundary)
	_, _ = s6.Feed([]byte("--b\r\nabc"))
	e6a, e6b := s6.Close(), s6.Close()
	_, e6c := s6.Feed([]byte("x"))
	incompleteOK := errors.Is(e6a, partscan.ErrIncomplete) &&
		errors.Is(e6b, partscan.ErrIncomplete) && errors.Is(e6c, partscan.ErrIncomplete)
	report("6. 未结束 Close 返回 ErrIncomplete 且可重复", incompleteOK)

	// 7. 拷贝隔离。
	s7 := partscan.New(boundary)
	in := []byte("--b\r\nAAAA\r\n--b\r\nBBBB\r\n--b--")
	ps7, _ := s7.Feed(in)
	for i := range in {
		in[i] = 'Z'
	}
	ps7[0][0] = 'Z'
	fresh, _ := partscan.New(boundary).Feed([]byte("--b\r\nAAAA\r\n--b--"))
	isolationOK := string(ps7[1]) == "BBBB" && string(fresh[0]) == "AAAA"
	report("7. 拷贝隔离（改写入参/返回段互不影响）", isolationOK)

	// 8. 暂存有界：一万个段后 Pending 归零，缓冲容量不膨胀。
	s8 := partscan.New(boundary)
	_, _ = s8.Feed([]byte("--b\r\n"))
	bounded := true
	for i := 0; i < 10000; i++ {
		if _, err := s8.Feed(append([]byte("seg"), []byte("\r\n--b\r\n")...)); err != nil {
			bounded = false
			break
		}
		if s8.Pending() != 0 {
			bounded = false
			break
		}
	}
	report("8. 一万段后 Pending 归零、缓冲不线性增长", bounded)
}
