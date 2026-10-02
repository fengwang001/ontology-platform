# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`/usr/local/go/bin/go version` 确认；若工具链已在 `PATH`，可直接使用 `go`）

## TCP 乱序重组与 D-SACK

`tcprecv` 包实现可确定重放的 TCP 接收端重组器：

- 构造：`New(rcvNxt uint32, window, maxSack, maxBlocks int)`，其中窗口为 `1..2^30`、`maxSack` 为 `1..4`、`maxBlocks` 为 `1..64`。
- 段处理：`OnSegment(seq uint32, data []byte) (Result, error)`，段长必须为 `1..65535`；非法参数返回 `ErrInvalidArgument` 且不改变状态。
- 查询：`RcvNxt()`、`Blocks()`、`DeliveredData()` 与段处理共用同一把读写锁，并发结果等价于某个串行顺序。

### 序号偏移与 32 位回绕

所有位置先转换为相对当前 `rcvNxt` 的有符号 32 位偏移：

```go
offset := int64(int32(seq - rcvNxt))
```

偏移范围为 `-2^31..2^31-1`；负数表示在当前 `rcvNxt` 左侧。窗口固定长度为 `W`，候选区间为：

```text
C = [max(offset, 0), min(offset+len(data), W))
```

因此跨 `0xFFFFFFFF -> 0` 的段和块仍用普通模 2^32 绝对序号表示；内部合并、比较和跨窗口移动使用有符号偏移。

### 裁剪、重复与先到者保留

- `[lo,0)` 是旧数据，可形成 D-SACK，但不会被再次交付。
- `hi > W` 的字节直接裁剪；整段在窗口右侧之外不产生 D-SACK，也不是错误。
- 候选区间中已属于乱序块的字节是重复字节；新字节与旧内容重叠时只写入未覆盖位置，旧块内容保留。
- 候选非空、包含新字节、既不接触 `rcvNxt` 也不接触任何块，并且已有 `maxBlocks` 个块时，整段丢弃；返回 `Dropped=true`，状态不变。
- 与块重叠或相邻（相接）的候选会合并，保持块互不相交、互不相邻。

### 按序交付、stamp 与 SACK

- 候选从偏移 0 开始时按序处理，并自动链接随后首尾相接的乱序块；`rcvNxt` 前进连续前缀长度，被完全交付的块删除。
- 只有段带来至少一个未被按序交付吞掉的新字节时，`clk` 才递增；新块或扩大/合并后的块取新 `clk`，未触及块的 stamp 不变。
- 普通 SACK 按 stamp 从大到小排列，最近修改的块优先。
- D-SACK 使用处理前状态中的重复字节，先合并旧数据与块覆盖区间，取起点最小的一段最大连续重复区间，放在 SACK 第一位；此时最多再列 `maxSack-1` 个乱序块，`maxSack=1` 时只返回 D-SACK。
- D-SACK 块的 `Stamp=0` 且不携带 `Data`；乱序块的 `Data` 对齐块的绝对 `Start`。

返回值字段：

- `AckNo`：处理后的 `rcvNxt`。
- `Delivered` 与 `DeliveredData`：本次按序交付的字节数和字节内容。
- `Sack`：D-SACK（如有）加最近优先的乱序块。
- `Dropped`：是否因块表满且与现有状态不相接而被拒绝。

### 本地验证

确定例覆盖规范中的 `rcvNxt=1000, W=10000, maxSack=3, maxBlocks=8` 序列、部分重叠先到者保留、跨回绕块与链式交付、块满丢弃/合并、窗口右边缘裁剪和非法参数：

```bash
/usr/local/go/bin/go test -v ./tcprecv
```

2000 组固定随机种子的测试用逐字节集合朴素模型模拟窗口、块、stamp、D-SACK、交付和丢弃；`-v` 会打印每组的输入、输出及与朴素模型的判定依据：

```bash
/usr/local/go/bin/go test -run TestRandomByteSetSimulation2000 -v ./tcprecv
```

并发与竞态检测：

```bash
/usr/local/go/bin/go test -race ./tcprecv
```

全量检查：

```bash
/usr/local/go/bin/gofmt -w .
/usr/local/go/bin/go test ./...
/usr/local/go/bin/go vet ./...
```

## 运行

```bash
# 拉取依赖
/usr/local/go/bin/go mod tidy

# 直接运行
/usr/local/go/bin/go run ./cmd/server

# 编译后运行
/usr/local/go/bin/go build -o bin/server ./cmd/server
./bin/server
```

## 覆盖率

```bash
/usr/local/go/bin/go test -coverprofile=coverage.out ./...
/usr/local/go/bin/go tool cover -html=coverage.out
```
