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

## 可滚动结果集游标（`cursor` 包）

`cursor.Manager` 在固定行数的结果集上管理命名游标，支持 SQL 风格的滚动定位。

### 位置模型

- 结果集行数为 `n`（`0 <= n <= 2^40`），行号从 `1` 起。
- 游标位置 `p ∈ [0, n+1]`：`0` 表示首行之前（before first），`n+1` 表示末行之后（after last），`1..n` 表示停在某一行上。
- `Open` 后初始位置为 `0`。位置任何时刻都不会越出 `[0, n+1]`。
- 「返回一行」即返回该行行号；单行类操作无行可返回时返回 `nil`，但位置仍按规则移动。

### 单行类操作的目标位置

`Fetch(name, op, k)` 先计算目标位置 `t`，再统一套用边界规则：

| 操作 | 目标位置 `t` |
| --- | --- |
| `NEXT` | `p+1` |
| `PRIOR` | `p-1` |
| `FIRST` | `1` |
| `LAST` | `n` |
| `ABSOLUTE k` | `k>0 → k`；`k<0 → n+1+k`；`k=0 → 0` |
| `RELATIVE k` | `p+k` |

边界规则：`t<1` → 位置置 `0`，不返回行；`t>n` → 位置置 `n+1`，不返回行；否则位置置 `t` 并返回第 `t` 行。`NEXT/PRIOR/FIRST/LAST` 忽略 `k`。`k` 与 `p` 的运算按精确整数进行（RELATIVE 溢出时数学和映射到对应边缘），`k` 取 `int64` 极值不会回绕。

### 批量操作

- `FORWARD k`（`k>0`）：从第 `p+1` 行起（`p=0` 时即第 `1` 行）升序返回至多 `k` 行，不越过第 `n` 行。返回行数不足 `k`（用尽）时位置置 `n+1`；恰好 `k` 行时位置置最后返回行的行号。
- `BACKWARD k`（`k>0`）：从第 `p-1` 行起（`p=n+1` 时即第 `n` 行）降序返回至多 `k` 行，不越过第 `1` 行。不足 `k` 行时位置置 `0`；恰好 `k` 行时位置置最后返回行（行号最小者）。

### 只进游标

`Open(name, n, false)` 打开只进游标，只允许：

- `NEXT`
- `FORWARD`（且 `k>0`）
- `RELATIVE` 且 `k >= 0`

其余操作一律以 `ErrForwardOnly` 拒绝。

### 错误原因（哨兵错误，按下列顺序只报第一个）

- `Open`：`ErrEmptyName`（名字为空）→ `ErrInvalidN`（`n<0` 或 `n>2^40`）→ `ErrNameExists`（名字已打开）。
- `Fetch`：`ErrNotFound`（游标不存在）→ `ErrInvalidOp`（操作非法）→ `ErrForwardOnly`（只进游标不允许）→ `ErrNonPositiveK`（`FORWARD/BACKWARD` 的 `k` 不为正）。
- `Position` / `Close`：游标不存在返回 `ErrNotFound`。

被拒绝的操作不会改变任何位置；`Close` 释放名字后可立即以同名重新 `Open`。

### 并发

不同游标的操作互不阻塞；同一游标上的并发操作按互斥串行化，等价于某个串行顺序，位置始终合法。

### 本地验证

```bash
# 全量测试（含边界、拒绝顺序、int64 极值、并发）
go test -v ./cursor/

# 2000 组随机操作序列与 big.Int 朴素参考模型对拍，日志含输入/输出/判定依据
go test -run TestFuzzAgainstReference -v ./cursor/

# 竞态检测
go test -race ./...
```
