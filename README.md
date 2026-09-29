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

## 增量左外连接（`join` 包）

`join` 包实现随左右两表插入/删除实时维护结果的增量左外连接组件，
下游按变更日志（changelog）顺序逐条应用即可始终得到正确的左外连接视图。

### 空填充行

- 左表每一行的结果形态二选一、互斥：
  - 右表存在同键行时，与该键下**全部**右行配对（一条左行 × 多条右行 = 多条配对行）；
  - 右表不存在任何同键行时，以**一条空填充行**存在（右侧标识与负载为空）。

### 配对、撤回与补回规则

- 左行插入：有同键右行则按右标识字典序插入全部配对；否则插入空填充行。
- 左行删除：撤回其全部配对行；若它以空填充行存在，则撤回空填充行。
- 右行插入：与全部同键左行新增配对；若是该键上**第一条**右行，对每个左行
  先撤回空填充行、再插入配对行（同一左行的两条记录相邻）。
- 右行删除：撤回相关配对行；若是该键上**最后一条**右行，为每个同键左行
  补回空填充行（先撤回配对、再补填充，同一左行两条相邻）。
- 输出按**对侧标识**字典序排列：左行变更按右标识排（空填充行右标识为空串，
  排在最前）；右行变更按左标识排。

### 批处理与拒绝原因

`Apply([]Op)` 原子地应用整批：先在内部副本上干跑校验，全部合法才提交；
任一条非法则整批拒绝，两表与已产生的判定日志都不改变。拒绝原因可通过
`*join.RejectError` 的 `Reason` 字段或 `errors.Is(err, join.ErrXxx)` 区分：

| 原因 | 触发条件 |
| --- | --- |
| `empty_key` | 插入/删除空连接键的行 |
| `empty_id` | 插入/删除空标识的行 |
| `unknown_side` | 操作侧既不是 Left 也不是 Right |
| `unknown_kind` | 操作类型既不是 Insert 也不是 Delete |
| `duplicate_id` | 插入该侧已存在的标识（含同批先插后插） |
| `id_not_found` | 删除该侧不存在的标识 |
| `row_limit_exceeded` | 插入会使左右总行数超过 `New(maxRows)` 上限 |

### 并发与确定性

- `Apply` 与 `Snapshot`、`DecisionLog` 通过读写锁互斥；`Snapshot` 返回某一时刻
  完整、逐行一致的结果副本，可被并发读取。
- 所有输出均按键、标识排序，同一输入序列在任意实例上反复计算产生完全相同的输出。

### 判定日志

`WithLogWriter(io.Writer)` 可输出人类可读日志，逐条打印输入操作、判定依据
（例如“最后一条右行，补回空填充行”）与输出条目；`DecisionLog()` 返回结构化记录。
被拒绝的批只输出拒绝原因文本，不会追加结构化记录。

### 本地验证

```bash
# 全量测试（含竞态检测）
go test -race -v ./join/

# 若 GOCACHE 所在目录只读，可指定临时缓存
GOCACHE=/tmp/gocache go test -race ./...

# 格式与静态检查
gofmt -l .
go vet ./...
```
