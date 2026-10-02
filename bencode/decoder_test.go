package bencode

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func valsString(vals []*Value) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = v.String()
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func list(items ...*Value) *Value { return &Value{Kind: KindList, List: items} }

func dict(entries ...DictEntry) *Value { return &Value{Kind: KindDict, Dict: entries} }

func pair(k string, v *Value) DictEntry { return DictEntry{Key: k, Val: v} }

// feedAll feeds the chunks in order and returns all delivered values plus
// the first real (non-poisoned) error. After a failure it keeps feeding
// the remaining chunks and asserts the decoder is sticky: every later
// Feed returns ErrPoisoned and neither counters nor state change.
func feedAll(t *testing.T, d *Decoder, chunks [][]byte) ([]*Value, error) {
	t.Helper()
	var vals []*Value
	var firstErr error
	var frozenConsumed int64
	var frozenBuffered int
	for _, c := range chunks {
		got, err := d.Feed(c)
		if firstErr != nil {
			if err != ErrPoisoned {
				t.Fatalf("after failure Feed(%q) returned %v, want ErrPoisoned", c, err)
			}
			if got != nil {
				t.Fatalf("after failure Feed(%q) delivered values %v", c, got)
			}
			if d.Consumed() != frozenConsumed || d.Buffered() != frozenBuffered {
				t.Fatalf("poisoned decoder changed state: consumed %d->%d, buffered %d->%d",
					frozenConsumed, d.Consumed(), frozenBuffered, d.Buffered())
			}
			continue
		}
		if err != nil {
			firstErr = err
			frozenConsumed = d.Consumed()
			frozenBuffered = d.Buffered()
		}
		vals = append(vals, got...)
	}
	return vals, firstErr
}

func TestInt64Boundaries(t *testing.T) {
	valid := []struct {
		input string
		want  int64
	}{
		{"i-9223372036854775808e", math.MinInt64},
		{"i9223372036854775807e", math.MaxInt64},
		{"i0e", 0},
		{"i-1e", -1},
	}
	for _, tc := range valid {
		d := NewDecoder(Options{})
		vals, err := feedAll(t, d, [][]byte{[]byte(tc.input)})
		if err != nil {
			t.Fatalf("input %q: unexpected error %v", tc.input, err)
		}
		want := []*Value{Int(tc.want)}
		if !reflect.DeepEqual(vals, want) {
			t.Fatalf("input %q: got %s, want %s", tc.input, valsString(vals), valsString(want))
		}
		if d.Consumed() != int64(len(tc.input)) || d.Buffered() != 0 {
			t.Fatalf("input %q: consumed=%d buffered=%d, want %d/0",
				tc.input, d.Consumed(), d.Buffered(), len(tc.input))
		}
		t.Logf("input=%q output=%s 判定依据=int64 边界值必须被精确接受", tc.input, valsString(vals))
	}

	overflow := []struct {
		input  string
		offset int64
	}{
		{"i9223372036854775808e", 19},  // 末位数字使幅值超过 MaxInt64
		{"i-9223372036854775809e", 20}, // 末位数字使幅值超过 MinInt64 的幅值
		{"i99999999999999999999e", 19}, // 第 19 位数字越界
	}
	for _, tc := range overflow {
		d := NewDecoder(Options{})
		_, err := feedAll(t, d, [][]byte{[]byte(tc.input)})
		var perr *Error
		if !errors.As(err, &perr) {
			t.Fatalf("input %q: got %v, want *Error", tc.input, err)
		}
		if !errors.Is(err, ErrIntOverflow) || perr.Offset != tc.offset {
			t.Fatalf("input %q: got %v, want ErrIntOverflow at %d", tc.input, perr, tc.offset)
		}
		t.Logf("input=%q output=%v 判定依据=使数值越界的那个数字字节即违规点", tc.input, perr)
	}
}

