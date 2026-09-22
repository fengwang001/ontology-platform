# chunked

HTTP/1.1 分块传输编码（chunked transfer-encoding）的流式解码器。只依赖标准库，
所有状态保存在进程内存中。

## 包结构（依赖单向）

- `internal/hexline`：块大小行解析。十六进制大小（大小写不敏感、允许前导零）
  加零个或多个 `;name=value` / `;name="quoted"` 扩展；扩展整体跳过，引号内的
  `;`、`=` 不是分隔符，`\"` 为转义引号。不依赖其他包。
- `internal/frame`：单个块的边界判定。声明 N 字节就读走恰好 N 字节并强制尾随
  CRLF，支持任意位置切包。依赖 `hexline`。
- `chunked`：对外流式解码器。管理「大小行 → 块数据 → CRLF → 下一块 / 零长块
  → trailer 段 → 空行」状态机，做总量核对、四类上限检查与错误分类。

反向依赖不存在。

## 用法

```go
d := chunked.New() // 或 chunked.NewWithLimits(chunked.Limits{...})
for {
    n, err := d.Write(p) // p 是从网络读到的任意长度片段
    p = p[n:]
    if err != nil { /* 终态错误，见 chunked.Kind */ }
    if d.Done() { break }
}
body := d.Body() // Done 前是已解码部分，Done 后是完整消息体
```

`Write` 可在任意字节边界反复喂入，已消费的字节不会被解释两次；流结束必须调用
`Close()`，消息未完成时返回可区分的不完整错误（大小行中 / 块数据中 / 等 CRLF /
半 CRLF / 等 trailer 空行）。单个 `Decoder` 不支持并发写入，不同实例之间无共享
状态，可各自在不同 goroutine 中使用。

## 演示与测试

```bash
go run ./cmd/demo
go test -count=1 ./...
go test -race -count=1 ./...
gofmt -l .
go vet ./...
```
