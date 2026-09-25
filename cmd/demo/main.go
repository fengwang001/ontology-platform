package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func ok(name string, cond bool) {
	if !cond {
		failed = true
	}
	tag := "OK  "
	if !cond {
		tag = "FAIL"
	}
	fmt.Printf("%s %s\n", tag, name)
}

func pack(bits string) []byte {
	var out []byte
	for i := 0; i < len(bits); i += 8 {
		var b byte
		for j := 0; j < 8 && i+j < len(bits); j++ {
			if bits[i+j] == '1' {
				b |= 1 << uint(7-j)
			}
		}
		out = append(out, b)
	}
	return out
}

func main() {
	freq := map[byte]int{'A': 5, 'B': 2, 'C': 1, 'D': 1, 'E': 1}
	c, err := api.New(freq)
	if err != nil {
		fmt.Println("FAIL New:", err)
		os.Exit(1)
	}
	// 1. lengths and canonical codewords
	want := map[byte]string{'A': "0", 'B': "100", 'C': "101", 'D': "110", "E"[0]: "111"}
	good := true
	for s, w := range want {
		g, _ := c.CodeString(s)
		good = good && g == w
	}
	ok("码长与规范码字 A=0 B=100 C=101 D=110 E=111", good)
	// 2. encode "AABCCD"
	enc, _ := c.Encode([]byte("AABCCD"))
	ok(`"AABCCD" -> 00100101101110 -> 0x25 0xB8`, bytes.Equal(enc, []byte{0x25, 0xB8}))
	// 3. left-0/right-1 tree codes vs canonical; misread B(100) as 10
	tree := map[byte]string{'A': "0", 'B': "110", 'C': "100", 'D': "101", "E"[0]: "111"}
	swap := tree['B'] == "110" && tree['C'] == "100" && tree['D'] == "101" // B/C/D 循环互换
	rest, _ := c.Decode(pack("0101101110"), 4)                             // AA + 误读"10"=B 之后的余位
	ok("左0右1对照 B/C/D 互换；误读码长得 AABACCD", swap && string(rest) == "ACCD")
	// 4. empty message, single-symbol alphabet, invalid freq
	e0, _ := c.Encode(nil)
	c1, e1 := api.New(map[byte]int{'A': 1})
	aa, _ := c1.Encode([]byte("AA"))
	_, eInv := api.New(map[byte]int{'A': 0, 'B': 0})
	ok("空消息->0字节; 单符号码0, AA->0x00; 全零频率->ErrInvalidFreq",
		len(e0) == 0 && e1 == nil && bytes.Equal(aa, []byte{0x00}) && errors.Is(eInv, api.ErrInvalidFreq))
	// 5. round-trip over generated messages
	good = true
	for n := 0; n <= 64; n++ {
		m := make([]byte, n)
		for i := range m {
			m[i] = "ABCDE"[(i*7+n)%5]
		}
		b, _ := c.Encode(m)
		d, err := c.Decode(b, n)
		good = good && err == nil && bytes.Equal(d, m)
	}
	ok("往返一致 Decode(Encode(m))==m", good)
	// 6. chunking invariance
	good = true
	for cut := 0; cut <= len(enc); cut++ {
		st := c.NewStream(6)
		st.Feed(enc[:cut])
		st.Feed(enc[cut:])
		d, err := st.Finish()
		good = good && err == nil && bytes.Equal(d, []byte("AABCCD"))
	}
	ok("切分点无关 Feed 任意切块", good)
	// 7. four distinguishable errors, no partial output
	d1, e1 := c.Encode([]byte("AZ"))
	d2, e2 := c.Decode([]byte{0x25}, 6)
	d3, e3 := c.Decode([]byte{0x25, 0xB9}, 6)
	_, e4 := api.New(map[byte]int{})
	ok("四类哨兵错误可区分且被拒后无部分输出",
		errors.Is(e1, api.ErrUnknownSymbol) && errors.Is(e2, api.ErrTruncated) &&
			errors.Is(e3, api.ErrIllegalPadding) && errors.Is(e4, api.ErrInvalidFreq) &&
			!errors.Is(e1, api.ErrTruncated) && d1 == nil && d2 == nil && d3 == nil)
	// 8. large alphabets decode correctly (entry-count bound pinned by TestDecodeChecksBound)
	good = true
	for _, m := range []int{100, 200, 256} { // byte-keyed alphabet caps at 256
		f := map[byte]int{}
		for i := 0; i < m; i++ {
			f[byte(i)] = i + 1
		}
		cc, err := api.New(f)
		b, _ := cc.Encode([]byte{byte(m - 1), 0})
		d, err2 := cc.Decode(b, 2)
		good = good && err == nil && err2 == nil && d[0] == byte(m-1) && d[1] == 0
	}
	ok("大字母表 m=100..256 解码正确（检查条目数上界见测试）", good)
	// 9. concurrent Encode/Decode matches serial
	serial, _ := c.Encode([]byte("ABCDEABCDE"))
	var wg sync.WaitGroup
	good = true
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, _ := c.Encode([]byte("ABCDEABCDE"))
			d, _ := c.Decode(b, 10)
			if !bytes.Equal(b, serial) || !bytes.Equal(d, []byte("ABCDEABCDE")) {
				good = false
			}
		}()
	}
	wg.Wait()
	ok("并发 Encode/Decode 与串行逐字节一致", good)
	// 10. self check
	ok("SelfCheck 四条不变量", api.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
