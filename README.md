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

## COBS 流式分帧

`cobs` 包实现连续开销字节填充（Consistent Overhead Byte Stuffing）：

- 数据被切成至多 254 个非零字节的块；每块前缀一个码字节，码值为块长加 1，范围是 1 到 255。
- 码值小于 255 时，块后隐含一个原始 `0x00`；码值 255 时，块恰好为 254 个非零字节，块后不隐含 `0x00`。
- 规范编码从头取非零串：取满 254 个就输出 `FF + 254 字节` 且不消耗零；不足 254 个就输出“长度加 1 + 块”，若后面还有零则消耗一个零继续；若该零是数据末字节，再输出码 `01` 表示最后的空块。
- 空数据编码为 `01`；单字节数据 `00` 编码为 `01 01`；恰好 254 个非零字节编码为 `FF + 254 字节`，不会追加尾部 `01`。
- 帧使用 `0x00` 分隔。`Encoder.Encode` 返回规范 COBS 数据和末尾分隔符；`Decoder.Write` 可接收任意切分的字节流，每遇到一个 `0x00` 就完成一帧。
- 连续的两个 `0x00` 构成空帧；这类帧不会交付 payload，但会计入 `Empties`。编码后的空数据帧 `01 00` 是有效帧，会交付空 payload。
- 解码会校验块内没有零，并把解出的数据重新规范编码；结果必须与收到的帧逐字节一致，否则判为 `ErrNonCanonical`。

错误按如下优先级只记录第一个：

1. 相邻分隔符之间的帧长超过 `MaxFrame`：记录 `FrameTooLongs`，原因可用 `errors.Is(err, cobs.ErrFrameTooLong)` 识别；解码器丢弃到下一个 `0x00` 后再同步。
2. 码字节声明的块被帧末截断：记录 `Truncated`，原因为 `cobs.ErrTruncatedBlock`。
3. 其他无法再编码为同一帧的输入：记录 `NonCanonical`，原因为 `cobs.ErrNonCanonical`。

单帧错误不会终止流处理；下一帧继续解析。编码数据超过 `MaxData` 时返回 `cobs.ErrDataTooLong`，不返回任何字节，也不改变计数。`Encoder` 和 `Decoder` 的方法均可并发调用，回调交付顺序等价于某次串行 `Write` 顺序；相同输入重放产生相同输出。

本地验证：

```bash
# 单元测试
go test ./cobs

# 覆盖边界、随机数据、朴素实现对照，并打印输入/输出/判定依据
go test -v ./cobs

# 并发安全
go test -race ./cobs

# 静态检查
go vet ./...
```
