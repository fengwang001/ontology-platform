# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 目录树配额账本（`quota` 包）

`quota` 包实现带改名转移、字节预留与原子批处理的目录树配额账本，所有
方法均并发安全，可被多个 goroutine 同时调用。

### 编号与基本结构

- 初始只有根目录，编号 0；每次成功的 `Mkdir` / `AddFile` 分配下一个编号
  （从 1 起连续）。被拒绝的操作与被回滚的批处理都不消耗编号。
- 文件有 `size`（≥0），目录没有字节数。

### 用量与有效字节

对目录 `d` 定义：

- 子树字节：`d` 之内所有文件 `size` 之和。
- 子树条目数：`d` 之内（不含 `d` 自身）文件与子目录的个数。
- 自身预留 `r_d`（字节，初始 0）。
- 子树预留 `R(d)`：`d` 之内（含 `d` 自身）所有目录的 `r` 之和。
- 有效字节 `E(d) = 子树字节 + R(d)`。

字节限额始终与 `E(d)` 比较，条目限额与子树条目数比较；用量等于上限允许，
超过上限拒绝。`Usage(d)` 返回 `(字节, 条目数, R(d))`。

### 预留抵扣规则

- `Reserve(d, b)`（b>0）：对 `d` 及全部祖先核对 `E` 增加 `b`，通过后 `r_d += b`。
- `Release(d, b)`（b>0）：要求 `b ≤ r_d`，直接 `r_d -= b`，不再核对限额。
- `AddFile(p, size)`：先取 `c = min(size, r_p)`，按「`E` 增加 `size−c`、
  条目数增 1」核对 `p` 及祖先，成功后 `r_p -= c`。即预留大于 size 时只用掉
  size，`E` 的净增恰为 `size−c`。
- `Resize(f, s)` 只按差值 `s−原大小` 影响字节、不使用预留；差值 ≤0 恒允许。

### 改名（Rename）差异链规则

`Rename(x, p)` 将 `x` 整棵子树移到 `p` 下：

- 字节载荷为子树字节 + `R(x)`（文件即自身 size）；条目载荷为自身一个条目
  加内部全部条目；预留随所属目录一起移动。
- 只在新父链（`p` 及其祖先）而不在旧父链（`x` 旧父目录及其祖先）中的目录
  承担载荷并核对限额；只在旧链中的目录逐级退还；公共祖先用量不变也不核对。
- `p` 恰为 `x` 当前父目录时成功且什么都不改；把目录移入自身或其子孙之下
  报结构错误。

### 批处理回滚语义

`Batch(ops)` 按序执行 `Mkdir`、`AddFile`、`Resize`、`Remove`、`Rename`、
`Reserve`、`Release`、`SetQuota`；后面的操作可以引用批内前面新建节点的编号。
任一操作被拒绝则整批回滚（用量、预留、限额、编号全部恢复），返回首个失败
操作的下标与原因；该错误同时满足 `errors.Is(err, ErrBatch)` 与底层原因
（如 `ErrBytesQuota`）。`ops` 为空按参数非法拒绝。

### 拒绝顺序

只报第一个违规：参数非法 → 节点不存在（涉及两个节点时先 `x` 后 `p`）→
类型不符 → 结构错误（操作根 `ErrRoot`、删除非空目录、移入自身/子孙）→
预留不足 → 字节配额超限 → 条目配额超限 → `SetQuota` 低于当前用量（字节维度
对 `E`）。限额核对自近到远逐个检查，同一目录先字节后条目。

### 本地验证

```bash
# 全部测试（含 2000 组随机序列对朴素重算模型的差分测试，需加 -v 查看日志）
go test ./quota -v

# 竞态检测（Rename/Batch 与并发 Usage 的原子可见性）
go test -race ./quota

# 全量测试 / 构建 / 静态检查
go test ./...
go build ./...
go vet ./...
```

差分测试 `TestDifferentialRandom` 对每条输入打印 `input`、`output`（编号与
错误类别/违规目录）以及与朴素模型逐条重算结果对照的 `basis` 判定依据。

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
