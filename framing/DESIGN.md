# HTTP/1.x 请求分帧判定器 — 设计说明

包路径：`ontology/framing`。目标是在反向代理入口处对同一条 TCP 连接上
持续到达的字节流做**逐请求**的、**与切分无关**的分帧判定，并在发现任何
可能造成请求走私（request smuggling）的歧义时立即关闭该连接。

## 1. 外部接口

```go
p := framing.NewParser(framing.Config{MaxHeaderBytes: 64 * 1024, MaxBodyBytes: 1 << 40})
evs := p.Feed(chunk)          // 任意切分、按到达顺序送入
evs := p.FeedInto(buf, chunk) // 调用方复用事件缓冲，热路径零分配
p.Closed()                    // 是否已拒绝（连接关闭态）
```

事件 `Event`：

- `HeaderComplete{Method, Target, Version, Mode}`
- `Body{N}`（连续的 `Body` 事件在比较与语义上合并）
- `RequestEnd`
- `Reject{Reason}`

连接一旦 `Reject` 即进入关闭态：后续 `Feed` 不产生任何事件，也不会把字节
当作新请求；拒绝之前的事件保持不变。不同连接使用不同 `Parser`，天然隔离。

## 2. 核心模型：单字节状态机

判定器对输入逐字节推进一个显式状态机，不缓冲整个请求、也不依赖任何
“读到下一块再说”的回退逻辑。状态只有：

`Headers → Fixed`，或
`Headers → ChunkSize →(ChunkExt)→ ChunkCR → ChunkData → ChunkDataCR/LF
 → ChunkSize … → TrailerLine/CR → 结束`。

这是“结果与切分无关”的根本原因：**每个字节的处理结果只取决于当前有限
状态和该字节**，块边界在状态机中不可见。任意切分方式（逐字节、随机块、
一次性）喂入，等价于对同一字节序列做同一次状态机运行。

### 头部阶段的即时拒绝

在头部尚未完整前，每读一个字节，按规定顺序即时检查并拒绝：

1. 头部累计长度 `== MaxHeaderBytes` 之后再读到字节 → `HeaderTooLarge`
   （即前 `MaxHeaderBytes` 个字节仍被接受，第 `MaxHeaderBytes+1` 个字节
   触发，严格“超过上限的那一刻”）；
2. 该字节为 `0x00` → `ZeroByte`；
3. 该字节为 `\n` 且前一个字节不是 `\r` → `BareLF`。

三者在同一字节上按“超限 → 零字节 → 裸换行”取先（通过固定的判断顺序实现，
见 `feedHeaderByte`）。收到 `\r\n\r\n`（空行）时头部完整。

### 头部完整后的校验顺序

头部完整后，`finishHeaders` 严格按下列顺序取第一个问题：

1. **语法**：请求行必须恰为 `方法 SP 目标 SP 版本` 三段（恰好两个空格、
   段非空）；版本仅接受 `HTTP/1.1`、`HTTP/1.0`，同时宽容裸写的 `1.1`/`1.0`
   并规范化为 `1.1`/`1.0`；头行必须 `名称:值`，名称非空、不含空白、名称与
   冒号之间无空白；行首为空格/制表符的折叠续行拒绝；
2. **长度与编码并存**：同时出现 `Content-Length` 与 `Transfer-Encoding`
   → `LengthAndTransferEncoding`；
3. **长度头非法**：任一值为空或含非数字字符，或多个值（去掉首尾空白后的
   字面串）不全相同 → `ContentLengthInvalid`；
4. **长度过大**：十进制值超过 `MaxBodyBytes`（含超过 64 位的纯数字值）
   → `LengthTooLarge`；
5. **传输编码不支持**：`Transfer-Encoding` 出现多个头字段，或其（去空白、
   大小写不敏感的）值不是恰好 `chunked` → `TransferEncodingUnsupported`。

头名按小写比较；值在比较前 `TrimSpace`。

### 消息体模式

- 有合法 `Content-Length`：`BodyFixedLength`，恰好消费该数字节；`0` 时
  `HeaderComplete` 后立即 `RequestEnd`。
- `Transfer-Encoding: chunked`：`BodyChunked`。块大小为十六进制，允许
  `;` 后扩展；块大小超过 64 位 → `ChunkFormatInvalid`；本次块或累计消息体
  超过 `MaxBodyBytes` → `LengthTooLarge`。块数据后必须严格 `\r\n`。读到
  `0` 块后进入尾部，尾部以空行结束。
