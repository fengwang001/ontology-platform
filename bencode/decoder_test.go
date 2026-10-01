package bencode_test

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/bencode"
)

// feedChunks 依次喂入各分片，返回交付的值序列与首个非粘滞错误。
func feedChunks(dec *bencode.Decoder, chunks ...[]byte) ([]any, error) {
	var vals []any
	var firstErr error
	for _, c := range chunks {
		out, err := dec.Feed(c)
		vals = append(vals, out...)
		if err == nil {
			continue
		}
		if errors.Is(err, bencode.ErrPoisoned) {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return vals, firstErr
}

// sameError 判定两个错误是否具有相同原因与偏移。
func sameError(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	var ea, eb *bencode.Error
	if !errors.As(a, &ea) || !errors.As(b, &eb) {
		return false
	}
	return ea.Offset == eb.Offset && errors.Is(a, eb.Kind)
}

// result 是一次完整解码的可比结果。
type result struct {
	vals     []any
	consumed int64
	buffered int64
	err      error
}

func runChunks(dec *bencode.Decoder, chunks ...[]byte) result {
	vals, err := feedChunks(dec, chunks...)
	return result{vals, dec.Consumed(), dec.Buffered(), err}
}

func checkSameResult(t *testing.T, input string, got, want result) {
	t.Helper()
	if !reflect.DeepEqual(got.vals, want.vals) ||
		got.consumed != want.consumed ||
		got.buffered != want.buffered ||
		!sameError(got.err, want.err) {
		t.Fatalf("结果不一致:\n输入=%q\n得到=%+v\n期望=%+v", input, got, want)
	}
}

func (r result) String() string {
	return fmt.Sprintf("vals=%v consumed=%d buffered=%d err=%v", r.vals, r.consumed, r.buffered, r.err)
}

// ruleCase 描述一条规范规则的定点用例。
type ruleCase struct {
	name     string
	input    string
	maxStr   uint64
	maxDepth int
	vals     []any  // 期望交付的顶层值（出错时只含出错点之前完成的值）
	kind     error  // 期望错误原因，nil 表示无错
	offset   int64  // 期望错误偏移
	basis    string // 判定依据（日志用）
}

func runRuleCases(t *testing.T, cases []ruleCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			maxStr, maxDepth := tc.maxStr, tc.maxDepth
			if maxStr == 0 {
				maxStr = bencode.DefaultMaxString
			}
			if maxDepth == 0 {
				maxDepth = bencode.DefaultMaxDepth
			}
			dec := bencode.NewDecoderWithLimits(maxStr, maxDepth)
			got := runChunks(dec, []byte(tc.input))
			t.Logf("输入=%q 输出=%s 判定依据=%s", tc.input, got, tc.basis)

			if !reflect.DeepEqual(got.vals, tc.vals) {
				t.Errorf("值序列不符: 得到 %v (%T)，期望 %v", got.vals, got.vals, tc.vals)
			}
			if tc.kind == nil {
				if got.err != nil {
					t.Fatalf("期望无错误，得到 %v", got.err)
				}
				if got.consumed != int64(len(tc.input)) || got.buffered != 0 {
					t.Errorf("计数不符: consumed=%d buffered=%d，期望 consumed=%d buffered=0",
						got.consumed, got.buffered, len(tc.input))
				}
				return
			}
			if !errors.Is(got.err, tc.kind) {
				t.Fatalf("错误原因不符: 得到 %v，期望 %v", got.err, tc.kind)
			}
			var perr *bencode.Error
			if !errors.As(got.err, &perr) {
				t.Fatalf("错误类型不是 *bencode.Error: %T", got.err)
			}
			if perr.Offset != tc.offset {
				t.Errorf("错误偏移不符: 得到 %d，期望 %d", perr.Offset, tc.offset)
			}
			if got.consumed > tc.offset {
				t.Errorf("消费计数不应超过出错偏移: consumed=%d offset=%d", got.consumed, tc.offset)
			}
		})
	}
}

