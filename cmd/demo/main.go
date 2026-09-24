package main

import (
	"bytes"
	"fmt"
	"os"

	"ontology/qp"
)

func report(name string, ok bool) bool {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
	}
	return ok
}

func main() {
	pass := true

	ws := bytes.Equal(qp.Encode([]byte("a \n")), []byte("a=20\r\n")) &&
		bytes.Equal(qp.Encode([]byte("a b")), []byte("a b")) &&
		bytes.Equal(qp.Encode([]byte("a ")), []byte("a=20"))
	pass = report("行末空白三个样例", ws) && pass

	pass = report("=XX 不被拆开", false) && pass
	pass = report("76 字符上限（软换行 = 计入）", false) && pass
	pass = report("五类错误可区分且偏移正确", false) && pass
	pass = report("所有切分点逐字节一致", false) && pass
	pass = report("往返 Decode(Encode(x))==N(x)", false) && pass
	pass = report("最小转义（幂等）", false) && pass
	pass = report("检查计数 <= 2*len", false) && pass

	if pass {
		fmt.Println("总计: 8/8 OK")
	} else {
		fmt.Println("总计: 存在 FAIL")
		os.Exit(1)
	}
}
