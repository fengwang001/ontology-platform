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

## 审计日志（auditlog 包）

`auditlog` 是带外部锚点的哈希链审计日志校验器。每条记录含序号、负载、
前摘要与自报摘要：序号从 1 连续，首条前摘要为固定创世值
（`GenesisDigest`），摘要由外部提供的确定性函数
（`DigestFunc`，默认为 SHA-256 实现）对（序号，负载，前摘要）算出。

### 追加与锚点

- `Append` 是原子操作：序号为锚点间隔 A 的倍数的记录追加成功时，同一
  原子操作内把（序号，摘要）发布到外部锚点存储（`AnchorStore`，只增
  不改）。因此任一时刻，链长以内每个 A 的倍数序号都已有锚点。
- 创建时 A 非正拒绝（`ErrNonPositiveInterval`）；追加时按顺序只报第一
  个拒绝原因：负载为空（`ErrEmptyPayload`）、负载超过长度上限
  （`ErrPayloadTooLarge`）。被拒绝的追加不占用序号、不改变链与锚点。
- 追加与校验均可并发调用：任意交错下序号连续无重复无空洞，并发校验
  看到的链恒为某个自洽前缀；相同追加序列得到相同的链与锚点。

### 校验判定顺序

`Verify`（或纯函数 `VerifyChain`）按以下顺序判定：

1. 逐条检查第 i 条（i 从 1 起），同一条多项成立时按此优先级，取最小的 i：
   - 摘要重算与自报不符 → 「内容被改」
   - 序号不等于 i → 「序号不连续」
   - 前摘要不等于上一条自报摘要（首条对创世值）→ 「链断裂」
2. 锚点核对：
   - 链长小于最大锚点序号 → 「尾部截断」，报链长加一
   - 某锚点摘要与链中同序号记录的自报摘要不符 → 「与锚点不符」，报该
     锚点序号（多个不符取最小序号）
3. 两部分都有问题时报位置较小者；位置相同取逐条检查的类别。
4. 都无问题则通过，并报告已被锚点确认的前缀长度（最大锚点序号）。

### 锚点的作用与无法发现的情形

逐条检查只能发现未重算摘要的篡改。攻击者篡改某条记录后重算其后全部
摘要与前摘要即可绕过逐条检查，但外部锚点只增不改，仍记录着原始摘要，
因此「与锚点不符」能抓住这类重算绕过；锚点也让「尾部截断」可被发现。

无法发现的情形：截断后链长仍不小于最大锚点序号时，剩余前缀逐条检查
与锚点核对均一致，按通过处理。要缩短可检测的截断窗口，应减小锚点间
隔 A。

### 本地验证

```bash
# 全部测试（含并发追加与竞态检测）
go test -race -v ./auditlog

# 测试日志会打印每个场景的输入、输出与判定依据
go test -v ./auditlog -run TestTamperedPayload
```
