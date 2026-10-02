# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 证书路径构建与验证器（`ontology` 包）

`ontology.Validator` 支持证书登记（`Add`）、信任锚标记（`Trust`）、
吊销登记（`Revoke`）与路径验证（`Verify`）。构造参数为链长上限 `L`
（路径中证书个数，1..16）与登记数上限 `Nmax`（1..100000），越界整体拒绝。

### 签发者候选次序与深度优先发现

- 证书 `c` 的签发者候选为已登记且 `Subject == c.Issuer`、
  `Key == c.AuthKey`、`ID != c.ID` 的证书，按 `Subject` 建索引，
  按 `NotAfter` 降序、`NotAfter` 相同时按 `ID` 字节序升序排列。
- `Verify(leafID, name, now)` 从终端证书出发按候选次序深度优先：
  当前证书是信任锚则路径在此终止（锚不再向上找签发者），否则逐个深入候选。
- 同一 ID 不得在一条路径重复出现（该候选计一次后跳过）；
  路径证书数达到 `L` 后取出的候选计一次并剪掉。
- 每走到信任锚得到一条结构路径，按发现次序逐条检验；第一条通过全部
  检查的路径立即返回，之后的候选不再取出（惰性枚举）。

### 固定检验次序与失败归因

对路径 `c0`（终端）、`c1`、…、`cn`（锚）按以下顺序，首个违规即
该路径的失败原因并带出证书下标；所有结构路径都失败时，结果取第一条
被发现路径的原因与该路径 ID 列表：

1. 下标 0..n 逐个查有效期（`[NotBefore, NotAfter)` 左闭右开）；
   全部通过后再逐个查吊销（`now >= at`）。
2. 下标 1..n 必须 `IsCA`。
3. 下标 1..n 中 `PathLen >= 0` 的证书，统计下标 1..i-1 里
   **非自签发**（`Subject != Issuer`）证书数 `m`，要求 `m <= PathLen`；
   信任锚自身的 `PathLen` 同样参与判定。
4. 下标 1..n 逐个检查域名约束：先 `Excluded`，命中即违规；
   再 `Permitted`（非空且全部不命中即违规）。
5. `name` 必须被 `c0` 的某条 SAN 匹配。

### 路径长度与域名匹配规则

- 子树约束 `s` 不以点开头：`name == s` 或以 `.s` 结尾（按标签边界，
  `notexample.com` 不命中 `example.com`）。
- `s` 以点开头：仅以 `s` 结尾的真子域命中（去掉前导点后与 `name`
  相等不命中）。
- SAN `*.s`：`name` 须恰为一个不含点的标签加 `.s`；
  非通配 SAN 须与 `name` 逐字节相等。

### 吊销与并发语义

- `Revoke(id, at)` 幂等登记；已有吊销时刻时保留较小值（只能提前）。
- 所有方法可并发调用：写操作互斥，`Verify` 在读锁下读取一致快照，
  结果等价于某个串行顺序，重放结果完全相同。
- `VerifyResult.Considered` 为非导出计数器对外的只读呈现：每次从候选
  列表取出一个候选计一次（含重复 ID 跳过、超长剪掉、深入后无路径），
  终端证书不计，走到信任锚不再取候选；同一证书在不同分支各计各的。

### 拒绝类别

`OpError.Kind` 区分：`KindInvalidConfig`（参数/字段越界、非法字符串、
`at`/`now`/`name` 越界）、`KindNotFound`（ID 不存在）、
`KindConflict`（ID 已存在）、`KindLimitExceeded`（将超 `Nmax`），
按此顺序只报第一个；被拒绝的操作不改变任何状态。
`Verify` 通过入参检查后，无结构路径报 `NoPath`，有结构路径但无一通过
报 `Failure`（原因枚举、证书下标、路径 ID 列表）。

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

证书验证器的测试包含：有效期/吊销边界、候选排序与回溯、非 CA、
PathLen 0/1 与自签发计数、子树标签边界与前导点、通配单标签、
交叉签名环、链长 L/L+1、终端即锚、拒绝不改状态、考察数稳定性，
以及与朴素全枚举模型的 2000 组随机序列对照：

```bash
go test -race ./...
# 打印随机对照的输入、输出与判定依据
DIFF_LOG=1 go test ./ontology -run TestNaiveDifferential -v
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
