# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## cobs：COBS 流式分帧器

`cobs` 包实现连续开销字节填充（COBS）的规范编码与流式分帧解码。

### 块与码的规则

- 数据被切成若干块，每块是至多 254 个非零字节，前缀一个码字节，码 = 块长 + 1（1 到 255）。
- 码 < 255：块后隐含一个 `0x00`；码 = 255（0xFF）：块恰有 254 个非零字节，后面没有隐含的 `0x00`。
- 规范编码：从头取一段非零字节（至多 254 个）。取满 254 个则输出 `FF` 加该段并继续（不消耗 `0x00`）；不足 254 个则输出「长度加 1」加该段——若数据到此结束则完成，否则消耗紧随其后的一个 `0x00` 并继续；若该 `0x00` 恰是数据最后一个字节，还要再输出一个码 `01` 作为最后的空块。
- 推论：空数据编码为 `01`；数据 `00` 编码为 `01 01`；恰好 254 个非零字节编码为 `FF` 加这 254 字节，不带尾部的 `01`。

### 帧与规范形式

- 帧以 `0x00` 分隔：`Encoder.Encode` 输出每帧编码后接一个 `0x00`；`Decoder.Write` 流式接收字节，遇到 `0x00` 即结束一帧。连续的 `0x00` 构成空帧，忽略但计入 `Stats.Empties`。
- 解码按码读取：块内字节必须非零；码 < 255 且块后仍有字节时才输出一个 `0x00`。解码结果再按规范编码必须逐字节等于收到的帧，否则为非规范（如 `FF` 块后多余的 `01`）。
- 帧级拒绝按顺序只报第一个原因，均可 `errors.Is` 区分并分别计数：
  1. `ErrFrameTooLong`：相邻两个 `0x00` 之间的编码字节数超过 `Decoder.MaxFrame`，丢弃该帧直到下一个 `0x00` 再同步；
  2. `ErrTruncated`：块被帧末截断；
  3. `ErrNonCanonical`：帧不规范。
- 出错后解码继续处理后续帧而不失败；`Encoder.Encode` 拒绝超过 `MaxData` 的数据（`ErrDataTooLarge`），被拒绝时不输出任何字节、不改变计数。
- `Encoder` 与 `Decoder` 均可并发调用（内部加锁，等价于某个串行顺序）；字节流按任意切分（含逐字节）喂给解码器，交付的帧序列与各类计数完全相同。

### 本地验证

```bash
go test ./cobs/            # 边界、非规范、切分无关、随机往返对照
go test -race -v ./cobs/   # 竞态检测 + 打印输入/输出/判定依据日志
```

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
