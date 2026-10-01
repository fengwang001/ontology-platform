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

## 目录树配额账本（`quota` 包）

`quota` 实现带改名转移、字节预留与原子批处理的目录树配额账本。
目录树初始只有根目录（编号 0），每次成功的 `Mkdir`/`AddFile` 分配下一个连续编号；
被拒绝的操作与回滚的批处理不消耗编号。

### 用量与有效字节

- 目录 `D` 的子树用量为 `(字节, 条目数)`：字节是 `D` 之内所有文件的 `size` 之和，
  条目数是 `D` 之内（不含 `D` 自身）所有文件与子目录的个数。
- 每个目录 `d` 另有自身预留 `r_d`（初始 0）；`R(D)` 为 `D` 之内（含自身）所有目录的 `r` 之和。
- 有效字节 `E(D) = 子树字节 + R(D)`；字节限额一律针对 `E` 核对，条目限额针对条目数。
- `Usage(d)` 返回 `(子树字节, 子树条目数, R(d))`。

### 限额

`SetQuota(d, b, n)` 设置限额，`b`/`n` 取 `-1` 表示该维度无限制，否则为不小于 0 的上限；
用量等于上限允许、超过不允许。限额低于该目录当前对应维度用量（字节维度为 `E`）时报
`ErrBelowUsage`。核对一律自近到远逐个目录进行，同一目录先字节后条目数，只报第一个违规。

### 预留抵扣规则

- `Reserve(d, b)`（`b > 0`）对 `d` 及全部祖先核对「`E` 增加 `b`」，成功后 `r_d += b`。
- `Release(d, b)`（`b > 0`）要求 `b <= r_d`，不核对限额。
- `AddFile(p, size)` 先取 `c = min(size, r_p)`，按「`E` 增加 `size - c`、条目数增 1」核对，
  成功后 `r_p -= c`；预留大于 `size` 时只用掉 `size`。
- `Resize(f, s)` 只按差值核对字节增量，不使用预留；增量不大于 0 恒允许。

### 改名只核对差异链

`Rename(x, p)` 把节点 `x` 整体移到目录 `p` 之下：字节增量为 `x` 的子树字节加 `R(x)`
（文件即 `size`），条目增量为自身一个条目加内部全部条目。只有「`p` 及其祖先」中不在
「`x` 旧父目录及其祖先」里的目录承担增量并核对限额；只在旧链里的目录退还；公共祖先
不变也不核对；预留随目录一起移动。`p` 恰为当前父目录时成功且什么都不改。

### 批处理回滚语义

`Batch(ops)` 按序原子执行一组操作，后面的操作可引用批内前面新建节点的编号
（编号按成功顺序连续分配，可预先推算）。任一操作被拒绝则整批回滚——用量、预留、
限额、编号全部恢复——返回首个失败操作的下标与原因；错误同时可用
`errors.Is(err, ErrBatchFailed)` 与 `errors.Is(err, 底层原因)` 判定。`ops` 为空报
`ErrInvalidArgument`。

### 拒绝顺序

同一操作按下列顺序只报第一个错误（均可 `errors.Is` 区分）：

1. `ErrInvalidArgument`：参数非法（负的 `size`/`s`、非正的 `Reserve`/`Release` 量、小于 `-1` 的限额）。
2. `ErrNotFound`：节点不存在（涉及两个节点时先 `x` 后 `p`）。
3. `ErrTypeMismatch`：类型不符（`Mkdir`/`AddFile`/`Rename` 的 `p` 不是目录，
   `SetQuota`/`Reserve`/`Release` 的对象不是目录，`Resize` 的对象是目录）。
4. 结构错误：`ErrRoot`（`Remove`/`Rename` 作用于根）、`ErrNotEmpty`（删除非空目录）、
   `ErrCycle`（把目录移入自身或子孙之下）。
5. `ErrInsufficientReservation`：`Release` 的 `b` 大于 `r_d`。
6. `ErrByteQuotaExceeded` / `ErrEntryQuotaExceeded`：配额超限，
   可用 `errors.As` 取出 `*QuotaError` 获得违规目录编号。
7. `ErrBelowUsage`：`SetQuota` 的限额低于当前用量。

被拒绝的操作不改变任何用量、预留、限额与编号；相同操作序列重放得到完全相同的编号与结果。

### 本地验证

```bash
# 全部单元测试（限额边界、预留抵扣、改名差异链、批处理回滚等）
go test ./quota

# 2000 组随机操作序列（含随机 Batch）与从头累加的朴素模型对照，
# -v 日志打印每步的输入、输出与判定依据
go test -run TestRandomAgainstNaive -v ./quota

# 并发等价串行 + 原子可见性（竞态检测）
go test -race ./quota
```
