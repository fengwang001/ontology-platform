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

## HDLC 位流编解码

实现在 `hdlc/` 包：

- `hdlc.NewEncoder(writer, maxPayload)`：有状态流式编码器。
- `hdlc.NewDecoder()`：线程安全的流式解码器，`Write` 返回本次输入中完整交付的帧。
- `hdlc.Stats()`：返回成功帧、中止与三类丢帧计数。

### 位序、FCS 与填充

- 每个字节按低位到高位发送。
- 帧内容为载荷后接 2 字节 FCS，FCS 低字节在前。
- FCS 使用 CRC-16/X-25：多项式 `0x1021` 的反射形式 `0x8408`，初值 `0xFFFF`，结果取反；`123456789` 的校验值为 `0x906E`。
- 内容位流中每出现连续 5 个 `1`，后面插入一个 `0`；内容以 5 个 `1` 结尾时也插入。
- 标志固定为低位在先的 `01111110`（字节 `0x7E`）。

### 标志、空闲与中止

- 第一帧前输出一个起始标志；每个结束标志同时作为下一帧起始标志，相邻帧之间只有一个标志。
- `Flush` 将不足一字节的缓冲用全 `1` 补齐；补齐后下一帧重新输出起始标志。
- 解码器只有在最近完整 8 位等于 `01111110` 时识别标志；流开头的前导 `1` 不能充当标志前的 `0`。
- 相邻标志允许共享中间的一个 `0`，例如 `011111101111110` 表示两个标志，其间内容为零位；零位内容按空闲忽略且不计数。
- 帧内连续 7 个物理 `1` 表示中止，丢弃当前帧、`Aborts` 加一并重新搜索标志；`Flush` 恰好补出 7 个 `1` 时也按同一规则处理。
- 结束标志到达时按顺序只报告第一个错误：内容位数不是 8 的倍数（`AlignmentErrors`）、内容少于 3 字节（`ShortFrames`）、FCS 不符（`FCSErrors`）。
- 编码器拒绝空载荷或超过 `MaxPayload` 的载荷；拒绝时不输出任何位，也不改变共享标志状态与位缓冲。

### 本地验证

```bash
# 全量测试
GOCACHE=/tmp/go-cache-ontology go test ./...

# 定点边界、并发和随机逐位朴素对照
GOCACHE=/tmp/go-cache-ontology go test -v ./hdlc

# 竞态检测
GOCACHE=/tmp/go-cache-ontology go test -race ./...

go vet ./...
gofmt -w hdlc
```

随机测试使用固定种子重放，并按从逐字节到整包的所有切分方式喂给解码器；每轮对照独立的逐位朴素扫描结果，校验交付帧、中止数和三类丢帧计数完全一致。
