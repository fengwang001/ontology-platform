# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## CDC 事件确定性令牌化脱敏（`tokenize` 包）

`tokenize` 包把变更数据捕获（CDC）事件中的敏感列替换为**按域（domain）分配**的确定性令牌。

### 域作用域

- 每个敏感列在配置中归属一个域；同一张域令牌表被所有引用该域的表共享。
- 同一原值在同域内跨表、跨事件永远得到同一令牌；不同原值得到不同令牌。
- 不同域互不影响，各自独立编号。
- 令牌格式为 `<域名>-<首次出现顺序编号>`，编号从 1 开始，连续无空洞。

### 出现顺序

令牌的“首次出现”按以下确定性顺序排列：

1. 事件按到达顺序（`Transform` 调用顺序）处理；
2. 同一事件内先处理变更前镜像 `Before`，再处理变更后镜像 `After`，两者共用同一令牌表；
3. 同一镜像内按列名字节序（`sort.Strings`）逐列处理。

因此给定相同的事件序列，任意多次独立运行都会得到逐字节一致的令牌结果。

### 空值处理

- 敏感列值为 `nil`（空值）时结果仍为 `nil`，**不分配令牌、不占用编号**。
- 空字符串 `""` 是普通值，正常分配令牌。
- 非敏感列原样透传；脱敏始终返回新构造的事件与镜像，不会修改调用方传入的事件。

### 并发与原子性

- `Tokenizer` 可被多个 goroutine 并发调用，内部以互斥锁串行化提交。
- 每次调用对令牌表的影响具有整事件原子性：事件内新分配的令牌先进入暂存区，
  仅当整条事件（前后两个镜像）处理成功时才一次性提交。
- 处理中途被拒绝时，本次事件内已暂存的分配全部撤销，已提交令牌表与各域下一编号均不改变（失败不留痕）。

### 边界与错误类别

错误通过 `*tokenize.Error` 返回，`tokenize.KindOf(err)` 可取到四类互斥、可区分的原因：

| 类别 (`ErrorKind`) | 触发场景 |
| --- | --- |
| `KindInvalidConfig` | 无域/无表、域名或表名为空、`MaxTokens <= 0`、敏感列引用未定义域、列名或域名为空 |
| `KindUnknownTable` | 事件的表未在配置中声明 |
| `KindInvalidEvent` | `Before` 与 `After` 同时为 `nil`；敏感列值既非 `nil` 也非 `string` |
| `KindTokenLimitExceeded` | 某域已提交令牌数 + 本事件暂存数达到该域 `MaxTokens`，无法再为新原值分配 |

其他边界说明：镜像中缺少的敏感列保持“缺少”（不会补列）；镜像为 `nil` 表示该镜像不存在。

### 本地验证方法

```bash
# 全量测试（含朴素参照随机一致性、超限撤销、并发与可复现性）
go test ./...

# 竞态检测
go test -race ./tokenize

# 代码检查
gofmt -l .
go vet ./...
```

运行时通过传入的 `slog.Logger` 逐步记录：每个事件的输入（表、镜像是否存在）、
每列的判定依据（空值保留 / 已提交复用 / 本事件内暂存复用 / 首次出现及编号）、
分配到的令牌以及提交或拒绝结果。

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
