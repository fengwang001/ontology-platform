# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## dnsname：DNS 报文名字压缩

`dnsname` 包实现 RFC 1035 第 4.1.4 节的名字压缩编码器（`Encoder`）与解码器（`Decode`），同一报文内重复后缀只写一次。

### 登记与命中规则

- 构造器从起始偏移 `base`（默认 12）开始追加；`Write(labels)` 返回名字的起点偏移。
- 写入名字时，从整名起逐次去掉最前一个标签，依次检查每个后缀是否已登记（按 ASCII 不分大小写比较）；命中的第一个（最长）后缀，写出尚未覆盖的前缀标签后接两字节指针（高两位 `11`，低 14 位为距报文起始的偏移）并结束；整名命中时只写 2 字节指针。
- 每次写入中未被指针覆盖的各后缀，在写出后登记其起点偏移；起点偏移大于 16383 的后缀不登记。
- 根名字（零个标签）只写一个 `0` 字节，既不压缩也不登记。
- 被拒绝的写入（空标签、标签超 63 字节、名字线格式含结尾超 255 字节、报文总长将超 65535）不追加任何字节也不登记任何后缀。

### 大小写保真约定

后缀比较按 ASCII 不分大小写，但被命中的后缀在解码时读到的是**首次写入时**的大小写，而不是后续传入的大小写（指针直接引用首次写出的字节）。

### 指针方向规则（解码）

`Decode(msg, off, base)` 返回标签序列与消费字节数（遇到指针时消费到指针的两个字节为止），按读取顺序只报第一个错误，可用 `errors.Is` 区分：

- `ErrReservedBits`：标签长度字节高两位为 01 或 10；
- `ErrTruncated`：标签或指针被报文末尾截断；
- `ErrPointerNotBackward`：指针目标不小于该指针自身的起始偏移（含指向自身）；
- `ErrPointerBeforeBase`：指针目标小于 `base`；
- `ErrNameTooLong`：展开后总长超过 255。

指针指向另一个指针是合法的，但每一跳都要满足同样的方向规则（目标严格小于自身指针偏移且不小于 `base`），因此指针链必然终止。

### 并发与确定性

`Encoder` 可被并发调用，结果等价于某个串行顺序，登记表与已写字节始终一致；相同写入序列重放得到逐字节相同的报文；`Decode` 为只读操作，可并发作用于同一报文。

### 本地验证

```bash
go test ./dnsname            # 单元测试
go test -race -v ./dnsname   # 竞态检测 + 打印输入/输出/判定依据
```
