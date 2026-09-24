package main

import (
	"fmt"
	"ontology/field"
	"ontology/stream"
)

func report(ok bool, name string, fails *int) {
	if ok {
		fmt.Println("OK  ", name)
	} else {
		*fails++
		fmt.Println("FAIL", name)
	}
}

func main() {
	fails := 0

	collect := func(input string, chunk int) []stream.Event {
		var got []stream.Event
		p := stream.New(0, 0, func(e stream.Event) { got = append(got, e) })
		for i := 0; i < len(input); i += chunk {
			end := i + chunk
			if end > len(input) {
				end = len(input)
			}
			if err := p.Feed([]byte(input[i:end])); err != nil {
				return nil
			}
		}
		return got
	}

	three := func(sep string) bool {
		ev := collect("data: a"+sep+"data: b"+sep+sep, 1)
		return len(ev) == 1 && ev[0].Data == "a\nb"
	}
	report(three("\n") && three("\r\n") && three("\r"), "三种行尾", &fails)
	report(len(collect("data: x\r\n\r\n", 2)) == 1, "CRLF 不算两空行", &fails)

	split := []string{"data: x\r", "\ny\n\n"}
	var cross []stream.Event
	cp := stream.New(0, 0, func(e stream.Event) { cross = append(cross, e) })
	cp.Feed([]byte(split[0]))
	cp.Feed([]byte(split[1]))
	report(len(cross) == 1 && cross[0].Data == "x", "块边界落在 CRLF 中间", &fails)

	f1, f2, f3, f4, f5, f6 := field.Parse("data: x"), field.Parse("data:x"),
		field.Parse("data:  x"), field.Parse("data:"), field.Parse("data"), field.Parse(":c")
	report(f1.Name == "data" && f1.Value == "x" && f2.Value == "x" &&
		f3.Value == " x" && f4.Value == "" && f5.Name == "data" && f5.Value == "" &&
		f6.Comment, "冒号后空格规则", &fails)

	in := "data: a\n\n:ping\n\ndata:b\n\n"
	ref := collect(in, len(in))
	splitOK := true
	for n := 1; n <= len(in); n++ {
		ev := collect(in, n)
		if len(ev) != len(ref) {
			splitOK = false
			continue
		}
		for i := range ev {
			if ev[i] != ref[i] {
				splitOK = false
			}
		}
	}
	report(splitOK && len(ref) == 2, "切分点遍历一致", &fails)

	report(collect("data:a\r\ndata:b\r\n\r\n", 1)[0].Data == "a\nb", "多 data 拼接", &fails)
	report(len(collect(":only a comment\n\nunknown:x\n\n", 1)) == 0, "注释不派发", &fails)

	idP := stream.New(0, 0, func(stream.Event) {})
	idP.Feed([]byte("id: 1\n\nid: a\x00b\ndata:x\n\n"))
	report(idP.LastEventID() == "1", "id 含 NUL 忽略", &fails)

	rp := stream.New(0, 0, func(stream.Event) {})
	rp.Feed([]byte("retry: 500\n\nretry: nope\n\n"))
	report(rp.Retry() == 500, "非法 retry 不改状态", &fails)

	var bdata []string
	bp2 := stream.New(0, 0, func(ev stream.Event) { bdata = append(bdata, ev.Data) })
	bp2.Feed([]byte("\xEF\xBB\xBFdata:bom\n\ndata:\xEF\xBB\xBFmid\n\n"))
	report(bdata[0] == "bom" && bdata[1] == "\xEF\xBB\xBFmid", "BOM 只在流首", &fails)

	var before, after string
	lp := stream.New(3, 0, func(stream.Event) {})
	lp.Feed([]byte("id:z\n\n"))
	before = lp.LastEventID()
	errL := lp.Feed([]byte("data:abcd\n\n"))
	after = lp.LastEventID()
	dp := stream.New(0, 4, func(stream.Event) {})
	dp.Feed([]byte("id:z\n\n"))
	errD := dp.Feed([]byte("data:ab\ndata:cd\n\n"))
	report(errL == stream.ErrLineTooLong && errD == stream.ErrDataTooLong &&
		errL != errD && before == after && dp.LastEventID() == "z", "两类超限", &fails)

	dispatched := false
	tp := stream.New(0, 0, func(stream.Event) { dispatched = true })
	tp.Feed([]byte("data:no-blank-line"))
	report(!dispatched, "末尾不自动派发", &fails)

	report(tp.LastEventID() == tp.LastEventID() && rp.Retry() == rp.Retry(), "连查一致", &fails)
	if fails == 0 {
		fmt.Println("总计: 全部 OK")
	} else {
		fmt.Printf("总计: %d FAIL\n", fails)
	}
}
