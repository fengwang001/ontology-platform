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

## 审计日志校验器（`audit` 包）

带外部锚点的哈希链审计日志校验器，支持并发追加与并发校验。

### 链结构

- 每条记录含序号、负载、前摘要与自报摘要；序号从 1 连续递增，首条前摘要为固定创世值 `GenesisDigest`。
- 自报摘要由外部提供的确定性函数对（序号，负载，前摘要）算出，包内提供 `SHA256Hash` 作为参考实现。
- 追加是原子操作；序号为 A 的倍数的记录追加成功时，同一原子操作内把（序号，摘要）发布到外部锚点存储，锚点只增不改。
- 创建时 A 非正拒绝；追加时负载为空、负载超长按此顺序只报第一个原因（`ErrEmptyPayload` / `ErrPayloadTooLarge`），被拒绝的追加不占序号、不改链与锚点。

### 校验判定顺序

校验分两段，先逐条检查链，再核对锚点：

1. **逐条检查**：对第 i 条依次判定，同一条多项成立时按此优先级取第一个类别，并取最小的 i：
   1. `内容被改`（ContentTampered）：摘要重算与自报不符；
   2. `序号不连续`（SeqDiscontinuous）：序号不等于 i；
   3. `链断裂`（ChainBroken）：前摘要不等于上一条自报摘要（首条对创世值）。
2. **锚点核对**：链长小于最大锚点序号为 `尾部截断`（TailTruncated），报链长加一；某锚点摘要与链中同序号记录的自报摘要不符为 `与锚点不符`（AnchorMismatch），报该锚点序号。
3. **合并**：两段都有问题时报位置较小者；位置相同取逐条检查的类别；都无问题则通过，并报告已被锚点确认的前缀长度（最大锚点序号）。

### 锚点的作用与无法发现的情形

锚点是发布到外部只增存储的（序号，摘要）检查点。攻击者即使改动一条记录后**重算整条后缀**绕过逐条检查，也无法改写已发布的外部锚点，因而被 `与锚点不符` 抓住；尾部被截断到最大锚点序号之前会被 `尾部截断` 抓住。

**无法发现的情形**：截断后链长仍不小于最大锚点序号（即只删掉最后一个锚点之后的尾部记录），按通过处理。缩小 A 可降低该盲区的长度。

### 并发与确定性

- 追加与校验可任意并发交错：序号连续无重复无空洞，任一时刻链长以内每个 A 的倍数序号都已有锚点，并发校验看到的链恒为某个自洽前缀。
- 相同的追加序列得到相同的链与锚点。

### 本地验证

```bash
# 全部测试（含竞态检测与详细日志，日志含输入、输出与判定依据）
go test -race -v ./audit

# 指定场景
go test -run TestRecomputedSuffixCaughtByAnchor -v ./audit
```
