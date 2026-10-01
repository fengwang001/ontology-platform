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

## LSH 近似最近邻检索（`lsh` 包）

基于随机超平面签名的近似最近邻检索器，向量维度固定，相似度为余弦。

### 签名规则

- 创建索引时按种子确定性地生成 `maxTables x maxBits` 个超平面法向量（标准正态，`math/rand` 同一源码序列），同一种子得到逐字节相同的超平面。
- 签名第 `j` 位：向量与第 `j` 个法向量点积 `>= 0` 取 `1`，否则取 `0`（点积恰为零取 `1`）。
- 签名为 `uint64`，位数上限 `MaxBits = 64`。

### 表与位的嵌套关系

- 表数为 `L` 时所用的表，恰为更大表数配置下的前 `L` 张表。
- 位数为 `b` 时所用的超平面，恰为更多位数配置下每张表的前 `b` 个。
- 因此候选集合随表数单调扩大、随位数单调缩小，召回率随表数单调不降、候选数随位数单调不增。

### 候选与精排规则

- 候选：前 `L` 张表中与查询同桶（前 `b` 位签名相等）向量的去重并集。
- 精排：对候选逐一计算精确余弦，按余弦降序取前 `K`，并列按编号升序；候选为空时返回空结果。
- `Stats` 报告候选数 `Candidates` 与精排计算次数 `Reranked`。

### 错误处理

维度不符、零向量、编号重复、删除不存在的编号、表数/位数/K 非正、位数超上限均整体拒绝，
分别返回 `ErrDimensionMismatch`、`ErrZeroVector`、`ErrDuplicateID`、`ErrNotFound`、
`ErrInvalidTables`、`ErrInvalidBits`、`ErrInvalidK`、`ErrInvalidDim`（可用 `errors.Is` 区分），
被拒绝的操作不改变任何桶。插入、删除、查询均可并发调用（`sync.RWMutex` 保护）。

### 本地验证

```bash
# 全部测试（含与暴力精确检索的召回率对照、单调性、并列、非法输入、并发）
go test -race -v ./lsh/

# 仅召回率对照（表数 1、2、4、8）
go test ./lsh/ -run TestRecallMonotonicInTables -v
```