func TestIntegerRules(t *testing.T) {
	runRuleCases(t, []ruleCase{
		{"int64最大值", "i9223372036854775807e", 0, 0,
			[]any{int64(math.MaxInt64)}, nil, 0, "int64 上界合法"},
		{"int64最小值", "i-9223372036854775808e", 0, 0,
			[]any{int64(math.MinInt64)}, nil, 0, "int64 下界 -2^63 合法"},
		{"正数溢出", "i9223372036854775808e", 0, 0,
			nil, bencode.ErrIntOverflow, 19, "末位数字 8 使值越过 MaxInt64"},
		{"负数溢出", "i-9223372036854775809e", 0, 0,
			nil, bencode.ErrIntOverflow, 20, "末位数字 9 使绝对值越过 2^63"},
		{"零", "i0e", 0, 0,
			[]any{int64(0)}, nil, 0, "0 合法"},
		{"负零", "i-0e", 0, 0,
			nil, bencode.ErrNegativeZero, 2, "负号后的 0 即违规点"},
		{"前导零", "i01e", 0, 0,
			nil, bencode.ErrIntLeadingZero, 2, "紧随前导 0 的数字 1 即违规点"},
		{"负号后前导零", "i-01e", 0, 0,
			nil, bencode.ErrIntLeadingZero, 3, "-01 以前导零论，违规点为数字 1"},
		{"空整数ie", "ie", 0, 0,
			nil, bencode.ErrSyntax, 1, "i 后无数字，e 属语法错误"},
		{"负号后无数字", "i-e", 0, 0,
			nil, bencode.ErrSyntax, 2, "i- 后无数字，e 属语法错误"},
		{"整数内非法字节", "i12x5e", 0, 0,
			nil, bencode.ErrSyntax, 3, "x 非数字非 e"},
		{"加号不允许", "i+1e", 0, 0,
			nil, bencode.ErrSyntax, 1, "+ 非数字非 e"},
	})
}

func TestStringRules(t *testing.T) {
	runRuleCases(t, []ruleCase{
		{"空串", "0:", 0, 0,
			[]any{[]byte{}}, nil, 0, "0: 合法"},
		{"普通串", "3:abc", 0, 0,
			[]any{[]byte("abc")}, nil, 0, "长度前缀加内容"},
		{"长度前导零", "01:a", 0, 0,
			nil, bencode.ErrLenLeadingZero, 1, "紧随前导 0 的数字 1 即违规点"},
		{"长度后非法字节", "2x:ab", 0, 0,
			nil, bencode.ErrSyntax, 1, "x 非数字非冒号"},
		{"长度达上限", "10:aaaaaaaaaa", 10, 0,
			[]any{[]byte("aaaaaaaaaa")}, nil, 0, "长度等于 MaxString 合法"},
		{"长度超上限", "11:aaaaaaaaaaa", 10, 0,
			nil, bencode.ErrLenTooLarge, 1, "第二个数字 1 使长度 11 越过上限 10"},
	})
}

