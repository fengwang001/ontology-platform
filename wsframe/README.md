# wsframe — 服务端 WebSocket 帧解码器

`wsframe` 实现 RFC 6455 服务端入站帧的解码与消息重组状态机。输入可按
任意边界切分喂入（含逐字节），交付的消息/控制帧序列、错误原因与错误
字节偏移均与切分方式无关；`Feed` 可并发调用，结果等价于某种串行顺序。

## 快速使用

```go
d := wsframe.NewDecoder(1 << 20) // MaxMessage = 1 MiB

events, err := d.Feed(chunk)
if err != nil {
    var de *wsframe.DecodeError
    if errors.As(err, &de) {
        log.Printf("protocol error at byte %d: %v", de.Offset, de.Err)
    }
    // err 也可能直接是 ErrClosed / ErrFailed
}
for _, ev := range events {
    switch ev.Kind {
    case wsframe.KindMessage: // ev.Op 为 OpText/OpBinary，ev.Payload 已重组
    case wsframe.KindPing, wsframe.KindPong:
    case wsframe.KindClose:   // 此后 Feed 一律返回 ErrClosed
    }
}
```

`Event.Payload` 始终是已去掩码的独立副本，调用方可长期持有。

## 帧格式

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-------+-+-------------+-------------------------------+
|F|R|R|R| opcode|M| Payload len |    Extended payload length    |
|I|S|S|S|  (4)  |A|     (7)     |             (16/64)           |
|N|V|V|V|       |S|             |   (if payload len==126/127)   |
| |1|2|3|       |K|             |                               |
+-+-+-+-+-------+-+-------------+ - - - - - - - - - - - - - - -+
|     Extended payload length continued, if payload len == 127  |
+ - - - - - - - - - - - - - - - +-------------------------------+
|                               |Masking-key, if MASK set to 1  |
+-------------------------------+-------------------------------+
| Masking-key (continued)       |          Payload Data         |
+-------------------------------- - - - - - - - - - - - - - - -+
```

服务端规则（本包实现的判定）：

- 首字节：FIN(0x80)、RSV1-3(0x70) 必须全为 0；opcode 只接受
  0(continuation)、1(text)、2(binary)、8(close)、9(ping)、10(pong)。
- 次字节：MASK(0x80) 必须置位（服务端只接受掩码帧）；低 7 位长度：
  - `0..125`：即载荷长度；
  - `126`：后接 2 字节大端无符号长度，且值必须 `>= 126`；
  - `127`：后接 8 字节大端无符号长度，最高位必须为 0，且值必须
    `> 65535`。
  以上条件不满足即为「非最小编码」。
- 去掩码：`unmasked[i] = masked[i] XOR masking-key[i mod 4]`。
- 控制帧（8/9/10）必须 FIN=1 且载荷不超过 125 字节。

## 分片与消息重组

- FIN=0 的数据帧（opcode 1/2）开启一条分片消息；此后只能出现
  opcode 0 的延续帧，直到 FIN=1 的延续帧结束消息，交付一个
  `KindMessage`（opcode 取起始帧的 1 或 2，Payload 为全部累计载荷）。
- 控制帧可在分片中途穿插，立即单独交付（`KindPing`/`KindPong`/
  `KindClose`），不打断分片状态。
- 未分片时出现 opcode 0、分片中途出现新的 opcode 1/2 数据帧均为协议错误。
- 累计载荷长度上限为 `MaxMessage`，恰好等于上限允许，超出即报错
  （在长度字段最后一个可判定字节处，无需等待载荷到达）。

## 关闭帧

- 载荷长度必须为 0 或 `>= 2`；长度恰为 1 报错 `ErrCloseLength`。
- `>= 2` 时前两字节为大端关闭码，合法集合：
  `1000..1003`、`1007..1011`、`3000..4999`，其余报 `ErrCloseCode`。
- 关闭帧成功交付后解码器进入「已关闭」态，此后 `Feed` 立即返回
  `ErrClosed`，不消费任何字节、不改变任何计数。

## 错误原因与偏移约定

所有协议违规都返回 `*DecodeError{Offset, Err}`，可用 `errors.As`
取偏移、`errors.Is` 比对原因；已关闭/失败态直接返回哨兵错误
`ErrClosed` / `ErrFailed`（同样可用 `errors.Is` 区分）。

`Offset` 是从该解码器收到的第 0 个字节起算的**绝对字节偏移**，
取该违规「可判定的最早字节」。同一字节多项可判定时，按下列次序报第一个：

1. `ErrRSV`（RSV 非零，偏移 0 起算的首字节）
2. `ErrOpcode`（非法 opcode，首字节）
3. `ErrUnmasked`（未掩码，次字节）
4. `ErrLengthEncoding`（非最小长度编码）
5. `ErrLength64Bit`（64 位长度最高位为 1，扩展长度第 1 字节）
6. `ErrControlFragment`（控制帧 FIN=0，首字节）
7. `ErrControlTooLong`（控制帧载荷 > 125，长度字段末字节）
8. `ErrNewDataFragment`（分片中途出现新数据帧，首字节）
9. `ErrUnexpectedCont`（无分片时出现延续帧，首字节）
10. `ErrMessageTooLarge`（消息超限，长度字段末字节）
11. `ErrCloseLength`（关闭帧载荷 1 字节，该载荷字节）
12. `ErrCloseCode`（关闭码非法，载荷第 2 字节）

注意「最早字节」规则会跨次序裁决：例如 RSV 错误（偏移 0）即使与未掩码
（偏移 1）同时成立，也报 RSV。典型长度相关偏移：

- 16 位形式值 `< 126`：报在扩展长度第 2 字节（帧内偏移 3）；
- 64 位形式最高位为 1：报在扩展长度第 1 字节（帧内偏移 2）；
- 64 位形式值 `<= 65535`：前 6 个扩展字节全零即可判定，报在
  扩展长度第 6 字节（帧内偏移 7）。

出错后进入粘滞「失败」态：此后 `Feed` 一律返回 `ErrFailed`，
不消费字节、不改变偏移与计数。

## 本地验证

```bash
# 常规测试（带详细的输入/输出/判定依据日志）
go test -v ./wsframe

# 竞态检测
go test -race -count=1 ./wsframe

# 全仓测试与静态检查
go test ./...
go vet ./...
gofmt -l .
```

测试要点（见 `decode_test.go`、`testutil_test.go`）：

- 长度 125/126/65535/65536 的编码选择，以及 16/64 位的非最小写法；
- 分片之间穿插 ping/pong，控制帧即时交付；
- 消息大小恰好等于上限与超上限 1 字节（单帧与分片两种）；
- 掩码键跨 `Feed` 边界（键内每个位置切分、逐字节喂入）；
- 每个用例都遍历全部切分点（大载荷采用边界窗口 + 步长 + 随机切分），
  并与整体解码的独立朴素实现 `naiveDecode` 对照事件序列、错误原因与偏移；
- 已关闭/失败粘滞态、并发 `Feed` 的竞态与串行等价性；
- 日志逐条打印输入字节数、参考输出事件、错误原因、偏移与判定结论。
