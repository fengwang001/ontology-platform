# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 数据血缘追踪（`lineage` 包）

`lineage.Tracker` 追踪对象的来源与派生关系，支持双向追溯，保证血缘完整且只指向正确版本。

### 血缘记录

- `PutSource(id, payload)` 登记源对象；`Derive(DeriveRequest)` 执行派生。
- 派生在**操作时**为每个输入记录一条 `输入版本 → 输出版本` 的血缘边（`lineage.Edge`），输入去重后按引用排序。
- 无任何输入的派生直接拒绝（`ErrNoLineage`），不登记输出、不写边。
- 相同操作 + 相同输入版本 + 相同内容重复（含并发）提交命中同一版本，幂等不产生重复血缘。

### 追溯方向

- `Trace(ref, Upstream)`：向上游追溯“谁产生了我”。
- `Trace(ref, Downstream)`：向下游追溯“我产生了谁”。
- `Trace(ref, Both)`：以目标为中心的完整双向血缘子图。
- `Direct(ref, dir)` 返回直接上游/下游。
- 返回的 `Graph` 中节点与边按 `id@version` 规范化排序；同一组派生以任意顺序、任意并发时序提交，得到完全相同的血缘图。

### 版本规则

- 版本号为内容指纹（SHA-256 前 16 位）：
  - 源对象：`sha256("source" + 内容)`，内容不变版本不变；
  - 派生对象：`sha256(操作 + 排序后的全部输入版本 + 输出内容)`，输入或内容变化版本必变。
- 对象旧版本被新版本取代后即**失效**；`Edges()` 返回的旧边 `Active=false`。
- 派生只允许引用输入对象的**当前版本**；追溯起点与所有可达边端点也必须是当前版本。

### 拒绝原因（可用 `errors.Is` 区分）

| 错误 | 触发场景 |
| --- | --- |
| `ErrNoLineage` | 派生没有任何输入血缘记录 |
| `ErrRefNotFound` | 血缘引用不存在的对象或版本 |
| `ErrStaleVersion` | 血缘指向已被取代的过期版本（写入或查询时） |
| `ErrMissingUpstream` | 派生节点缺少上游边 / 上游反向索引缺失 |
| `ErrMissingDownstream` | 消费方记录了输入但下游反向边缺失 |
| `ErrKindConflict` | 同一 ID 在源对象与派生对象之间混用 |

所有拒绝都发生在状态变更之前，被拒绝的操作不改变任何血缘；查询路径不修改状态。

### 并发模型

- 记录与查询可被并发调用：写操作持互斥锁，读操作（`Trace`/`Direct`/`Current`/`Edges`）持读锁。
- 并发派生下血缘完整：每条边的双向索引（`out`/`in`）在同一临界区内成对写入，查询时做“节点输入记录 ↔ 双向索引”交叉闭合校验。

### 日志

追踪器通过 `log/slog` 打印：源对象登记、`派生血缘记录`、`血缘追溯`/`血缘直接追溯` 及拒绝事件，每条均带 `判定依据` 字段说明版本指纹、当前版本与闭合校验结论。传入 `New(w)` 的 `io.Writer` 决定日志去向（如 `os.Stderr`）。

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

# 血缘组件：竞态检测 + 详细日志（查看派生/追溯/判定依据日志）
go test -race -v ./lineage
go test -run TestConcurrent -race -count=10 ./lineage

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
