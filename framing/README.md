# framing — HTTP/1.x 反向代理入口分帧判定器

对同一连接上任意切分到达的字节流，逐请求判定头部合法性与消息体边界，
拒绝所有可能引起请求走私的歧义输入。详细设计见 [DESIGN.md](DESIGN.md)。

## 用法

```go
package main

import (
	"fmt"

	"ontology/framing"
)

func main() {
	p := framing.NewParser(framing.Config{
		MaxHeaderBytes: 64 * 1024,
		MaxBodyBytes:   1 << 30,
	})

	chunks := [][]byte{
		[]byte("POST /submit HTTP/1.1\r\nContent-Len"),
		[]byte("gth: 5\r\n\r\nhel"),
		[]byte("loGET /next HTTP/1.1\r\n\r\n"), // 流水线紧接下一请求
	}

	var evs []framing.Event
	for _, c := range chunks {
		evs = p.FeedInto(evs, c)
	}
	for _, e := range evs {
		switch e.Kind {
		case framing.EventHeaderComplete:
			fmt.Printf("header %s %s mode=%d\n", e.Method, e.Target, e.Mode)
		case framing.EventBody:
			fmt.Printf("body %d bytes\n", e.N)
		case framing.EventRequestEnd:
			fmt.Println("request end")
		case framing.EventReject:
			fmt.Println("rejected:", e.Reason)
		}
	}
}
```

事件：`HeaderComplete`（方法、目标、版本、消息体模式）、`Body`（字节数，
相邻可合并）、`RequestEnd`、`Reject`（可区分的错误类别）。

错误类别：`header_too_large`、`zero_byte`、`bare_lf`、`syntax`、
`length_and_transfer_encoding`、`content_length_invalid`、`length_too_large`、
`transfer_encoding_unsupported`、`chunk_format_invalid`、`trailer_invalid`。

## 保证

- **切分无关**：同一字节序列无论如何切块，合并连续 `Body` 后的事件序列与
  拒绝类别完全相同（差分测试对一次性 / 逐字节 / 随机切分三方比对）。
- **即时关闭**：一旦拒绝，该连接后续字节不产生任何事件。
- **有界资源**：缓冲由头部上限封顶，消息体不落缓冲；每字节 O(1)，稳态
  body 路径 0 堆分配，内存不随消息体或请求数增长。
- **并发**：不同连接独立；同一连接的并发 `Feed` 被串行化，等价于某种串行
  顺序；通过 `-race`。

## 测试

```bash
export PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache
go test ./...
go test -race ./...
go test ./framing/ -run TestDifferential -v   # 1200+ 随机序列对朴素模型
go test ./framing/ -bench . -benchmem
```

差分样例的输入/输出/判定依据日志：`framing/differential.log`。
