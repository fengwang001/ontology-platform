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

## 证书路径构建与验证

证书路径构建器位于 `ontology` 包。`New(L, Nmax)` 创建验证器：

- `L` 是一条路径允许的证书个数上限，包含终端证书与信任锚，范围为 1 到 16。
- `Nmax` 是登记证书数上限，范围为 1 到 100000。
- `Add` 登记证书；非法字段先被拒绝，随后依次区分 ID 冲突与登记数超限。
- `Trust(id)` 幂等标记信任锚；`Revoke(id, at)` 记录吊销时刻，重复吊销保留较小时刻。
- 被拒绝的操作不会修改证书、信任锚或吊销状态。

### 候选与发现次序

证书 `c` 的签发者候选必须同时满足：

- `Subject == c.Issuer`
- `Key == c.AuthKey`
- 候选 ID 与 `c.ID` 不同

候选按 `NotAfter` 降序排列；`NotAfter` 相同则按 ID 字节序升序。候选以 `(Subject, Key)` 建索引，因此不相关 Subject 的登记数量不会影响验证考察数。

路径从终端证书开始深度优先枚举：

- 当前证书已是信任锚时立即终止，不再读取它的候选。
- 否则按上述候选顺序逐个深入，整个过程是惰性的。
- 同一 ID 不得在同一路径重复出现；重复候选仍从候选列表取出并计入考察数。
- 路径已有 `L` 张证书且当前证书不是信任锚时，其候选仍会被逐个取出计数，但不能继续深入。
- 每发现一条以信任锚结尾的结构路径，立即按固定顺序验证；第一条完全通过的路径就是结果，后续候选不再取出。
- 没有任何结构路径时结果为无路径；有结构路径但均失败时，失败原因取深度优先发现的第一条结构路径的原因，并带证书下标与完整路径 ID。

`VerifyResult.ExaminedCandidates()` 返回本次验证考察过的候选数。终端证书本身不计；同一张证书在不同分支被取出时分别计数。

### 固定检查顺序

对路径 `c0, c1, ..., cn`（`cn` 是信任锚）严格按以下顺序检查，第一个失败即确定原因：

1. 对所有下标 `i=0..n`，先检查有效期，再检查吊销：
   - 有效期为左闭右开区间：`NotBefore <= now < NotAfter`。
   - 已记录吊销时刻 `at` 且 `now >= at` 即吊销。
2. `c1..cn` 必须都是 CA。
3. 对 `c1..cn` 的非负 `PathLen`，统计 `c1..c(i-1)` 中 Subject 与 Issuer 不相等的非自签发证书数 `m`，要求 `m <= PathLen`；信任锚自身的 `PathLen` 也参与。
4. 对每张 `ci`（`i>=1`）按证书顺序先检查全部 `Excluded`，再检查 `Permitted`。
5. `name` 必须匹配 `c0` 的至少一条 SAN。

### DNS 匹配

请求名与普通 DNS 串只允许小写 ASCII 字母、数字、连字符和点，不能为空、不能有连续点或尾点。子树约束允许一个前导点。

- 非前导点子树 `s`：`name == s` 或 `name` 以 `.s` 结尾，因此按标签边界匹配，`notexample.com` 不命中 `example.com`。
- 前导点子树 `.s`：只有以 `.s` 结尾的真子域命中，`s` 本身不命中。
- 非通配 SAN：与 `name` 逐字节相等。
- 通配 SAN `*.s`：`name` 必须恰好是一个不含点的标签，后接 `.s`；`a.s` 命中，`a.b.s` 不命中。

### 并发与复现

所有公开操作都可并发调用。写操作通过互斥串行化；`Verify` 在读锁内取得证书、候选索引、信任锚和吊销时刻的一致快照，之后使用快照完成惰性枚举。相同的登记、信任和吊销序列重放会产生相同的候选顺序、路径、失败原因、失败路径与候选考察数。

### 本地验证

```bash
go test ./...
go test -race ./...
go test -run TestRandomSequencesAgainstNaiveSimulation -v ./ontology
```

最后一条命令运行 2000 组随机登记与验证序列，并在日志中打印输入、实际输出、独立朴素模拟器的结构路径、选择依据、失败归因与候选考察数。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
