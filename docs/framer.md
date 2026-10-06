# 反向代理请求分帧判定器

## 范围

`framer.Decoder` 对一条 TCP 连接上的任意字节切片做增量判定，输出：

- `EventHeaders`：方法、目标和 `BodyModeNone` / `BodyModeFixed` / `BodyModeChunked`。
- `EventBody`：本次或连续可合并的消息体字节数；不保存消息体内容。
- `EventEnd`：当前请求完整结束，下一字节立即属于下一个请求。
- `EventReject`：携带互相区分的错误类别，并将连接永久置为关闭态。

## 关键取舍

- 使用单一显式状态机，而不是先拼接后调用普通 HTTP 解析器。所有迁移只依赖当前字节和常量大小的计数器，因此一次送入、逐字节送入和任意边界切分得到同一事件序列。
- 头部阶段缓冲当前请求头部，缓冲区由 `MaxHeaderBytes` 上限约束；头部结束后立即释放。请求行和头行在完整头部上一次性校验，但每个字节只在其所属头部被线性扫描一次，跨整条连接看摊销成本为 O(1)。
- 定长消息体不保存数据，只递减剩余长度。分块消息体不保存块数据，只保存块大小、块剩余量、累计总量和少量数字解析状态。
- 分块大小行只接受十六进制数字和严格 CRLF，不接受分块扩展，因为题面要求块大小为十六进制且未授权扩展语法。
- 尾部头逐行缓冲并立即校验；每一行同样受 `MaxHeaderBytes` 约束，避免无界尾部占用内存。`Content-Length` 与 `Transfer-Encoding` 在尾部中出现时归类为 `trailer`。
- `Decoder.Push` 在实例内用互斥锁串行化。不同连接使用不同实例，互不影响；同一连接并发调用时，锁保证它们等价于某个全局调用顺序。

## 错误优先级

头部读取过程中的即时错误在到达该字节时判定，优先级为：

1. `header_too_large`
2. `null_byte`
3. `bare_line_feed`

头部完整后的语义错误按以下顺序取第一个：

1. `syntax`
2. `length_and_transfer_encoding`
3. `invalid_content_length`
4. `body_too_large`
5. `unsupported_transfer_encoding`

进入分块消息体后，块大小或 CRLF 结构错误为 `chunk_format`，累计消息体超过上限为 `body_too_large`，超过 64 位的块大小为 `chunk_format`。尾部阶段的结构、禁止字段和行上限问题统一且可区分地报 `trailer`。

## 被放弃的方案

- **依赖标准库 HTTP 解析器**：标准库面向服务端请求消费，不直接暴露所有走私歧义和错误优先级，也难以保证任意切分下的精确事件类别一致。
- **跨块等待再批量解析**：会使延迟和单块成本依赖缓冲大小，并需要更复杂的“何时可提交”规则；显式状态机更适合证明切分无关性。
- **保存完整消息体后再验证**：内存随消息体增长，不符合入口代理只判定边界的目标。
- **宽松接受重复或复合传输编码后再规范化**：这正是请求走私的常见歧义来源；实现选择拒绝而非猜测上游或下游会如何解释。
- **接受块扩展或可折叠头**：两者都会增加分帧歧义；本规格没有要求兼容，故严格拒绝。

## 复杂度与内存

- 每个连接的状态字段数量固定；消息体数据从不复制，消息体内存占用为 O(1)。
- 头部缓冲为 O(`MaxHeaderBytes`)，尾部行为 O(`MaxHeaderBytes`)，均不随消息体或连接总字节数增长。
- 除该字节所属头部的一次性摊销解析外，每个字节执行常量次状态判断；不扫描已处理请求，也不扫描已完成的消息体。
- 返回的 `[]Event` 是每次调用独立分配的输出切片，不被 `Decoder` 保留；调用方可安全持有。

## 本地验证

```bash
GOCACHE=/tmp/go-cache go test ./...
GOCACHE=/tmp/go-cache go test -race ./...
GOCACHE=/tmp/go-cache go test -v -run TestRandomEquivalenceAgainstNaiveModel ./framer
GOCACHE=/tmp/go-cache go test -run '^$' -bench=. -benchmem ./framer
GOCACHE=/tmp/go-cache go vet ./...
```

随机等价测试使用固定种子运行 1200 组序列，对每组比较独立朴素模型、一次送入、逐字节送入和随机切分；使用 `-v` 时打印输入、事件输出和判定依据。显式测试覆盖头部边界、错误先后、重复长度、64 位块大小、消息体上限、流水线、拒绝后输入和尾部禁止字段。
