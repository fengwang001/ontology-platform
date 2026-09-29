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

## ranking 包：排名函数的增量维护器

`ranking.Leaderboard` 在插入与删除下增量维护每个元素的三元组
（位次、排名、稠密排名），可并发使用。

### 三种排名的定义

排序键为**先分数降序，分数相同再按唯一标识升序**，由此得到全序。

- **位次（Position）**：元素在全序中从一起的位次。每个元素互不相同，
  全部位次构成 `[1, n]` 上的双射。
- **排名（Rank）**：严格高于该元素的元素个数加一。同分元素共享同一
  排名，并列会产生空洞（如三人并列第一，下一名排名为 4）。
- **稠密排名（DenseRank）**：严格高于该元素的互异分数个数加一。
  同分元素共享同一稠密排名，且无空洞（如三人并列第一，下一名稠密排名为 2）。

### 并列与空洞规则

- 插入一个元素后，所有分数严格更低的元素位次与排名都加一；稠密排名
  仅当新元素引入了一个新的更高分数取值时才加一。
- 删除对称：所有分数严格更低的元素位次与排名都减一；稠密排名仅当
  被删元素是其分数上的最后一个元素时才减一。

### 非法输入

三类非法输入被互不相同的哨兵错误拒绝，可用 `errors.Is` 判定，
失败不改变任何元素的三元组，被拒后仍可继续正常使用：

- `ranking.ErrEmptyID`：标识为空（插入与删除均校验）。
- `ranking.ErrDuplicateID`：插入已存在的标识。
- `ranking.ErrIDNotFound`：删除不存在的标识。

### 并发保证

查询（`Get`/`Len`）与自检（`SelfCheck`）使用读锁，可与并发插入
安全混用；并发插入互异元素后，位次在 `[1, n]` 上构成双射。

### 本地验证

```bash
# 运行 ranking 包全部测试（日志打印输入、结果与判定依据）
go test -v ./ranking

# 带竞态检测
go test -race -v ./ranking
```
