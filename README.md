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

## lsh 包：随机超平面近似最近邻检索

`lsh` 包实现基于随机超平面签名的近似最近邻（ANN）检索器，向量维度固定，
相似度为余弦。

### 签名规则

- 索引由 `L` 张哈希表组成，每张表使用 `b` 个超平面（`1 <= b <= 64`）。
- 第 `t` 张表第 `p` 个超平面的法向量由 `(seed, t, p)` 经 splitmix64 确定性
  生成，分量取自整数区间 `[-128, 127]`；与整数向量的点积为精确整数，
  符号判定无浮点误差。
- 向量 `v` 在第 `t` 张表的签名是一个 `b` 位位图：第 `p` 位为 1 当且仅当
  `v` 与第 `p` 个法向量的点积 **>= 0**（点积恰为零取 1），否则为 0。

### 表与位的嵌套关系

法向量只取决于 `(seed, t, p)`，与 `L`、`b` 无关，因此：

- 表数为 `L` 时所用的表，恰为更大表数下的前 `L` 张；
- 位数为 `b` 时所用的超平面，恰为更多位数下的前 `b` 个。

推论：固定种子下，增大 `L` 候选集合只增不减（召回率单调不降），
增大 `b` 候选集合只减不增（候选数单调不增），测试对此做了实测验证。

### 候选与精排规则

- 候选为各表中与查询向量同桶（签名相同）的向量的**去重并集**。
- 对候选逐一计算精确余弦，按余弦**降序**排列，余弦并列时按**编号升序**，
  取前 `K` 个；候选为空时返回空结果。
- 每次查询通过 `Stats` 报告候选数 `Candidates` 与精排计算次数
  `RerankCount`（二者相等）。

### 错误与并发

- 维度不符、零向量、编号重复、删除不存在的编号、表数/位数/K 非正、
  位数超上限均整体拒绝，返回可用 `errors.Is` 区分的哨兵错误
  （如 `lsh.ErrDimMismatch`、`lsh.ErrZeroVector` 等），被拒绝的操作不改变任何桶。
- `Insert`/`Delete`/`Query` 均可并发调用（内部读写锁保护）；
  同一种子与同一操作序列得到逐字节相同的签名与结果。

### 本地验证

```bash
# 全部测试（含与暴力精确检索的召回率对照、单调性、非法输入、并发）
go test ./lsh -v

# 竞态检测
go test ./lsh -race
```

测试日志会打印每个用例的输入、输出与判定依据（`-v` 可见）。
