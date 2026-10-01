# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 倒排索引段合并器（`ontology` 包）

`ontology.Merger` 实现带删除回放的两段式倒排索引段合并，全部方法可用一把
互斥锁并发调用，结果等价于某个合法的串行顺序。

**段与统计量**
- 段由 `Register([]Doc)` 登记一批文档产生；批内局部编号按给出顺序从 0 开始。
- 键在全部存活文档中唯一；已删除的键可再次登记。`Delete(key)` 只给该键当前
  存活文档打删除标记，不改段的其余内容，因此删除可在合并进行期间到达且不丢失。
- `Stats`：`MaxDoc`（局部编号总数，含已删除）、`NumDocs`（存活数）、
  `TermCount`（存活文档中出现过的不同词项数）。
- `Postings(segID, term)`：存活文档按局部编号升序的 `(DocID, TF, Positions)`，
  词项不存在时返回空倒排表；返回值与 `Stats` 均为深拷贝。

**两步合并与删除回放**
- `BeginMerge(ids)` 在调用时刻冻结合并集合：按 `ids` 的段次序、段内局部编号
  升序，取各段此刻存活的文档重新编号为 `0..m-1`，返回自增句柄号（从 1 起）；
  输入段随即标记为忙，不能再参与其它未结束合并。
- `Commit(handle)` 生成新段：`MaxDoc = m`（冻结编号全部保留，即使文档之后被
  删除）；某冻结文档若在 BeginMerge 之后、Commit 之前被删除，则保留其编号并
  标记为已删除。删除与否以被冻结的那个文档实例为准，与之后同键重新登记的新
  文档无关——旧文档不会复活，新文档留在其登记段中。倒排表、`df`、`TermCount`
  只按 Commit 时刻仍存活的文档计算。提交成功后输入段移除，新段取下一个段编号。
- `Abort(handle)` 放弃合并，输入段恢复可合并状态。
- 没有合并期间删除时，Commit 结果与把同一冻结文档序列直接 `Register` 成一个
  段的朴素结果在 Stats 与全部倒排表上逐字段相同；若 BeginMerge 时所有文档都
  已删除，则冻结序列为空，新段 `MaxDoc = 0`。

**编号分配**：段编号从 1 起自增，登记与合并提交各取下一个编号；句柄号从 1
起自增。被拒绝的操作不消耗任何编号。

**拒绝原因（按所列顺序只报第一个，可区分）**
- 登记：`ErrEmptyBatch` → `ErrEmptyKey` → `ErrEmptyTerm`（参数非法）→
  `ErrDuplicateKey`（与存活文档或批内其它键冲突）。
- BeginMerge：`ErrInvalidMerge`（少于 2 个或含重复编号）→
  `ErrSegmentNotFound`（按 ids 次序的首个不存在/已移除段）→
  `ErrSegmentBusy`（按 ids 次序的首个忙段）。
- Delete：`ErrKeyNotFound`；Commit/Abort：`ErrInvalidHandle`（未知、已提交、
  已放弃）；Stats/Postings：`ErrSegmentNotFound`。

**本地验证**

```bash
# 全量测试（含竞态检测）
go test -race ./...

# 2000 组随机操作序列与朴素参考模型逐项对照，
# 输入/输出/判定依据写入 ontology/fuzz_operations.log
go test -run TestRandomDifferential2000 -v ./ontology
grep -c MISMATCH ontology/fuzz_operations.log   # 期望 0
```

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
