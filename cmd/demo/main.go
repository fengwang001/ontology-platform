package main

import (
	"errors"
	"fmt"
	"math"

	"ontology/coalesce"
	"ontology/multipart"
	"ontology/rangespec"
	"ontology/serve"
	"ontology/source"
)

type rec struct {
	name string
	ok   bool
	detail string
}

func main() {
	var rs []rec

	// 1) 三种写法与越界裁剪。
	specs, _ := rangespec.Parse("bytes=90-200,95-,-500")
	ivs, err := coalesce.Normalize(specs, 100)
	ok1 := err == nil && len(ivs) == 2
	rs = append(rs, rec{"三种写法与越界裁剪", ok1, ""})

	// 2) bytes=-0 不可满足且带总长。
	s0, _ := rangespec.Parse("bytes=-0")
	_, e0 := coalesce.Normalize(s0, 100)
	var ue *coalesce.UnsatisfiableError
	rs = append(rs, rec{"bytes=-0 不可满足", errors.As(e0, &ue) && ue.TotalLength == 100, ""})

	// 3) 两类错误可判定，且语法错误带偏移。
	_, esyn := rangespec.Parse("bytes=0")
	var se *rangespec.SyntaxError
	rs = append(rs, rec{"两类错误可判定(偏移)", errors.As(esyn, &se) && se.Offset == 1, ""})

	// 4) 合并前后字节集合相同（这里随机抽样 + 位图）。
	rs = append(rs, rec{"合并前后字节集合相同", setEqualDemo(), ""})

	// 5) 比较次数 n=100 / n=10000 对照。
	c1, c2 := cmpDemo(100), cmpDemo(10000)
	b2 := int64(10000) * int64(math.Ceil(math.Log2(10000)))
	rs = append(rs, rec{"比较次数两组对照", c2 <= b2, fmt.Sprintf("C(100)=%d C(10000)=%d", c1, c2)})

	// 6) 短读补齐。
	data := make([]byte, 60)
	for i := range data {
		data[i] = byte(i*7 + 1)
	}
	fl := source.NewFlaky(source.NewMemory(data))
	fl.MaxChunk, fl.ShortCalls = 3, map[int]bool{0: true, 2: true}
	a := serve.New(serve.Config{})
	e6 := a.Build("bytes=0-39", fl)
	rs = append(rs, rec{"短读补齐", e6 == nil && string(drain(a)) == string(data[:40]), ""})

	// 7) 写出切分点遍历一致。
	rs = append(rs, rec{"写出切分点遍历一致", splitDemo(data), ""})

	// 8) 单区间裸字节（含合并后退化）。
	a8 := buildOne(data)
	_ = a8.Build("bytes=0-9,10-19", source.NewMemory(data))
	rs = append(rs, rec{"单区间裸字节/合并退化", !a8.IsMultipart() && len(a8.Ranges()) == 1, ""})

	// 9) 边界串与内容不冲突。
	fake := append([]byte("--ontologyBR"), make([]byte, 400)...)
	a9 := serve.New(serve.Config{})
	_ = a9.Build("bytes=0-9,30-39", source.NewMemory(append(fake, data...)))
	rs = append(rs, rec{"边界串与内容不冲突", a9.IsMultipart() &&
		!multipart.ContainsBoundary(append(fake, data...), a9.Boundary()), ""})

	// 10) 三类超限彼此可判定且状态不变。
	rs = append(rs, rec{"三类超限", limitsDemo(data), ""})

	// 11) 查询连查两次一致。
	a11 := serve.New(serve.Config{})
	_ = a11.Build("bytes=0-9,20-29", source.NewMemory(data))
	r1, r2 := a11.Ranges(), a11.Ranges()
	eq := len(r1) == len(r2) && a11.TotalSize() == a11.TotalSize() && a11.Written() == a11.Written()
	rs = append(rs, rec{"查询稳定", eq, ""})

	pass := 0
	for _, r := range rs {
		status := "FAIL"
		if r.ok {
			status, pass = "OK", pass+1
		}
		extra := r.detail
		if extra != "" {
			extra = " " + extra
		}
		fmt.Printf("%-4s %s%s\n", status, r.name, extra)
	}
	fmt.Printf("总计 %d/%d OK\n", pass, len(rs))
}
