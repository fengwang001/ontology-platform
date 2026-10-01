# ontology-platform

## 小文件内联 / 扩展属性布局管理器（`layout`）

包 `ontology/layout` 实现文件数据与扩展属性争用 inode 内有限区域时的布局
管理，含外部属性块的跨文件去重与块池占用记账。构造：

```go
m := layout.New(A, X, Bs, P) // A、X、Bs、P 均须 >= 1，否则 New panic(ErrInvalidArgument)
```

- `A`：inode 内区域大小；`X`：单个外部属性块容量；`Bs`：数据块大小；`P`：块池总块数。

### 属性占用与放置规则

- 属性为名字（1–255 字节）/值（0–4096 字节）对，同名 `SetXattr` 覆盖。
- 单个属性占用 `4 + roundup4(len(name)) + roundup4(len(value))` 字节，
  `roundup4(0)=0`，其余向上取整到 4 的倍数。
- 属性按**名字字节序升序**依次尝试放入 inode 内剩余空间，占用 `<=` 剩余即
  放入；遇到第一个放不下的属性即停止，**它及其后的全部属性（即使更小）一律
  外置**。
- inode 内剩余空间：内联模式为 `A - size`，块模式为 `A`。
- 某文件外部放置总字节 `<= X` 才算放置合法。
- 文件的外部属性集 `Ext` 为外置属性按名字升序的 `(名字,值)` 序列；
  `Ext` 为空不占外部块。

### 模式转换条件（`Resize` / `SetXattr` / `RemoveXattr`）

- `Resize(f, s)`（`0 <= s <= 2^40`）：
  - 内联模式：`s <= A` 且按 `s` 放置合法则保持内联，否则转块模式；
  - 块模式：`s == 0` 回到内联模式；否则保持块模式（缩小到 `<= A` 也不回内联）。
- `SetXattr` / `RemoveXattr` 后若内联放置不合法，则转块模式按剩余 `A` 重算
  （数据改占数据块）；块模式仍不合法则该操作整体拒绝。

### 外部块共享与块池占用

- `Ext` 非空的文件需要一个外部块；`Ext` **逐字节完全相同**的文件共享同一个
  外部块（最后一个使用者改变后自动释放）。
- 数据占用：块模式 `ceil(size / Bs)` 块（`size == 0` 为 0），内联模式不占块。
- 块池占用 = 各文件数据占用块数之和 + 全体文件中互不相同的非空 `Ext` 种数。
- 任何操作完成后占用必须 `<= P`（恰等允许），按共享/释放后的占用计算。
- `Clone` 复制模式、大小与全部属性；数据块独立占用，外部块与 `Ext` 相同者共享。
- `Stat` 返回模式、大小、数据块占用、`Ext` 编号（同 `Ext` 全部文件中最小编号，
  空为 0）及各属性按名字升序的落点（inode 内 / 外部）。`GetXattr` 返回值副本。

### 拒绝顺序（可用 `errors.Is` 区分；被拒操作不改任何状态，`Clone` 被拒不耗编号）

1. 参数非法（`s` 越界、名字/值长度越界）—— `ErrInvalidArgument`
2. 文件不存在 —— `ErrNotFound`
3. 属性不存在（`GetXattr`/`RemoveXattr`）—— `ErrNoXattr`；
   或属性任何模式都放不下（外部总字节超过 `X`）—— `ErrXattrTooLarge`
4. 块池不足（共享后完成占用仍 `> P`）—— `ErrPoolFull`

所有方法可并发调用（内部互斥），落点只由 `(模式, 大小, 属性集)` 决定，
相同操作序列重放结果完全相同。

### 本地验证

```bash
# 全量测试（含 2000 组随机操作序列与独立朴素模型的逐步对照）
go test ./...

# 打印差分测试每一步的输入、输出与判定依据（2000 组完整日志）
go test ./layout -run TestNaiveDifferential -v

# 指定随机种子复现
go test ./layout -run TestNaiveDifferential -v -naive-seed=20261001

# 竞态检测（含并发冒烟用例）与静态检查
go test -race ./...
go vet ./...
```

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
