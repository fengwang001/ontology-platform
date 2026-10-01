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

`linetable` 提供字节码行号表的增量编码器与解码器：按 pc 递增追加
`(pc, 行号)` 记录，编码成带符号变长增量字节串，并支持按 pc 反查源码行。
所有方法可并发调用，效果等价于某个串行顺序；`Encode` 只读且对同一状态
结果恒定；相同的追加序列重放得到完全相同的字节串与查询结果。

### 追加与合并规则

- 构造时给定指令总数 `N >= 1`（`New(n)`），否则报 `ErrInvalidSize`。
- `Append(pc, line)` 要求 `0 <= pc < N`、行号为正整数、pc 严格大于上一次
  成功追加的 pc，且首条追加的 pc 必须为 0。校验按「pc 越界 → 行非正 →
  pc 不大于上一次 → 首条 pc 不为 0」的次序只报第一个错误
  （`ErrPCOutOfRange` / `ErrNonPositiveLine` / `ErrPCNotIncreasing` /
  `ErrFirstPCNotZero`），被拒绝的追加不改变表与上一次追加的 pc。
- 若新行与表中最后一个条目的行相同则合并：追加成功但不新增条目，只把
  「上一次成功追加的 pc」推进到本次 pc。
- `Line(pc)` 返回起点不大于 pc 的最后一个条目的行；表为空报
  `ErrEmptyTable`，pc 越界报 `ErrPCOutOfRange`（按此次序）。

### 编码格式

`Encode` 把全部条目依次写成两个无符号变长整数：

1. 本条目 pc 减去上一条目 pc 的差值；
2. 本条目行减去上一条目行的差值。

首条目的「上一条目」视为 `(pc 0, 行 0)`。每个有符号差值先做 zigzag
映射（`n >= 0` 映为 `2n`，`n < 0` 映为 `-2n-1`），再按 LEB128 写出：
低 7 位在前，除末字节外每字节高位置 1。

### 解码校验次序

`Decode(data, n)` 按字节顺序扫描，遇到第一处错误即整体拒绝（返回
`nil` 表，不留半成品）：

1. 变长整数级：`ErrVarintTooLong`（超过 10 字节仍未结束）、
   `ErrVarintTruncated`（字节流在续位后截断）、`ErrVarintNonMinimal`
   （多于 1 字节且末字节为 0 的非最短形式）；
2. 条目级（每条目按序）：`ErrFirstPCDeltaNonZero`（首条 pc 差不为 0）、
   `ErrPCDeltaNotPositive`（非首条 pc 差不为正）、`ErrPCBeyondN`
   （累计 pc 不小于 N）、`ErrLineDeltaZero`（非首条行差为 0）、
   `ErrLineNotPositive`（累计行非正）。

对任意合法编码，解码后再编码必得到原字节串。

### 本地验证

```bash
# 运行行号表全部测试（日志打印输入、输出与判定依据）
go test -v ./linetable

# 带竞态检测验证并发等价性
go test -race ./linetable
```

测试覆盖：pc 恰为 `N-1` 与 `N` 的边界、同行合并后仍推进上一次追加的
pc、行号下降的负差值、pc 差 63/64 与行差 ±63/±64、-64/-65 的 zigzag
字节边界、三类变长整数错误、全部条目级错误、与朴素逐 pc 对照表的逐点
比对，以及并发追加/编码/查询的串行等价性。