- 两者皆无：`BodyNone`，消息体长度为零。

分块相关错误被刻意区分：

- 块大小行/块结尾/扩展中的非法字节、裸 `\n`、`NUL` → `ChunkFormatInvalid`；
- 尾部头语法错误（空名、名含空白、无冒号、裸 `\n`、`NUL`）
  → `ChunkFormatInvalid`；
- 尾部出现 `Content-Length` 或 `Transfer-Encoding` → `TrailerInvalid`。

请求结束后状态被复位，**下一个字节立即成为新请求的起点**（流水线）。

## 3. 关键取舍

- **只接受明确、唯一的分帧**。`CL` 与 `TE` 并存、多个不一致的 `CL`、
  非 `chunked` 的传输编码等，一律拒绝而不是“猜一个后端会怎么解”。走私
  防护的关键就是消除任何“两个解析器可能理解不同”的输入。
- **重复 `CL` 采用字面串严格比较**（去首尾空白后比较原文），因此
  `5` 与 `05` 判为不一致而拒绝，避免“按数值比较”在不同实现间产生分歧。
- **分块不做宽容解析**：不跳过多余空白、不接受裸 `\n`、不忽略尾部里的
  `CL/TE`。这比 RFC 的最小实现更严格，但正符合入口拒绝歧义的目标。
- **状态机而非缓冲/回溯**：保证切分无关与 O(1) 每字节开销；不需要保存
  消息体，只保存 `fixedLeft/chunkLeft/bodySeen` 等机器字计数。
- **事件在块边界刷出、相邻 `Body` 合并**：允许按块即时产出，同时规格只
  要求“合并连续 body 字节数后”一致，天然屏蔽块边界差异。

## 4. 被放弃的方案

- **复用 `net/http` 的 `ReadRequest`**：它会自行缓冲、容忍多种走私歧义
  （如重复 `CL`、某些 `CL,TE` 组合）且不暴露“拒绝的确切字节位置/类别”，
  无法满足即时、可区分、切分无关的要求。
- **先缓存整块再用正则/字符串切分**：内存随消息体增长，且很难表达
  “读到第 N 个字节的那一刻拒绝”，被放弃。
- **按数值比较重复 `Content-Length`**：不同代理对前导零处理不一，被放弃，
  改用字面串严格一致。
- **用 `big.Int` 处理长度**：纯数字超长只需区分“非法 / 过大”，手写饱和
  十进制累加即可，避免引入额外开销。

## 5. 内存与时间复杂度

- 每字节处理为常数次比较/计数更新，不随连接已处理字节数或请求数增长。
- 唯一的字节缓冲是 `hdrBuf` 与 `lineBuf`，都被 `MaxHeaderBytes` 封顶；
  消息体**不落任何缓冲**，只做计数，因此内存不随消息体长度增长。
- 请求结束时复用同一底层缓冲（`hdrBuf[:0]`、`lineBuf[:0]`），稳态不扩容。
- 并发：`Parser` 内部用容量 1 的通道做互斥；对同一连接的并发 `Feed`
  等价于某个串行顺序，不同连接互不影响。热路径可用 `FeedInto` 复用事件
  缓冲，做到 0 次堆分配。

## 6. 本地验证方法

```bash
export PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache
gofmt -l .
go vet ./...
go test ./...
go test -race ./...                         # 竞态检测
go test ./framing/ -run TestDifferential -v # 1200 组差分（FRAMING_ITERS 可调）
go test ./framing/ -bench . -benchmem       # 吞吐与分配
```

- `TestDifferential` 用一个**独立、朴素的整缓冲参考实现**（`naive_test.go`）
  作为对照，对 1200 组以上随机字节流，分别用“一次性 / 逐字节 / 两种随机
  切分”喂入真实判定器，比较合并 body 后的完整事件序列与拒绝类别。
- 前 25 组的输入、输出与判定依据写入 `framing/differential.log`。
- `TestSteadyStateZeroAlloc` 证明 1 KiB 与 1 MiB 消息体在稳态 body 转换上
  均为 0 次分配且不随长度变化；`BenchmarkBodySteadyState` 直接显示
  `0 B/op, 0 allocs/op`。
