# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 联合目录视图（`ontology` 包）

`ontology.New(lower map[string]string)` 构造一个“一层只读下层 + 一层可写上层”的
联合目录视图。下层键不含前导 `/`；以 `/` 结尾的键是目录（值忽略），否则是文件。
每个键的所有上级目录必须都以目录键出现（根隐含），同一路径不得既是文件又是目录，
否则构造整体拒绝（包装 `ErrInvalidPath`）。下层构造后不可变，上层初始为空。

上层记录为路径到四类之一：

- `KindFile`：文件（带内容）
- `KindDir`：普通目录（与下层合并）
- `KindOpaque`：不透明目录（只含上层内容）
- `KindWhiteout`：白障（路径不存在并隐藏下层同名项）

### 合并解析

设 `U(q)` 为上层记录、`L(q)` 为下层条目。q“下层可达”当且仅当 `L(q)` 存在且 q
的每个真祖先在上层都不是白障/文件/不透明目录（普通目录只有在对应下层目录本身
可达时才放行下层内容）。逐段解析：

- 根总是目录；
- `U(q)` 为白障：q 不存在；
- `U(q)` 为文件：q 是该文件（其下任何路径都不存在，祖先判定报“不是目录”）；
- `U(q)` 为不透明目录：q 是目录且只列上层子项；
- `U(q)` 为普通目录：q 是目录，子项为上层子项与下层子项的并集（仅当 `L(q)`
  是目录且 q 下层可达时纳入下层），同名上层优先；
- `U(q)` 不存在：q 下层可达则取 `L(q)`，否则不存在。

一个子项名只要上层有任何记录（含白障），下层同名项即被隐藏；白障本身不出现在
`ReadDir` 列表中。`ReadDir` 按名字节序升序返回。

### 操作语义

- `Mkdir(path)`：祖先在合并视图中都必须是目录；`path` 已存在报 `ErrExist`。
  缺失的上层祖先记录自根向下补为普通目录；若 `U(path)` 是白障则替换为**不透明
  目录**，否则记为普通目录。
- `Write(path, content)`：祖先要求同上；`path` 是目录报 `ErrIsDirectory`；
  `U(path)` 记为文件（替换白障或旧文件），被其遮蔽的下层目录内容不再可见。
- `Remove(path)`：`path` 须存在；目录须在合并视图中无子项（`ErrDirNotEmpty`）。
  若 `path` 下层可达：先补缺失的上层祖先为普通目录，丢弃 `path` 之下全部上层
  记录，并令 `U(path)` 为白障；否则只丢弃上层 `path` 子树，不补祖先、不留白障。

### 改名规则（`Rename(old, new)`）

- 跨层拒绝：old 的内容有任何部分来自下层目录时一律拒绝（`ErrCrossLayer`），即
  无上层记录且取自下层的目录，或 `U(old)` 为普通目录且 `L(old)` 是下层可达的
  目录。其余为文件或“纯上层目录”（不透明目录，或下层不可达处的普通目录）。
- 冲突（白障视为不存在）：文件→文件替换；文件→目录 `ErrIsDirectory`；
  目录→文件 `ErrNotDirectory`；目录→非空目录 `ErrDirNotEmpty`，空目录被替换。
- 文件：等价于先 `Write(new, 内容)` 再 `Remove(old)`；old 下层可达则在原处写白障。
- 纯上层目录：丢弃 `new` 上层子树，补 `new` 的缺失祖先为普通目录，把 `old`
  子树整体改前缀；`new` 头部记为不透明目录当且仅当 old 头部不透明、或 `new`
  原上层记录是白障、或 `L(new)` 是下层可达的目录，否则保持原类型。若 old
  下层可达，最后在 old 处补祖先并写白障；否则上层不再有 old 的记录。

### 拒绝顺序（只报第一个）

1. 路径非法（`Remove`/`Mkdir`/`Write` 的空串也非法；`Lookup`/`ReadDir` 允许根）；
2. 祖先解析，自根向下：不存在 `ErrNotFound`，是文件 `ErrNotDirectory`；
   `Rename` 先 old 后 new，old 本身不存在（`ErrNotFound`）先于 new 的祖先判定；
3. 操作自有原因：`Rename` 依次为 `new==old` 或 new 位于 old 之下（`ErrIntoSelf`）、
   跨层（`ErrCrossLayer`）、冲突；`Mkdir` 已存在（`ErrExist`）；`Write` 是目录
   （`ErrIsDirectory`）；`Remove`/`Lookup`/`ReadDir` 不存在（`ErrNotFound`），
   目录非空（`ErrDirNotEmpty`），`ReadDir` 作用于文件（`ErrNotDirectory`）。

所有被拒绝的操作都不改变上层。所有方法可用 `sync.RWMutex` 并发调用，结果等价于
某个串行顺序；不变式：上层任何记录的真祖先都是上层目录记录；白障/文件之下不存在
上层记录；相同操作序列重放得到完全相同的上层记录。

### 错误判定

错误均包装下列哨兵，用 `errors.Is` 区分：`ErrInvalidPath`、`ErrNotFound`、
`ErrNotDirectory`、`ErrIsDirectory`、`ErrExist`、`ErrDirNotEmpty`、
`ErrCrossLayer`、`ErrIntoSelf`。

### 本地验证

```bash
# 本机 Go 若不在 PATH，先：
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache

go test ./...                    # 全量测试（含 2000 组随机对照）
go test -race ./...             # 竞态检测
go test -run TestRandomAgainstNaive -v ./ontology   # 打印输入/输出/判定依据日志
go vet ./...
gofmt -l .
```

测试包含：白障的产生与不产生、下层目录删除后重建为不透明目录且内容不复活、
再删除仍写白障、不透明祖先之下删除不写白障、写文件替换白障遮蔽下层目录、
深层写补普通目录祖先、删除丢弃子树记录、上层文件/目录遮蔽下层条目、
`ReadDir` 并集/同名优先/排序、被拒绝操作不改上层；改名覆盖跨层拒绝、
到下层同名目录变不透明、不透明目录原处写白障、下层不可达处不留白障、
new 为白障变不透明、空/非空目录冲突与移入自身；以及 2000 组随机操作序列与按
上述定义逐路径解析的朴素模型（`naiveModel`）的完整对照。

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
