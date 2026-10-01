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

## 字节码行号表（`linetable` 包）

`linetable.LineTable` 按 pc 递增追加 `(pc, 行号)` 记录，编码为带符号变长增量字节串，
支持按 pc 反查源码行。入口：

- `linetable.New(N int)`：给定指令总长（N ≥ 1），否则返回 `ErrInvalidN`。
- `(*LineTable).Append(pc, line int)`：pc ∈ [0, N−1]、line ≥ 1，且 pc 严格大于
  上一次成功追加的 pc，首条 pc 必须为 0。
- `(*LineTable).LineAt(pc int)`：返回起点不大于 pc 的最后一个条目的行号。
- `(*LineTable).Encode() []byte`：只读编码；`linetable.Decode(N, data)` 校验并还原。

### 同行合并规则

若新条目的行号与表中最后一个条目相同，则**合并**：追加成功、不新增条目，
只把「上一次成功追加的 pc」推进到本次 pc。因此后续追加仍须严格大于该 pc。

### Zigzag 与变长编码

每个条目依次写两个无符号变长整数：

1. pc 差 = 本条目 pc − 上一条目 pc（首条上一条目视为 pc 0）；
2. 行差 = 本行号 − 上一条目行号（首条上一条目视为行 0）。

差值先做 zigzag 映射：`n ≥ 0 → 2n`；`n < 0 → −2n − 1`
（例：`63→126` 一字节，`64→128` 两字节；`−64→127` 一字节，`−65→129` 两字节），
再按 LEB128 写出：低 7 位在前，非末字节高位置 1。首条 pc 差必须为 0；
非首条 pc 差必须为正；非首条行差不得为 0（行号允许下降，累计行号必须保持正数）。

### 追加 / 查询校验次序

- 追加只报第一个错误：`ErrPCOutOfRange` → `ErrLineNotPositive` →
  （非空表）`ErrPCNotIncreasing` / （空表）`ErrFirstPCNotZero`。
  被拒绝的追加不改变条目表与上一次追加的 pc。
- 查询先报 `ErrEmpty`（表为空），再报 `ErrPCOutOfRange`。

### 解码校验次序

`Decode` 先在独立副本上构建，全部通过后才返回；失败返回可区分错误且不留半成品：

1. 字节级（按字节顺序遇到第一处即报）：
   `ErrVarintTooLong`（超过 10 字节仍未结束，或第 10 字节超过 `0x01` 溢出 uint64）
   → `ErrVarintTruncated`（续位后字节流截断）
   → `ErrVarintNonCanonical`（多于 1 字节且末字节为 0 的非最短形式）；
2. 条目级（每读完两个变长整数）：
   `ErrFirstPCDeltaNotZero` → `ErrPCDeltaNotPositive` → `ErrPCExceedsN`
   （累计 pc ≥ N）→ `ErrLineDeltaZero` → `ErrLineNotCumulativePositive`。

空字节串合法，表示空表。对任意合法编码，解码后重新编码得到原字节串。
所有方法可并发调用：内部使用 `sync.RWMutex`，编码只读，同一追加序列重放得到
完全相同的字节串与查询结果。

### 本地验证

```bash
# 全部测试（含朴素逐 pc 对照、重放恒等、单写多读竞态检测）
go test -race -v ./linetable/

# 全量测试与检查
go test ./...
go vet ./...
gofmt -l .
```

测试日志（`-v`）逐条打印追加/查询/解码的输入、输出与判定依据，
覆盖 pc 恰为 N−1 与 N、同行合并推进、行号下降负差、
pc 差 63/64、行差 +63/+64/−64/−65 字节边界，
以及三类变长整数字节错误与全部条目级错误。