func TestDictKeyRules(t *testing.T) {
	runRuleCases(t, []ruleCase{
		{"重复键", "d1:a1:b1:a1:ce", 0, 0,
			nil, bencode.ErrDuplicateKey, 7, "第二个键 a 与前键相等，取其长度前缀首字节"},
		{"键乱序", "d1:b1:a1:a1:ce", 0, 0,
			nil, bencode.ErrKeyOrder, 7, "键 a 排在前键 b 之后，取其长度前缀首字节"},
		{"前缀键升序", "d1:a0:2:aa0:e", 0, 0,
			[]any{bencode.Dict{
				{Key: []byte("a"), Value: []byte{}},
				{Key: []byte("aa"), Value: []byte{}},
			}}, nil, 0, "较短前缀 a 排在 aa 之前"},
		{"前缀键乱序", "d2:aa0:1:ae", 0, 0,
			nil, bencode.ErrKeyOrder, 7, "aa 之后出现其前缀 a 属乱序"},
		{"空串键最小", "d0:1:x1:a1:ye", 0, 0,
			[]any{bencode.Dict{
				{Key: []byte{}, Value: []byte("x")},
				{Key: []byte("a"), Value: []byte("y")},
			}}, nil, 0, "空串是最小键"},
		{"空串键乱序", "d1:a1:x0:1:ye", 0, 0,
			nil, bencode.ErrKeyOrder, 7, "a 之后出现空串键属乱序"},
		{"键不是字节串", "di1e0:e", 0, 0,
			nil, bencode.ErrKeyNotString, 1, "键位置出现 i，取其首字节"},
		{"键是列表", "dle", 0, 0,
			nil, bencode.ErrKeyNotString, 1, "键位置出现 l，取其首字节"},
		{"键值交替完整", "d1:a1:b1:c1:de", 0, 0,
			[]any{bencode.Dict{
				{Key: []byte("a"), Value: []byte("b")},
				{Key: []byte("c"), Value: []byte("d")},
			}}, nil, 0, "键按字节序严格升序"},
	})
}

func TestDepthRules(t *testing.T) {
	runRuleCases(t, []ruleCase{
		{"深度恰为D", "llleee", 0, 3,
			[]any{[]any{[]any{[]any{}}}}, nil, 0, "三层列表深度为 3，等于 D 合法"},
		{"深度D+1", "lllleeee", 0, 3,
			nil, bencode.ErrDepthExceeded, 3, "第 4 层开括号 l 即违规点"},
		{"字典深度恰为D", "d1:ad1:bd1:c0:eee", 0, 3,
			[]any{bencode.Dict{{Key: []byte("a"), Value: bencode.Dict{
				{Key: []byte("b"), Value: bencode.Dict{
					{Key: []byte("c"), Value: []byte{}},
				}},
			}}}}, nil, 0, "三层字典深度为 3 合法"},
		{"字典深度D+1", "d1:ad1:bd1:cd0:eee", 0, 3,
			nil, bencode.ErrDepthExceeded, 12, "第 4 层开括号 d 即违规点"},
		{"标量不计深度", "li1ei2ee", 0, 1,
			[]any{[]any{int64(1), int64(2)}}, nil, 0, "整数不增加嵌套深度"},
	})
}

func TestTopLevelAndFirstByte(t *testing.T) {
	runRuleCases(t, []ruleCase{
		{"多个顶层值首尾相接", "i1e0:lei-2e", 0, 0,
			[]any{int64(1), []byte{}, []any{}, int64(-2)}, nil, 0,
			"四个顶层值紧邻，每个完成即交付"},
		{"非法首字节", "x", 0, 0,
			nil, bencode.ErrIllegalFirstByte, 0, "x 不能开始任何值"},
		{"顶层孤立e", "e", 0, 0,
			nil, bencode.ErrIllegalFirstByte, 0, "顶层 e 属非法首字节"},
		{"加号首字节", "+1:", 0, 0,
			nil, bencode.ErrIllegalFirstByte, 0, "+ 不能开始任何值"},
		{"字典缺值", "d1:ae", 0, 0,
			nil, bencode.ErrIllegalFirstByte, 4, "键后期待值时出现 e 属非法首字节"},
	})
}

// chunkInputs 覆盖合法、非法、不完整等各类输入。
var chunkInputs = []string{
	"i0e",
	"i-0e",
	"i01e",
	"i9223372036854775807e",
	"i-9223372036854775808e",
	"i9223372036854775808e",
	"i-9223372036854775809e",
	"0:",
	"3:abc",
	"01:a",
	"2x:ab",
	"ie",
	"i-e",
	"i1x",
	"x",
	"+1:",
	"e",
	"d1:a1:b1:a1:ce",
	"d1:b1:a1:a1:ce",
	"d0:0:e",
	"d1:a0:2:aa0:e",
	"d2:aa0:1:ae",
	"di1ee",
	"dle",
	"d1:a",
	"li2ee",
	"i1e0:lei-2e",
	"d1:a1:x0:1:ye",
	"i1ei2eX",
}