func TestValidValues(t *testing.T) {
	cases := []struct {
		name  string
		opts  Options
		input string
		want  []*Value
	}{
		{"empty string", Options{}, "0:", []*Value{Str("")}},
		{"string", Options{}, "3:abc", []*Value{Str("abc")}},
		{"empty list", Options{}, "le", []*Value{list()}},
		{"empty dict", Options{}, "de", []*Value{dict()}},
		{"nested list", Options{}, "ll0:ee", []*Value{list(list(Str("")))}},
		{"depth exactly D", Options{MaxDepth: 2}, "ll0:ee", []*Value{list(list(Str("")))}},
		{"dict value list at depth D", Options{MaxDepth: 2}, "d1:al0:ee",
			[]*Value{dict(pair("a", list(Str(""))))}},
		{"prefix keys ascending", Options{}, "d1:a0:2:aa0:e",
			[]*Value{dict(pair("a", Str("")), pair("aa", Str("")))}},
		{"empty key smallest", Options{}, "d0:0:1:a0:e",
			[]*Value{dict(pair("", Str("")), pair("a", Str("")))}},
		{"mixed dict", Options{}, "d1:ai1e1:bli2e3:xyzee",
			[]*Value{dict(pair("a", Int(1)), pair("b", list(Int(2), Str("xyz"))))}},
		{"multiple top level", Options{}, "i1e0:li2ee",
			[]*Value{Int(1), Str(""), list(Int(2))}},
		{"string at MaxString", Options{MaxString: 3}, "3:abc", []*Value{Str("abc")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDecoder(tc.opts)
			vals, err := feedAll(t, d, [][]byte{[]byte(tc.input)})
			if err != nil {
				t.Fatalf("input %q: unexpected error %v", tc.input, err)
			}
			if !reflect.DeepEqual(vals, tc.want) {
				t.Fatalf("input %q: got %s, want %s", tc.input, valsString(vals), valsString(tc.want))
			}
			if d.Consumed() != int64(len(tc.input)) || d.Buffered() != 0 {
				t.Fatalf("input %q: consumed=%d buffered=%d, want %d/0",
					tc.input, d.Consumed(), d.Buffered(), len(tc.input))
			}
			reencoded := ""
			for _, v := range vals {
				reencoded += v.String()
			}
			if reencoded != tc.input {
				t.Fatalf("input %q: re-encode gives %q", tc.input, reencoded)
			}
			t.Logf("input=%q output=%s 判定依据=规范性输入应完整解码且重编码一致", tc.input, valsString(vals))
		})
	}
}

func TestPartialDelivery(t *testing.T) {
	d := NewDecoder(Options{})

	vals, err := d.Feed([]byte("i1"))
	if err != nil || len(vals) != 0 {
		t.Fatalf("feed 1: vals=%s err=%v", valsString(vals), err)
	}
	if d.Consumed() != 0 || d.Buffered() != 2 {
		t.Fatalf("feed 1: consumed=%d buffered=%d, want 0/2", d.Consumed(), d.Buffered())
	}

	vals, err = d.Feed([]byte("e0:"))
	if err != nil {
		t.Fatalf("feed 2: unexpected error %v", err)
	}
	if !reflect.DeepEqual(vals, []*Value{Int(1), Str("")}) {
		t.Fatalf("feed 2: vals=%s", valsString(vals))
	}
	if d.Consumed() != 5 || d.Buffered() != 0 {
		t.Fatalf("feed 2: consumed=%d buffered=%d, want 5/0", d.Consumed(), d.Buffered())
	}

	vals, err = d.Feed([]byte("i2"))
	if err != nil || len(vals) != 0 {
		t.Fatalf("feed 3: vals=%s err=%v", valsString(vals), err)
	}
	vals, err = d.Feed([]byte("e"))
	if err != nil {
		t.Fatalf("feed 4: unexpected error %v", err)
	}
	if !reflect.DeepEqual(vals, []*Value{Int(2)}) {
		t.Fatalf("feed 4: vals=%s", valsString(vals))
	}
	if d.Consumed() != 8 || d.Buffered() != 0 {
		t.Fatalf("feed 4: consumed=%d buffered=%d, want 8/0", d.Consumed(), d.Buffered())
	}
	t.Logf("input=%q output=%s 判定依据=每个值一完成即交付，计数与切分无关",
		"i1|e0:|i2|e", "[i1e 0: i2e]")
}

