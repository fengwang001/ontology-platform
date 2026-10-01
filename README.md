# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 短语邻近匹配器（`package ontology`）

`matcher.go` 实现带句界、总间隙上限与单步间隙上限的有序短语邻近匹配。

### 数据操作

- `NewIndex() *Index`：创建并发安全的索引（`sync.RWMutex` + 原子计数器）。
- `Add(docID string, terms []string) error`：登记文档；`terms` 下标即词位置（从 0 起），
  词项为非空字符串且可重复；保留词项 `"<S>"`（常量 `Boundary`）表示句界，占一个位置，
  每篇文档可含任意多个句界。
- `Delete(docID string) error`：删除文档；删除后以同一 `docID` 重新 `Add` 视为新文档。
- `Phrase(query []string, slop, maxGap, k int) ([]Result, error)`：短语查询。
  `Result{DocID, Freq, MinGap}`。

### 匹配定义

给定查询 `q1..qn`（1 ≤ n ≤ 8，不可含空串或 `"<S"`），一个匹配是严格递增位置序列
`p1 < p2 < … < pn`，满足：

- 文档第 `p_i` 个词项等于 `q_i`，同一位置不能被两个查询词共用；查询词必须按给定次序出现，
  不允许颠倒；
- 单步间隙 `p_{i+1} − p_i − 1 ≤ maxGap`（每一步都受限，因此不能一律取最近出现位置）；
- 总间隙 `Σ(p_{i+1} − p_i − 1) = pn − p1 − (n−1) ≤ slop`，只取决于首尾位置；
- 区间 `[p1, pn]` 内不含句界位置，匹配不得跨句（句界恰好在 `p1` 之前或 `pn` 之后不影响）。

### 计数与排序

- `freq`：存在至少一个匹配的**不同首位置 `p1`** 个数；首位置相同的多个匹配只计一次，
  首位置不同但相互重叠的匹配各自计一次。单词查询时 `freq` 等于该词词频。
- `minGap`：该文档全部匹配的总间隙最小值；单词查询恒为 0。
- `freq` 为 0 的文档不返回；排序为 `freq` 降序 → `minGap` 升序 → `docID` 字节序升序，
  取前 `k` 个。没有任何文档含全部查询词项不是错误，返回空结果。

### 拒绝原因（按此顺序只报第一个，且被拒绝操作不改变索引）

- `Add`：`ErrInvalidArguments`（`docID` 为空 / `terms` 为空 / 含空串）→
  `ErrDuplicateDoc`（`docID` 已存在）；
- `Delete`：`ErrDocNotFound`；
- `Phrase`：`ErrInvalidQuery`（`query` 为空 / 多于 8 个 / 含空串 / 含 `"<S"`）→
  `ErrInvalidGap`（`slop` 或 `maxGap` 不在 0..100）→ `ErrInvalidK`（`k < 1`）。

### 算法与读取计数

查询先按倒排表交集（从最稀疏词开始）得到含全部去重查询词的候选文档，再按句界切分文档：
对每个候选首位置，在句段内对各查询词做逐层可达性 DP（滑动窗口实现单步 `maxGap` 约束），
最后按首尾位置计算总间隙判定 `slop`，并对每个首位置只记录最小总间隙。

非导出原子计数器 `PostingReads()` 记录 `Phrase` 实际读取的倒排位置项数。重复查询词只读取
一次其倒排表，因此计数不超过「各**去重**查询词倒排位置长度之和」，且不随无关文档数增长：
在 1000 与 100000 篇文档、含查询词位置项数相同（各 50 篇命中文档，共 200 项）两档下，
计数均为 200（见 `TestPostingReadsScale` 与 `BenchmarkPostingReads1k/100k`）。

### 本地验证

```bash
# 全量测试（含竞态检测）
go test -race ./...

# 2000 组随机对拍（与逐首位置回溯的朴素实现比较；
# 输入、输出、判定依据写入 testdata/fuzz.log）
go test -run TestNaiveFuzz -v ./...

# 读取计数 1000 / 100000 档对比
go test -run TestPostingReadsScale -v ./...
go test -run XXX -bench BenchmarkPostingReads -benchtime=5x .

go vet ./...
gofmt -l .
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
