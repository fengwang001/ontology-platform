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

## 倒排索引段合并器（`merger` 包）

`merger` 包实现带删除回放的倒排索引，多个不可变段可分两步合并为一个新段。

### 数据模型

- `Register(docs)`：登记一批 `(key, terms)` 文档产生一个段；局部编号按给出顺序从 0 开始；`terms` 可为空，词项为非空字符串并按字节序比较。
- `Delete(key)`：只给该键当前存活的文档打删除标记，不改动段内其余内容；已删除的键可再次登记。
- 段统计量 `Stats`：`MaxDoc`（局部编号总数，含已删除）、`NumDocs`（存活数）、`TermCount`（存活文档中的不同词项数）。
- `Postings(id, term)` 返回该段存活文档按局部编号升序的 `(Doc, TF, Positions)`，`DF` 即倒排表长度；词项不存在时返回空切片。统计值与倒排表均为副本，修改不影响内部状态。

### 两步合并与删除回放

1. `BeginMerge(ids)`：在调用时刻冻结合并集合。按 `ids` 的段次序、段内局部编号升序，取各段该时刻存活的文档，重新编号为 0..m-1；句柄号从 1 起自增，输入段标记为忙（不能同时参与另一个未结束的合并）。
2. `Commit(handle)`：
   - 新段 `MaxDoc == m`；冻结后、提交前被删除的文档保留编号但标记为已删除；
   - 是否删除以被冻结的那个文档本身当时的删除标记为准，与合并期间用同键登记的新文档无关（旧文档不会复活）；
   - 倒排表、`DF`、`TermCount` 只按提交时刻仍存活的文档计算；
   - 成功后输入各段被移除，返回新段。
3. `Abort(handle)`：放弃合并，输入段恢复可合并。

无合并期间删除时，提交结果与把同一冻结文档序列直接登记成一个段在 `Stats` 与全部倒排表上逐字段相同。

### 编号分配与拒绝规则

- 段编号从 1 开始，登记成功与合并提交成功各取一个下一号；被拒绝的操作不消耗编号；句柄号从 1 开始独立自增，拒绝不消耗。
- 拒绝原因通过 `*merger.Error` 的 `Code` 区分：`ErrInvalidArgument`、`ErrKeyConflict`、`ErrSegmentNotFound`、`ErrSegmentBusy`、`ErrKeyNotFound`、`ErrInvalidHandle`，被拒绝操作不改变任何段、标记与编号。
- 登记校验顺序：空批 / 空键 / 空词项（参数非法）→ 存活键或批内重复（键冲突）。
- 合并校验顺序：少于 2 个或重复 id（参数非法）→ 编号不存在（按 ids 次序报首个）→ 段忙（按 ids 次序报首个）。

所有方法均可并发调用（互斥串行化，等价于某个合法串行顺序），不变量：各在册段 `NumDocs` 之和恒等于全部存活键个数。

### 本地验证

```bash
# 全量测试（含 2000 组随机操作序列与朴素重建对照，-v 打印每组输入/输出/判定依据）
go test -race -v ./merger

# 只看随机对照
go test -v -run TestRandomSequencesAgainstOracle ./merger

# 常规检查
go test ./...
go vet ./...
gofmt -l .
```