func TestPoisoned(t *testing.T) {
	d := NewDecoder(Options{})
	vals, err := d.Feed([]byte("i1e"))
	if err != nil || !reflect.DeepEqual(vals, []*Value{Int(1)}) {
		t.Fatalf("feed 1: vals=%s err=%v", valsString(vals), err)
	}

	vals, err = d.Feed([]byte("x"))
	var perr *Error
	if !errors.As(err, &perr) || !errors.Is(err, ErrBadLeadingByte) || perr.Offset != 3 {
		t.Fatalf("feed 2: err=%v, want ErrBadLeadingByte at 3", err)
	}
	if len(vals) != 0 {
		t.Fatalf("feed 2: unexpected values %s", valsString(vals))
	}
	consumed, buffered := d.Consumed(), d.Buffered()
	if consumed != 3 || buffered != 1 {
		t.Fatalf("after error: consumed=%d buffered=%d, want 3/1", consumed, buffered)
	}

	for i := 0; i < 3; i++ {
		vals, err = d.Feed([]byte("i2e"))
		if err != ErrPoisoned || vals != nil {
			t.Fatalf("poisoned feed %d: vals=%s err=%v, want ErrPoisoned", i, valsString(vals), err)
		}
		if d.Consumed() != consumed || d.Buffered() != buffered {
			t.Fatalf("poisoned feed %d changed counters", i)
		}
	}
	if !errors.Is(d.Err(), ErrBadLeadingByte) {
		t.Fatalf("Err()=%v, want ErrBadLeadingByte", d.Err())
	}
	t.Logf("input=%q output=%v 判定依据=出错后进入粘滞失败态，原始错误可查询", "i1e|x|i2e", d.Err())
}

func TestErrorDeliversPriorValues(t *testing.T) {
	d := NewDecoder(Options{})
	vals, err := d.Feed([]byte("i1ei2ex"))
	if !reflect.DeepEqual(vals, []*Value{Int(1), Int(2)}) {
		t.Fatalf("vals=%s, want [i1e i2e]", valsString(vals))
	}
	var perr *Error
	if !errors.As(err, &perr) || !errors.Is(err, ErrBadLeadingByte) || perr.Offset != 6 {
		t.Fatalf("err=%v, want ErrBadLeadingByte at 6", err)
	}
	if d.Consumed() != 6 || d.Buffered() != 1 {
		t.Fatalf("consumed=%d buffered=%d, want 6/1", d.Consumed(), d.Buffered())
	}
	t.Logf("input=%q output=%s err=%v 判定依据=出错点之前已完整的值仍须交付",
		"i1ei2ex", valsString(vals), perr)
}

// eachChunking invokes fn with every possible chunking of data. For short
// inputs it enumerates all 2^(n-1) split masks; for longer ones it uses
// every single split point, every pair of split points, and byte-by-byte
// delivery.
func eachChunking(data []byte, fn func(chunks [][]byte)) {
	n := len(data)
	if n <= 1 {
		fn([][]byte{data})
		return
	}
	if n <= 16 {
		for mask := 0; mask < 1<<(n-1); mask++ {
			var chunks [][]byte
			start := 0
			for i := 0; i < n-1; i++ {
				if mask&(1<<i) != 0 {
					chunks = append(chunks, data[start:i+1])
					start = i + 1
				}
			}
			chunks = append(chunks, data[start:])
			fn(chunks)
		}
		return
	}
	for i := 1; i < n; i++ {
		fn([][]byte{data[:i], data[i:]})
	}
	for i := 1; i < n-1; i++ {
		for j := i + 1; j < n; j++ {
			fn([][]byte{data[:i], data[i:j], data[j:]})
		}
	}
	var bytewise [][]byte
	for i := 0; i < n; i++ {
		bytewise = append(bytewise, data[i:i+1])
	}
	fn(bytewise)
}

