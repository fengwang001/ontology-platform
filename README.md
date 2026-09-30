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

## 多版本事务历史的隔离异常判定器

实现在 `anomaly` 包，入口为 `anomaly.Analyze(anomaly.History) anomaly.Result`。
判定器无状态、确定性、可并发调用；结果只取决于历史内容，与事务和操作在
输入中的排列顺序无关。

### 输入模型

- `History.Txns`：至多 12 个事务，编号为正且互不相同；`0` 保留给初始版本。
  状态为 `anomaly.Committed` 或 `anomaly.Aborted`。
- 写操作：`anomaly.Op{Key: "k"}`（`Read` 为 nil）。事务对同一键的第 n 次写
  产生版本 `(事务编号, n)`，Seq 从 1 起。
- 读操作：`anomaly.Op{Key: "k", Read: &anomaly.Version{Txn: 1, Seq: 2}}`；
  读初始版本用 `{Txn: 0, Seq: 0}`。
- `History.Order[key]`：该键的版本次序，初始版本必须最先，其后是每个在该键
  上写过的**已提交事务的最后一次写**，各出现一次；顺序任意（即版本图）。

### 三类依赖边（只在已提交事务之间、两端事务不同时产生）

只有版本次序内的版本产生边，方向统一为“旧版本相关方 → 新版本相关方”：

- **写写边 `anomaly.WW`**：次序中 Ti 写的版本紧邻在 Tj 写的版本之前，
  则 `Ti → Tj`。初始版本不是任何事务写的，不作为来源。
- **写读边 `anomaly.WR`**：Tj 读了 Ti 写的版本，则 `Ti → Tj`。
- **读写边 `anomaly.RW`**（反依赖）：Tj 读了某版本，而 Ti 写的版本紧邻其后，
  则 `Tj → Ti`。读初始版本时，其后继为该键次序的第一个版本。

同一对事务之间可同时存在多种边（结果中以位掩码合并）。

### 类别判定顺序（只报第一个命中的类别）

先做输入校验（见下），随后：

1. **G0**：存在纯写写环（环上每一步都可走 WW 边）。
2. **G1a**：已提交事务读到已中止事务写的版本（无环，不给出见证环）。
3. **G1b**：已提交事务读到别的事务对该键的非最后一次写（无环）。
4. **G1c**：存在仅由 WW 与 WR 边组成、且至少一条 WR 的环。
5. **G-single**：存在恰含一条 RW 边的环（其余为 WW/WR）。
6. **G2**：存在含两条及以上 RW 边的环。
7. 以上均不命中：无异常。

若同一节点序列在某一步上同时可选多种边，按 `WW > WR > RW` 的优先顺序选择
具体类型，以使其命中尽可能早的类别（这与逐环穷举每个具体类型环等价）。

见证环选取规则：最短；等长时取事务编号序列字典序最小；序列以环内最小编号
事务起头，方向固定（不反向旋转）。

### 隔离等级推导

| 命中类别 | 隔离等级 |
| --- | --- |
| G0 | 无 |
| G1a / G1b / G1c | PL-1 |
| G-single | PL-2 |
| G2 | PL-2+ |
| 无命中 | PL-3 |

### 输入拒绝（整体拒绝，不改变任何状态）

按以下顺序只报第一个错误（`Result.Rejected == true`，错误码见 `anomaly.Err*`）：

1. `ErrTxnID`：事务编号为 0 或重复。
2. `ErrStatus`：状态既非 committed 也非 aborted。
3. `ErrReadVersion`：读引用了不存在的版本（含非法初始版本）。
4. `ErrOrder`：版本次序不符合构成——含已中止事务的版本、非最后一次写的
   版本、缺少某个已提交事务的最后一次写、初始版本不在首位或版本重复。
5. `ErrTooMany`：事务数超过 12。

### 用法示例

```go
h := anomaly.History{
    Txns: []anomaly.Txn{
        {ID: 1, Status: anomaly.Committed, Ops: []anomaly.Op{
            {Key: "x", Read: &anomaly.Version{Txn: 0, Seq: 0}},
            {Key: "y"}, // 写
        }},
        {ID: 2, Status: anomaly.Committed, Ops: []anomaly.Op{
            {Key: "y", Read: &anomaly.Version{Txn: 0, Seq: 0}},
            {Key: "x"},
        }},
    },
    Order: map[string][]anomaly.Version{
        "x": {{Txn: 0, Seq: 0}, {Txn: 2, Seq: 1}},
        "y": {{Txn: 0, Seq: 0}, {Txn: 1, Seq: 1}},
    },
}
r := anomaly.Analyze(h) // 写偏斜：r.Category == anomaly.G2，Level == "PL-2+"
```

### 本地验证

```bash
# 全量测试（包含随机小历史与逐环穷举参照的对拍，默认 8000 例/轮）
go test ./anomaly -v

# 竞态检测（含并发调用用例）
go test -race ./...

# 对拍多轮、覆盖率
go test ./anomaly -run TestRandomDifferential -count=10
go test -cover ./anomaly
```

`go test -v` 日志中每个用例都会打印输入历史、输出（类别/等级/见证环/边/
判定依据）以及拒绝原因。随机对拍的参照实现 `oracleAnalyze` 位于
`anomaly/anomaly_test.go`，它独立枚举所有简单环节点序列与每一步的具体边
类型组合，与主实现（Johnson 全简单环枚举）互相校验。
