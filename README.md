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

## 全外连接增量维护器（`fulljoin`）

包 `fulljoin` 在左右两侧行集合上按同一键维护全外连接物化视图，
只消费 `+`/`-` 变更日志做增量更新，不做整键重建。

### 结果形态

对每个键，设左行集合 L、右行集合 R：

- 两侧都有行：输出 `|L| × |R|` 个配对行 `(l ⋈ r)`，两侧字段均非空。
- 只有左行：每条左行输出一个补位行 `(l ⋈ ∅)`，空位在右侧。
- 只有右行：每条右行输出一个补位行 `(∅ ⋈ r)`，空位在左侧。
- 两侧都无：该键无任何输出。

### 穿越零切换规则

补位行与配对行之间的形态切换，**只在某侧计数穿越零时发生**：

- 某侧计数 `0 -> 非零`（另一侧非空）：先撤回另一侧全部补位行，再输出两侧的配对行。
- 某侧计数 `非零 -> 0`（另一侧非空）：先撤回该键全部配对行，再为另一侧输出补位行。
- 非穿越的增删：只增删涉及该行的结果行（插入新增另一侧行数个配对行，删除撤回对应配对行），不触碰其他行。
- 任何切换都遵循“**先撤回旧形态，再输出新形态**”的日志顺序；撤回时内部断言目标行当前存在且空位形态相符（左补位空位在右、右补位空位在左），不满足即视为不变量被破坏并 panic。

因此同一键上的输出永远与“从两侧当前行集合朴素重算”的结果逐字段一致。

### 补位行与配对行的关系

二者是同一结果集合在不同计数区间下的互斥形态：当且仅当另一侧计数为零时一行才以补位形态存在；
另一侧计数一旦变为非零，该补位行立即被撤回并由配对行取代，反之亦然。
左侧计数穿越到零为右行产生的补位空位在左（`∅ ⋈ r`），右侧计数穿越到零为左行产生的补位空位在右（`l ⋈ ∅`），
二者是不同的行；撤回必须命中当前存在且形态相符的那一条。

### 三类可判定错误

`Apply` 对整批先做模拟校验，任一条非法则整批不生效（状态与已输出日志均不变）。错误互不相同，用以下函数判定：

- `fulljoin.IsDuplicateID(err)`：同一侧重复插入已存在的行标识（`ErrDuplicateID`）。
- `fulljoin.IsRowNotFound(err)`：删除某侧当前不存在的行标识（`ErrRowNotFound`）。删除只按“侧 + 行标识”定位。
- `fulljoin.IsEmptyKey(err)`：行的键为空，插入/删除均拒绝（`ErrEmptyKey`）。

### 并发

`Apply` 串行化写入；`View`、`Log`、`Check` 为只读操作，彼此可并发、也可与写入并发。
`View` 返回确定性深拷贝快照（按键、左 ID、右 ID 排序，空位排末尾）；
同一实例在无写入间隔下被多个读者并发读取时，快照逐字段相同。
`Check` 同时校验“朴素批量重算 == 增量视图”与“从空状态重放全部日志 == 增量视图”。

### 本地验证

```bash
# 全量测试（含穿越零、左右补位差异、三类非法输入原子回滚、并发只读、2000 组随机差分）
go test -race -v ./fulljoin

# 运行打印输入/日志/视图/自检判定的演示
go run ./cmd/fulljoin-demo

# 覆盖率与静态检查
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
gofmt -l . && go vet ./...
```

> 若 `go build` 报构建缓存目录只读，可设置 `GOCACHE=/tmp/gocache`。