func TestChunkingInvariance(t *testing.T) {
	cases := []struct {
		opts  Options
		input string
	}{
		{Options{}, "i0e"},
		{Options{}, "i-1e"},
		{Options{}, "i9223372036854775807e"},
		{Options{}, "i-9223372036854775808e"},
		{Options{}, "i9223372036854775808e"},
		{Options{}, "i-0e"},
		{Options{}, "i00e"},
		{Options{}, "ie"},
		{Options{}, "i-e"},
		{Options{}, "i1xe"},
		{Options{}, "0:"},
		{Options{}, "3:abc"},
		{Options{}, "03:a"},
		{Options{}, "1x"},
		{Options{}, "x"},
		{Options{}, "le"},
		{Options{}, "de"},
		{Options{}, "ll0:ee"},
		{Options{}, "li1e0:ee"},
		{Options{}, "d1:ai1ee"},
		{Options{}, "d1:a1:b1:a1:ce"},
		{Options{}, "d1:b1:a1:a1:ce"},
		{Options{}, "d1:a0:2:aa0:e"},
		{Options{}, "d2:aa0:1:a0:e"},
		{Options{}, "d0:0:e"},
		{Options{}, "d0:0:0:0:e"},
		{Options{}, "d0:0:1:a0:e"},
		{Options{}, "di1ee"},
		{Options{}, "i1e0:li2ee"},
		{Options{}, "i1e0:lei2ee"},
		{Options{}, "i1ex"},
		{Options{}, "i1"},
		{Options{}, "3:ab"},
		{Options{}, "l"},
		{Options{MaxDepth: 2}, "ll0:ee"},
		{Options{MaxDepth: 2}, "lll0:eee"},
		{Options{MaxDepth: 2}, "d1:al0:ee"},
		{Options{MaxDepth: 2}, "d1:all0:eee"},
		{Options{MaxString: 3}, "3:abc"},
		{Options{MaxString: 3}, "4:abcd"},
		{Options{MaxString: 3}, "10:aaaaaaaaaa"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			data := []byte(tc.input)
			wantVals, wantReason, wantOffset, wantConsumed := naiveDecode(data, tc.opts)
			truncated := wantReason == ErrTruncated
			chunkings := 0
			eachChunking(data, func(chunks [][]byte) {
				chunkings++
				d := NewDecoder(tc.opts)
				vals, err := feedAll(t, d, chunks)
				if !reflect.DeepEqual(vals, wantVals) {
					t.Fatalf("chunks=%q: vals=%s, want %s", chunks, valsString(vals), valsString(wantVals))
				}
				if wantReason == nil || truncated {
					if err != nil {
						t.Fatalf("chunks=%q: err=%v, want nil", chunks, err)
					}
				} else {
					var perr *Error
					if !errors.As(err, &perr) {
						t.Fatalf("chunks=%q: err=%v, want *Error(%v)", chunks, err, wantReason)
					}
					if !errors.Is(err, wantReason) || perr.Offset != wantOffset {
						t.Fatalf("chunks=%q: got %v, want %v at %d", chunks, perr, wantReason, wantOffset)
					}
				}
				if d.Consumed() != wantConsumed {
					t.Fatalf("chunks=%q: consumed=%d, want %d", chunks, d.Consumed(), wantConsumed)
				}
				if wantReason == nil || truncated {
					// While healthy every fed byte is either consumed or
					// buffered; after an error the state is frozen, so
					// Buffered depends on where the error fell in a chunk.
					if d.Buffered() != len(data)-int(wantConsumed) {
						t.Fatalf("chunks=%q: buffered=%d, want %d", chunks, d.Buffered(), len(data)-int(wantConsumed))
					}
				}
				reencoded := ""
				for _, v := range vals {
					reencoded += v.String()
				}
				if reencoded != string(data[:wantConsumed]) {
					t.Fatalf("chunks=%q: re-encode %q != consumed prefix %q",
						chunks, reencoded, data[:wantConsumed])
				}
			})
			t.Logf("input=%q output=%s reason=%v offset=%d consumed=%d chunkings=%d 判定依据=任意切分下与朴素整体解码器结果一致",
				tc.input, valsString(wantVals), wantReason, wantOffset, wantConsumed, chunkings)
		})
	}
}

func TestDecodeAll(t *testing.T) {
	vals, err := DecodeAll([]byte("i1e0:"), Options{})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if !reflect.DeepEqual(vals, []*Value{Int(1), Str("")}) {
		t.Fatalf("vals=%s", valsString(vals))
	}

	_, err = DecodeAll([]byte("i1e3:ab"), Options{})
	var perr *Error
	if !errors.As(err, &perr) || !errors.Is(err, ErrTruncated) || perr.Offset != 3 {
		t.Fatalf("truncated: err=%v, want ErrTruncated at 3", err)
	}

	_, err = DecodeAll([]byte("i-0e"), Options{})
	if !errors.Is(err, ErrNegativeZero) {
		t.Fatalf("err=%v, want ErrNegativeZero", err)
	}
	t.Logf("判定依据=DecodeAll 整体解码：完整输入全量交付，截断输入报 ErrTruncated")
}