// compositions 枚举 n 字节输入的全部 2^(n-1) 种切分。
func compositions(n int, yield func(cuts []int) bool) {
	gaps := n - 1
	if gaps < 0 {
		gaps = 0
	}
	for mask := 0; mask < 1<<gaps; mask++ {
		cuts := []int{0}
		for i := 0; i < gaps; i++ {
			if mask&(1<<i) != 0 {
				cuts = append(cuts, i+1)
			}
		}
		cuts = append(cuts, n)
		if !yield(cuts) {
			return
		}
	}
}

func splitByCuts(s string, cuts []int) [][]byte {
	var chunks [][]byte
	for i := 0; i+1 < len(cuts); i++ {
		chunks = append(chunks, []byte(s[cuts[i]:cuts[i+1]]))
	}
	return chunks
}

// TestChunkingInvariance 同一字节流在任意切分下结果一致。
func TestChunkingInvariance(t *testing.T) {
	for _, input := range []string{"i1e0:lei-2e", "d1:a1:b1:a1:ce", "i-9223372036854775808e3:abc"} {
		ref := runChunks(bencode.NewDecoder(), []byte(input))
		t.Logf("输入=%q 整体解码=%s（判定基准）", input, ref)

		// 所有单切分点
		for k := 0; k <= len(input); k++ {
			got := runChunks(bencode.NewDecoder(), []byte(input[:k]), []byte(input[k:]))
			checkSameResult(t, input, got, ref)
		}
		// 逐字节
		var oneByteChunks [][]byte
		for i := 0; i < len(input); i++ {
			oneByteChunks = append(oneByteChunks, []byte(input[i:i+1]))
		}
		checkSameResult(t, input, runChunks(bencode.NewDecoder(), oneByteChunks...), ref)

		// 穷举全部切分组合（输入较长时跳过，单切分点与逐字节已覆盖）
		if len(input) <= 16 {
			count := 0
			compositions(len(input), func(cuts []int) bool {
				got := runChunks(bencode.NewDecoder(), splitByCuts(input, cuts)...)
				checkSameResult(t, input, got, ref)
				count++
				return true
			})
			t.Logf("输入=%q 全部 %d 种切分结果一致", input, count)
		}
	}
}

// TestAllInputsAllSplitPoints 对每个用例遍历所有单切分点与逐字节切分。
func TestAllInputsAllSplitPoints(t *testing.T) {
	for _, input := range chunkInputs {
		ref := runChunks(bencode.NewDecoder(), []byte(input))
		t.Logf("输入=%q 整体解码=%s（判定基准）", input, ref)
		for k := 0; k <= len(input); k++ {
			got := runChunks(bencode.NewDecoder(), []byte(input[:k]), []byte(input[k:]))
			checkSameResult(t, input, got, ref)
		}
		var oneByteChunks [][]byte
		for i := 0; i < len(input); i++ {
			oneByteChunks = append(oneByteChunks, []byte(input[i:i+1]))
		}
		checkSameResult(t, input, runChunks(bencode.NewDecoder(), oneByteChunks...), ref)
	}
}

// TestAgainstNaive 流式解码器与朴素整体解码器对照。
func TestAgainstNaive(t *testing.T) {
	for _, input := range chunkInputs {
		wantVals, wantConsumed, wantErr := naiveDecode([]byte(input),
			bencode.DefaultMaxString, bencode.DefaultMaxDepth)
		got := runChunks(bencode.NewDecoder(), []byte(input))
		t.Logf("输入=%q 流式=%s 朴素={vals:%v consumed:%d err:%v}",
			input, got, wantVals, wantConsumed, wantErr)
		if !reflect.DeepEqual(got.vals, wantVals) {
			t.Errorf("输入=%q 值序列不一致: 流式=%v 朴素=%v", input, got.vals, wantVals)
		}
		if got.consumed != wantConsumed {
			t.Errorf("输入=%q 消费数不一致: 流式=%d 朴素=%d", input, got.consumed, wantConsumed)
		}
		if !sameError(got.err, wantErr) {
			t.Errorf("输入=%q 错误不一致: 流式=%v 朴素=%v", input, got.err, wantErr)
		}
	}
}

