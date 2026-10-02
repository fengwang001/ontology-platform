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

## overlay：联合目录视图

`overlay` 包实现一层只读下层与一层可写上层的联合目录视图
（`overlay/overlay.go`、`overlay/query.go`、`overlay/ops.go`、
`overlay/rename.go`）。

### 构造与路径规则

- `New(lower map[string][]byte)`：键不含前导斜杠，各段非空且不为
  `.`、`..`；以 `/` 结尾的键表示目录（值被忽略），否则为文件。
  每个键的所有上级目录必须也以目录键出现（根隐含存在），同一
  路径不得同时是文件键与目录键，否则整体拒绝（`ErrInvalidPath`）。
  下层此后不变，上层初始为空。
- 操作路径规则相同但不带结尾斜杠，空串表示根（仅 `Lookup`、
  `ReadDir` 接受）。
- 上层记录四类：文件（含内容）、普通目录、不透明目录、白障。

### 合并解析规则

- 根总是目录。
- `U(q)` 为白障：`q` 不存在；为文件：`q` 是该文件；为不透明
  目录：`q` 是只含上层内容的目录；为普通目录：`q` 是目录，子项
  为上层子项与下层子项的并集（下层子项仅当 `L(q)` 是目录且 `q`
  下层可达时参与），同名项上层优先。
- `U(q)` 不存在：`q` 下层可达则取 `L(q)`，否则不存在。
- `q` 下层可达：`L(q)` 存在且 `q` 的每个真祖先在上层都不是不
  透明目录。一个子项名只要上层有任何记录（含白障），下层同名项
  即被隐藏。

### 白障与不透明目录的写入条件

- `Remove`：path 下层可达时，先补缺失的上层祖先为普通目录，丢弃
  上层 path 之下的全部记录，并令 `U(path)` 为白障；否则只丢弃上
  层 path 及其之下的全部记录，不补祖先。
- `Mkdir`：`U(path)` 是白障则替换为不透明目录，否则记为普通目
  录；缺失的上层祖先补为普通目录。
- `Write`：`U(path)` 记为文件（替换白障或旧文件）；缺失的上层
  祖先补为普通目录。
- `Rename` 纯上层目录时，`new` 处记为不透明目录，当且仅当
  `old` 的记录是不透明目录、或 `new` 原有的上层记录是白障、或
  `L(new)` 是目录且 `new` 下层可达（均按改名前状态判定）。

### 改名规则

- `old` 的内容有任何部分来自下层目录时一律拒绝（`ErrCrossLayer`）：
  `U(old)` 不存在而 `old` 取自下层目录，或 `U(old)` 为普通目录
  且 `L(old)` 是目录且 `old` 下层可达的合并目录。其余情形为文件
  或纯上层目录（含不透明目录、下层不可达处的普通目录）。
- `new` 已存在（白障视为不存在）：两文件则替换；old 文件 new 目
  录报 `ErrIsDir`；old 目录 new 文件报 `ErrNotDir`；两目录且
  `new` 非空报 `ErrNotEmpty`，为空则替换。
- old 为文件：先按 `Write(new, 内容)` 执行，再按 `Remove(old)`
  执行。old 为纯上层目录：丢弃上层 `new` 及其之下的全部记录，补
  `new` 缺失的上层祖先为普通目录，把上层 `old` 及其之下的全部记
  录整体改前缀为 `new`，再按上条决定 `new` 处是否不透明；最后若
  `old` 下层可达，补 `old` 缺失的上层祖先并令 `U(old)` 为白障，
  否则上层不再保留 `old` 的记录。

### 拒绝顺序

被拒绝的操作不改变上层，按下列顺序只报第一个（均可 `errors.Is`
区分）：路径非法（`ErrInvalidPath`，`Remove`/`Mkdir`/`Write` 的
空串也非法）→ 祖先解析（自根向下，不存在报 `ErrNotFound`，是文
件报 `ErrNotDir`）→ `Rename` 先 old 后 new 各自做路径与祖先判
定（old 本身不存在报 `ErrNotFound`，且先于 new 的祖先判定）→
各操作自有原因：`Rename` 依次报移入自身（`ErrRenameSelf`）、跨
层（`ErrCrossLayer`）、new 的冲突（`ErrIsDir`/`ErrNotDir`/
`ErrNotEmpty`）；`Mkdir` 报 `ErrExist`；`Write` 报 `ErrIsDir`；
`Remove` 报 `ErrNotFound`/`ErrNotEmpty`；`Lookup`/`ReadDir` 报
`ErrNotFound`，`ReadDir` 作用于文件报 `ErrNotDir`。

### 并发与不变式

所有方法（`Lookup`、`ReadDir`、`Mkdir`、`Write`、`Remove`、
`Rename`、`Upper`）可并发调用，由读写锁保证结果等价于某个串行
顺序。`ReadDir` 列出的每个名字都能 `Lookup` 到，未列出的名字
`Lookup` 不到；上层任何记录的真祖先在上层都是目录记录，且不存
在位于白障或文件之下的记录；相同操作序列重放得到完全相同的上层
记录。

### 本地验证

```bash
# 全部测试（含 2000 组随机操作序列与朴素模型的对照）
go test ./overlay

# 打印每组序列的输入、输出与判定依据
go test ./overlay -run TestRandomAgainstModel -v

# 竞态检测
go test -race ./overlay

# 静态检查与格式
go vet ./overlay
gofmt -l overlay
```
