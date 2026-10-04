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

## tsblock：Gorilla 风格时间序列压缩块

`tsblock` 包把 `(时间戳 int64, 值 float64)` 样本压缩成位流，并提供无计数解码。

### 位流约定

- 高位在前：每个字节从最高位写起，多位值从最高位写起。
- `Bytes()` 返回末尾补 0 到整字节的副本；`Bits()` 返回有效位数；`Len()` 返回样本数。
- 块头为 `start` 的 64 位补码；`New(start, maxBytes)` 要求 `13 ≤ maxBytes ≤ 1048576`，否则 `ErrParam`。
- 所有方法可并发调用，效果等价于某个串行顺序；相同操作序列重放得到完全相同的位流。

### 时间戳编码

- 第一个样本：`d0 = t − start` 须在 `[0, 15359]`（`t < start` 也报 `ErrDelta`），写 14 位，再写值的 64 位。
- 后续样本：`t` 不得小于上一个 `t`（可相等，否则 `ErrOrder`）；`d = t − 上一t`，`dod = d − 上一d`。
  `d`/`dod` 溢出 int64 或 `dod` 超出 int32 范围报 `ErrDelta`。`dod` 按首个成立的区间写：
  `0` → `0`；`[−63,64]` → `10`+7 位；`[−255,256]` → `110`+9 位；`[−2047,2048]` → `1110`+12 位；
  其余 → `1111`+32 位。k 位值写 dod 的低 k 位补码；解码读出 `u > 2^(k−1)` 时减 `2^k`。

### 值编码

- `x = 本值位模式 XOR 上一值位模式`（按 IEEE754 位模式，`±0.0`、各 NaN 位模式互不相同）。
- `x = 0` 写 `0`；否则 `lz = min(31, 前导零)`，`tz = 尾部零`。
- 已有窗口 `(pl, pt)` 且 `lz ≥ pl`、`tz ≥ pt` 且复用（`2+(64−pl−pt)` 位）不比重开
  （`13+(64−lz−tz)` 位）更长时，写 `10` + `x>>pt` 的低 `64−pl−pt` 位（相等时复用）；
  否则写 `11` + `lz`（5 位）+ 有效长度 `64−lz−tz`（6 位，64 记为 0）+ `x>>tz` 的有效位，
  并把窗口置为 `(lz, tz)`。`x=0` 与复用都不改变窗口。

### 容量与封口

- 追加样本后的位数加 36 必须不超过 `maxBytes×8`，否则 `ErrFull`（为结束标记预留空间，
  故 `Seal` 永不失败）；被拒绝的追加不改变任何状态。
- `Seal()` 写结束标记 `1111` + 32 个 0 位（共 36 位）；重复 `Seal` 或封口后 `Append` 报 `ErrSealed`。
  错误次序：`ErrSealed` → `ErrOrder` → `ErrDelta` → `ErrFull`。零样本块也可封口。
- `Decode(b)` 返回起点与样本序列：读 64 位起点后逐样本解码，直到结束标记；样本中途位不足、
  读完仍无标记、标记后剩余位 ≥ 8 或含非零补位，均报 `ErrCorrupt`。

### 本地验证

```bash
# 单元测试（含规范示例、区间边界、窗口复用边界、容量边界、截断拒绝）
go test ./tsblock

# 2000 组随机序列与朴素位串模型对照 + 解码往返（打印输入、输出与判定依据）
go test -run TestRandomModel -v ./tsblock

# 竞态检测
go test -race ./tsblock
```