// genValue 生成随机合法值（深度不超过 maxDepth）。
func genValue(r *rand.Rand, depth, maxDepth int) any {
	choice := r.Intn(4)
	if depth >= maxDepth {
		choice = r.Intn(2)
	}
	switch choice {
	case 0:
		switch r.Intn(4) {
		case 0:
			return int64(math.MaxInt64)
		case 1:
			return int64(math.MinInt64)
		default:
			return r.Int63() - r.Int63()
		}
	case 1:
		n := r.Intn(9)
		s := make([]byte, n)
		r.Read(s)
		return s
	case 2:
		n := r.Intn(3)
		l := make([]any, 0, n)
		for i := 0; i < n; i++ {
			l = append(l, genValue(r, depth+1, maxDepth))
		}
		return l
	default:
		n := r.Intn(3)
		keys := make([][]byte, 0, n)
		seen := map[string]bool{}
		for len(keys) < n {
			k := make([]byte, r.Intn(4))
			r.Read(k)
			if seen[string(k)] {
				continue
			}
			seen[string(k)] = true
			keys = append(keys, k)
		}
		sortBytes(keys)
		d := bencode.Dict{}
		for _, k := range keys {
			d = append(d, bencode.Pair{Key: k, Value: genValue(r, depth+1, maxDepth)})
		}
		return d
	}
}

func sortBytes(bs [][]byte) {
	for i := 1; i < len(bs); i++ {
		for j := i; j > 0 && string(bs[j]) < string(bs[j-1]); j-- {
			bs[j], bs[j-1] = bs[j-1], bs[j]
		}
	}
}

// encodeValue 把值编码为规范 bencode。
func encodeValue(v any, buf *[]byte) {
	switch x := v.(type) {
	case int64:
		*buf = append(*buf, 'i')
		*buf = append(*buf, []byte(fmt.Sprintf("%d", x))...)
		*buf = append(*buf, 'e')
	case []byte:
		*buf = append(*buf, []byte(fmt.Sprintf("%d:", len(x)))...)
		*buf = append(*buf, x...)
	case []any:
		*buf = append(*buf, 'l')
		for _, e := range x {
			encodeValue(e, buf)
		}
		*buf = append(*buf, 'e')
	case bencode.Dict:
		*buf = append(*buf, 'd')
		for _, p := range x {
			encodeValue(p.Key, buf)
			encodeValue(p.Value, buf)
		}
		*buf = append(*buf, 'e')
	}
}

// TestRandomAgainstNaive 随机合法值与其变异体的流式/朴素对照。
func TestRandomAgainstNaive(t *testing.T) {
	r := rand.New(rand.NewSource(20261002))
	for i := 0; i < 500; i++ {
		var buf []byte
		var wantVals []any
		n := 1 + r.Intn(3)
		for j := 0; j < n; j++ {
			v := genValue(r, 1, 5)
			wantVals = append(wantVals, v)
			encodeValue(v, &buf)
		}
		mutated := false
		if r.Intn(2) == 0 && len(buf) > 0 {
			buf[r.Intn(len(buf))] = byte(r.Intn(256))
			mutated = true
		}

		naiveVals, naiveConsumed, naiveErr := naiveDecode(buf,
			bencode.DefaultMaxString, bencode.DefaultMaxDepth)
		got := runChunks(bencode.NewDecoder(), buf)
		if !reflect.DeepEqual(got.vals, naiveVals) ||
			got.consumed != naiveConsumed ||
			!sameError(got.err, naiveErr) {
			t.Fatalf("输入=%q 流式=%s 朴素={vals:%v consumed:%d err:%v}",
				buf, got, naiveVals, naiveConsumed, naiveErr)
		}
		if !mutated {
			if got.err != nil || !reflect.DeepEqual(got.vals, wantVals) {
				t.Fatalf("合法输入=%q 应无错解码为 %v，得到 %s", buf, wantVals, got)
			}
		}
		if i < 5 {
			t.Logf("输入=%q 流式=%s 朴素 consumed=%d err=%v（对照一致）",
				buf, got, naiveConsumed, naiveErr)
		}
	}
}