func TestConcurrentAccess(t *testing.T) {
	input := strings.Repeat("i1e", 50) + "d1:a1:be" + strings.Repeat("0:", 50)
	wantVals, err := DecodeAll([]byte(input), Options{})
	if err != nil {
		t.Fatalf("reference decode failed: %v", err)
	}

	d := NewDecoder(Options{})
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = d.Consumed()
					_ = d.Buffered()
					_ = d.Err()
				}
			}
		}()
	}
	var got []*Value
	for i := 0; i < len(input); i++ {
		vals, err := d.Feed([]byte{input[i]})
		if err != nil {
			t.Fatalf("byte %d: unexpected error %v", i, err)
		}
		got = append(got, vals...)
	}
	close(stop)
	wg.Wait()

	if !reflect.DeepEqual(got, wantVals) {
		t.Fatalf("got %d values, want %d", len(got), len(wantVals))
	}
	if d.Consumed() != int64(len(input)) || d.Buffered() != 0 {
		t.Fatalf("consumed=%d buffered=%d, want %d/0", d.Consumed(), d.Buffered(), len(input))
	}
	t.Logf("input=%d 字节 output=%d 个值 判定依据=并发查询下逐字节 Feed 的结果与整体解码一致",
		len(input), len(got))
}

func TestErrorOffsets(t *testing.T) {
	cases := []struct {
		name   string
		opts   Options
		input  string
		reason error
		offset int64
	}{
		{"int leading zero", Options{}, "i00e", ErrIntLeadingZero, 2},
		{"int leading zero nonzero next", Options{}, "i01e", ErrIntLeadingZero, 2},
		{"negative zero", Options{}, "i-0e", ErrNegativeZero, 2},
		{"negative zero with more digits", Options{}, "i-01e", ErrIntLeadingZero, 3},
		{"empty integer", Options{}, "ie", ErrSyntax, 1},
		{"sign without digits", Options{}, "i-e", ErrSyntax, 2},
		{"garbage inside integer", Options{}, "i1xe", ErrSyntax, 2},
		{"plus sign rejected", Options{}, "i+1e", ErrSyntax, 1},
		{"length leading zero", Options{}, "03:a", ErrLenLeadingZero, 1},
		{"length double zero", Options{}, "00:", ErrLenLeadingZero, 1},
		{"garbage after length", Options{}, "1x", ErrSyntax, 1},
		{"bad leading byte", Options{}, "x", ErrBadLeadingByte, 0},
		{"bad leading byte in list", Options{}, "lxe", ErrBadLeadingByte, 1},
		{"dict key not string", Options{}, "di1ee", ErrKeyNotString, 1},
		{"dict key not string nested", Options{}, "d1:adlee", ErrKeyNotString, 5},
		{"duplicate key", Options{}, "d1:a1:b1:a1:ce", ErrDuplicateKey, 7},
		{"out of order key", Options{}, "d1:b1:a1:a1:ce", ErrKeyOutOfOrder, 7},
		{"prefix keys in order", Options{}, "d2:aa0:1:a0:e", ErrKeyOutOfOrder, 7},
		{"duplicate empty key", Options{}, "d0:0:0:0:e", ErrDuplicateKey, 5},
		{"string too long first digit", Options{MaxString: 3}, "4:abcd", ErrStringTooLong, 0},
		{"string too long second digit", Options{MaxString: 3}, "10:aaaaaaaaaa", ErrStringTooLong, 1},
		{"depth exceeded in list", Options{MaxDepth: 2}, "lll0:eee", ErrDepthExceeded, 2},
		{"depth exceeded in dict value", Options{MaxDepth: 2}, "d1:all0:eee", ErrDepthExceeded, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDecoder(tc.opts)
			_, err := feedAll(t, d, [][]byte{[]byte(tc.input)})
			var perr *Error
			if !errors.As(err, &perr) {
				t.Fatalf("input %q: got %v, want *Error", tc.input, err)
			}
			if !errors.Is(err, tc.reason) {
				t.Fatalf("input %q: reason %v, want %v", tc.input, perr.Reason, tc.reason)
			}
			if perr.Offset != tc.offset {
				t.Fatalf("input %q: offset %d, want %d", tc.input, perr.Offset, tc.offset)
			}
			if d.Err() == nil || !errors.Is(d.Err(), tc.reason) {
				t.Fatalf("input %q: Err()=%v, want %v", tc.input, d.Err(), tc.reason)
			}
			t.Logf("input=%q output=%v 判定依据=首个违规字节的绝对偏移", tc.input, perr)
		})
	}
}
