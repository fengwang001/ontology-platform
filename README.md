# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## TCP 乱序重组

`tcpreassembly.Receiver` 实现带 D-SACK 的 TCP 接收端重组。所有比较先把 32 位序号转成相对当前 `rcvNxt` 的有符号偏移：`int32(seq-rcvNxt)`，偏移范围为 `-2^31` 到 `2^31-1`；跨回绕段在偏移空间中连续表示，最终块边界再转回绝对 32 位序号。

- 接收窗口为 `[rcvNxt, rcvNxt+W)`。段左侧 `[lo,0)` 是旧数据；右边缘之外的字节直接截去，完全位于窗口外的段不是错误，也不产生 D-SACK。
- 候选区间 `[max(lo,0), min(hi,W))` 中，与处理前已有乱序块重叠的字节是重复字节；其余才是新字节。重复字节与旧数据合并为连续重复区间，D-SACK 只报告起点最小的一段最大连续区间。
- 乱序块互不相交且互不相邻。新字节与已有块重叠或相接都会合并；内容冲突时先复制新字节，再用先到块内容覆盖重叠位置，因此先到者保留。
- 包含新字节且起点为 `rcvNxt` 的合并组件会按序交付，并继续吞掉随后相接的乱序块。填洞后相接的块会随同一组件交付。
- `clk` 只在本段带来至少一个保留下来的乱序新字节时增加；新形成或扩大的乱序块取新的 `clk`，未触及块保持原时间戳。
- 普通 SACK 按 `stamp` 从大到小排序；有 D-SACK 时它固定为第一个块，最多再列 `maxSack-1` 个普通乱序块。
- 块表已满且候选区间包含新字节、起点不是 `rcvNxt`、且不与任何现有块重叠或相接时，整段候选内容丢弃，`Dropped=true`，状态不变但仍返回当前确认。
- `OnSegment`、状态查询和块查询均由读写锁保护，并发结果等价于某种串行重放。非法构造参数或段长会返回 `tcpreassembly.ErrInvalidArgument`，且不改变状态。
- 固定例、32 位回绕、窗口裁剪、块表丢弃、先到者保留、并发竞态，以及 2000 组随机段序列与朴素参考模型的区间和字节内容对照位于 `tcpreassembly/receiver_test.go`。随机测试使用固定随机种子；加 `-v` 时日志会打印每步输入、实际输出、参考输出、块集合和判定依据。

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

TCP 重组本地验证：

```bash
GOCACHE=/tmp/ontology-go-cache go test -v ./tcpreassembly
GOCACHE=/tmp/ontology-go-cache go test -race ./tcpreassembly
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