// TestStickyFailure 出错后进入粘滞失败态。
func TestStickyFailure(t *testing.T) {
	dec := bencode.NewDecoder()
	vals, err := dec.Feed([]byte("i1ei2eX"))
	t.Logf("输入=%q 输出 vals=%v err=%v（判定依据：X 为非法首字节，之前两个整数已交付）",
		"i1ei2eX", vals, err)
	if !reflect.DeepEqual(vals, []any{int64(1), int64(2)}) {
		t.Fatalf("出错前已完成值应照常交付，得到 %v", vals)
	}
	if !errors.Is(err, bencode.ErrIllegalFirstByte) {
		t.Fatalf("期望 ErrIllegalFirstByte，得到 %v", err)
	}
	var perr *bencode.Error
	if !errors.As(err, &perr) || perr.Offset != 6 {
		t.Fatalf("错误偏移应为 6，得到 %v", err)
	}
	consumed, buffered := dec.Consumed(), dec.Buffered()
	if consumed != 6 || buffered != 0 {
		t.Fatalf("计数不符: consumed=%d buffered=%d", consumed, buffered)
	}

	for i := 0; i < 3; i++ {
		out, err := dec.Feed([]byte("i3e"))
		if !errors.Is(err, bencode.ErrPoisoned) || out != nil {
			t.Fatalf("粘滞态 Feed 应返回 ErrPoisoned，得到 vals=%v err=%v", out, err)
		}
		if dec.Consumed() != consumed || dec.Buffered() != buffered {
			t.Fatalf("粘滞态计数不应变化: consumed=%d buffered=%d", dec.Consumed(), dec.Buffered())
		}
	}
	if !errors.Is(dec.Err(), bencode.ErrIllegalFirstByte) {
		t.Fatalf("Err() 应返回原始错误，得到 %v", dec.Err())
	}
	var orig *bencode.Error
	if !errors.As(dec.Err(), &orig) || orig.Offset != 6 {
		t.Fatalf("原始错误偏移应为 6，得到 %v", dec.Err())
	}
}

// TestConcurrentAccess Feed 与查询并发调用，结果等价于某个串行顺序。
func TestConcurrentAccess(t *testing.T) {
	input := []byte("i1e2:abd1:x1:yei-3e")
	dec := bencode.NewDecoder()
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = dec.Consumed()
					_ = dec.Buffered()
					_ = dec.Err()
				}
			}
		}()
	}
	var vals []any
	for _, b := range input {
		out, err := dec.Feed([]byte{b})
		if err != nil {
			t.Fatalf("Feed: %v", err)
		}
		vals = append(vals, out...)
	}
	close(stop)
	wg.Wait()

	want := []any{int64(1), []byte("ab"),
		bencode.Dict{{Key: []byte("x"), Value: []byte("y")}}, int64(-3)}
	if !reflect.DeepEqual(vals, want) {
		t.Fatalf("值序列不符: 得到 %v，期望 %v", vals, want)
	}
	if dec.Consumed() != int64(len(input)) || dec.Buffered() != 0 {
		t.Fatalf("计数不符: consumed=%d buffered=%d", dec.Consumed(), dec.Buffered())
	}
	t.Logf("输入=%q 并发逐字节喂入输出 vals=%v consumed=%d（与串行一致）",
		input, vals, dec.Consumed())
}
