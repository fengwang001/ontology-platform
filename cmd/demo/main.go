// Command demo 逐条打印规范 Huffman 实现的自检结果，全部 OK 时退出码为 0。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"

	"ontology/api"
	"ontology/hcode"
	"ontology/htree"
)

var specFreq = map[byte]int{'A': 5, 'B': 2, 'C': 1, 'D': 1, 'E': 1}

func specTable() *hcode.Table {
	lengths, _ := htree.Lengths(specFreq)
	return hcode.New(lengths)
}

// bitsOf 把所有符号的规范码字拼成位串。
func bitsOf(t *hcode.Table, msg string) string {
	var sb strings.Builder
	for i := 0; i < len(msg); i++ {
		c, _ := t.CodeOf(msg[i])
		sb.WriteString(c)
	}
	return sb.String()
}

// misread 用「B 的码长误读成 2」的错误码表贪心解码，演示 (乙)。
func misread(bits string) string {
	wrong := map[string]byte{"0": 'A', "10": 'B', "101": 'C', "110": 'D', "111": 'E'}
	var out []byte
	for len(bits) > 0 {
		for l := 1; l <= 3 && l <= len(bits); l++ {
			if s, ok := wrong[bits[:l]]; ok {
				out = append(out, s)
				bits = bits[l:]
				break
			}
		}
	}
	return string(out)
}

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK  ", name)
}

func main() {
	lengths, err := htree.Lengths(specFreq)
	wantLen := map[byte]int{'A': 1, 'B': 3, 'C': 3, 'D': 3, 'E': 3}
	check("码长 A=1,B=C=D=E=3", err == nil && reflect.DeepEqual(lengths, wantLen))
	t := specTable()
	var gotCodes []string
	for _, s := range []byte("ABCDE") {
		c, _ := t.CodeOf(s)
		gotCodes = append(gotCodes, c)
	}
	check("规范码字 0/100/101/110/111", reflect.DeepEqual(gotCodes,
		[]string{"0", "100", "101", "110", "111"}))

	enc, err := t.Encode([]byte("AABCCD"))
	check("AABCCD=00100101101110=0x25B8", err == nil &&
		bitsOf(t, "AABCCD") == "00100101101110" &&
		bytes.Equal(enc, []byte{0x25, 0xB8}))

	tree, _ := htree.Codes(specFreq)
	check("左0右1对照 B/C/D/E 循环顺移", tree['A'] == "0" && tree['B'] == "111" &&
		tree['C'] == "100" && tree['D'] == "101" && tree['E'] == "110")
	check("误读B码长→AABABDEA", misread("00100101101110") == "AABABDEA")
	c, _ := api.New(specFreq)
	empty, err1 := c.Encode(nil)
	one, err2 := api.New(map[byte]int{'A': 1})
	oneCode, _ := one.CodeOf('A')
	aa, _ := one.Encode([]byte("AA"))
	check("空消息0字节/单符号码0/AA=2位", err1 == nil && len(empty) == 0 &&
		err2 == nil && oneCode == "0" && bytes.Equal(aa, []byte{0x00}))

	_, e1 := api.New(map[byte]int{})
	_, e2 := api.New(map[byte]int{'A': 0, 'B': 0})
	_, e3 := api.New(map[byte]int{'A': -1})
	check("非法频率→ErrInvalidFreq", errors.Is(e1, api.ErrInvalidFreq) &&
		errors.Is(e2, api.ErrInvalidFreq) && errors.Is(e3, api.ErrInvalidFreq))
	msg := []byte("AABCCDEABCDE")
	enc, _ = c.Encode(msg)
	dec, err := c.Decode(enc, len(msg))
	st := c.NewStream()
	for _, b := range enc {
		st.Feed([]byte{b}) // 逐字节喂入
	}
	dec2, err2 := st.Decode(len(msg))
	check("往返一致+切分点无关+SelfCheck", err == nil && err2 == nil &&
		bytes.Equal(dec, msg) && bytes.Equal(dec2, msg) && api.SelfCheck() == nil)

	_, eu := c.Encode([]byte("Z"))
	_, et := c.Decode([]byte{0x25}, 6)
	_, ep := c.Decode([]byte{0x25, 0xB9}, 6)
	distinct := !errors.Is(api.ErrUnknownSymbol, api.ErrTruncated) &&
		!errors.Is(api.ErrTruncated, api.ErrIllegalPadding) &&
		!errors.Is(api.ErrIllegalPadding, api.ErrInvalidFreq)
	stillOK, err3 := c.Encode(msg)
	check("四类错误可区分+被拒后无部分输出", errors.Is(eu, api.ErrUnknownSymbol) &&
		errors.Is(et, api.ErrTruncated) && errors.Is(ep, api.ErrIllegalPadding) &&
		distinct && err3 == nil && bytes.Equal(stillOK, enc))

	big := map[byte]int{}
	for i := 0; i < 256; i++ {
		big[byte(i)] = i + 1
	}
	bc, err := api.New(big)
	var bmsg []byte
	for i := 0; i < 256; i++ {
		bmsg = append(bmsg, byte(i), byte(255-i))
	}
	benc, err1 := bc.Encode(bmsg)
	bdec, err2 := bc.Decode(benc, len(bmsg))
	var wg sync.WaitGroup
	raceOK := true
	for g := 0; g < 8; g++ { // 并发结果须与串行逐字节相同
		wg.Add(1)
		go func() {
			defer wg.Done()
			e, _ := bc.Encode(bmsg)
			d, _ := bc.Decode(e, len(bmsg))
			if !bytes.Equal(e, benc) || !bytes.Equal(d, bmsg) {
				raceOK = false
			}
		}()
	}
	wg.Wait()
	check("大m解码正确(条目数有界见测试)+并发一致", err == nil && err1 == nil &&
		err2 == nil && bytes.Equal(bdec, bmsg) && raceOK)
	if failed {
		os.Exit(1)
	}
}
