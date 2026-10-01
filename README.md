# ontology-platform

本体服务平台。当前 Go 包提供带版本历史与回收水位的并发安全增量 BM25 变体排名器。

## 环境要求

- Go 1.26+（`go version` 确认）
- 若系统缓存目录只读，可设置 `GOCACHE=/tmp/go-cache`

## 排名器

创建索引：

```go
idx := ontology.NewIndex()
```

### 操作与版本

- `Add(docID, terms)`：登记新文档；`docID` 已存在时拒绝。
- `Update(docID, terms)`：原子替换已有文档的完整词项序列。
- `Delete(docID)`：删除已有文档。
- `Compact(keep)`：回收小于 `keep` 的历史版本，并把水位 `W` 移到 `keep`。
- `Search(query, k, asOf)`：查询版本 `asOf` 的不可变状态，返回前 `k` 项。

空索引为版本 `0`。每次成功的 `Add`、`Update`、`Delete` 都让当前版本 `V` 增加 1；被拒绝的操作不消耗版本，也不修改任何状态。`Compact` 只改变可查询历史范围，不改变 `V` 与当前文档状态。

### 分数公式

查询词先去重。对版本 `asOf`，设：

- `N`：当前版本文档数
- `L`：当前版本文档长度总和
- `dl`：文档长度
- `tf`：词项在该文档中的出现次数
- `df`：包含该词项的文档数
- `avgdl = L/N`

对文档中出现的每个查询词，累加：

```text
idf(t) * tf * 5/2 / (tf + 3/2 * (1/4 + 3/4 * dl/avgdl))
idf(t) = (N - df + 1/2) / (df + 1/2)
```

固定 `k1=3/2`、`b=3/4`。IDF 不取对数，因此即使 `df=N` 也为正的 `1/(2N+1)`。实现使用 `math/big.Rat` 保存中间统计和分数，输出前转为既约文本 `分子/分母`。

不含任何查询词的文档不返回；结果先按分数降序，分数并列时按 `docID` 的字节序升序。

### 参数与版本错误

错误按以下顺序只返回第一个原因：

- 参数非法：空 `docID`、空 `terms`、包含空词项；搜索时空查询、包含空词项或 `k < 1`。
- 重复文档：`Add` 已存在的 `docID`。
- 文档不存在：`Update` 或 `Delete` 不存在的 `docID`。
- 版本越界：`Compact` 的 `keep < 0` 或 `keep > V`；`Search` 的 `asOf < 0` 或 `asOf > V`。
- 水位回退：`Compact` 的 `keep < W`。
- 版本已回收：`Search` 的 `asOf < W`。

负数 `asOf` 同时小于水位时先报版本越界。`asOf=W` 可查，`W-1` 不可查；`keep=W` 是成功的空操作。索引为空或目标版本文档数为 0 时，搜索返回空结果而非错误。

### 增量统计与并发

索引增量维护每个版本的 `(N,L)`、每个词项的 `df` 变化点、每个文档的内容变化点，以及词项—文档倒排存在性变化点。查询按 `asOf` 读取一致快照；写操作使用互斥锁整体提交，查询使用读锁，因此并发调用等价于某个合法串行顺序，`Update` 不会暴露新旧内容混杂状态。

当前版本 `asOf=V` 的搜索会更新包内非导出计数器，值恰好为各去重查询词当前倒排表长度之和。测试在 1000 与 100000 篇文档、命中倒排长度同为 128 时验证该计数均为 128，不随无关文档增长。

`Compact(keep)` 以版本 `keep` 的状态重建基线并截断更早变化点；水位及以上版本在回收前后查询结果不变。

## 本地验证

```bash
# 普通全量测试
GOCACHE=/tmp/go-cache go test ./...

# 竞态检测
GOCACHE=/tmp/go-cache go test -race ./...

# 2000 组随机 Add/Update/Delete/Compact/Search 对拍（含输入、输出、判定依据日志）
GOCACHE=/tmp/go-cache go test -run TestRandomDifferential2000 -v ./...

# 1000 / 100000 文档两档倒排读取计数
GOCACHE=/tmp/go-cache go test -run TestCurrentPostingReadsUnchangedByIrrelevantDocs -v ./...

# 格式化与静态检查
GOCACHE=/tmp/go-cache gofmt -w *.go
GOCACHE=/tmp/go-cache go vet ./...
```

随机对拍会把每个操作序列同时交给增量索引和朴素实现；朴素实现从目标版本的完整文档集合重算 `N`、`L`、`df`、每篇文档的 `tf/dl`、精确分数、并列顺序和截断结果，逐项相同才判定通过。
