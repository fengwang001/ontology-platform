# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## CDC 事件确定性令牌化脱敏（`ontology` 包）

`ontology.Tokenizer` 把变更数据捕获（CDC）事件中配置为敏感的列替换为**按域分配的确定性令牌**，供下游在不接触原值的前提下做跨表关联。

### 域作用域

- 配置由「域」和「敏感列绑定」组成：`Config.Domains` 声明域及令牌上限，`Config.Columns` 把 `(表, 列)` 绑定到某个域。
- 令牌作用域为域：**同一原值在同一域内、跨表、跨事件永远得到同一令牌**；不同域相互独立，不同原值令牌互不相同。
- 令牌格式为 `<域名>-<域内编号>`，编号从 `1` 开始且**连续无空洞**（如 `email-1`、`email-2`）。

### 出现顺序（编号如何确定）

编号按原值**首次出现**的顺序分配，次序规则固定：

1. 按事件到达顺序；
2. 同一事件内先处理变更前镜像（`Before`），再处理变更后镜像（`After`），两个镜像共用同一张令牌表；
3. 同一镜像内按**列名字节序**（UTF-8 字节序，等价于 Go 的 `sort.Strings`）逐列处理；
4. 同一事件内前面位置首次分配的值，在后面位置（含另一镜像）复用时不另占编号。

### 空值与空串

- 列值为 `nil`（SQL NULL）时**保持空值、不分配令牌**，即使该列是敏感列；
- 空字符串 `""` 是普通值，正常分配令牌（例如可能得到 `phone-1`），与 NULL 严格区分；
- 非敏感列（含未绑定的列）原样透传；`Before`/`After` 为 `nil` 表示该镜像不存在，原样保留；
- `Tokenize` 返回新建的事件，**绝不修改调用方传入的事件**。

支持的列值类型：`string`、`[]byte`、`int64`（及 `int`）、`float64`（及 `float32`）、`bool`、`nil`；其他类型属于非法事件。

### 边界与错误类别

所有拒绝都是**整事件原子**的：错误返回时该事件内已分配的编号全部逆序撤销，令牌表与各域下一编号恢复到事件开始前，失败不留痕。错误为 `*ontology.RejectError`，可用 `errors.Is` 按类别区分，且不同原因的错误信息互不相同：

| 哨兵错误 | Reason | 触发条件 |
| --- | --- | --- |
| `ErrInvalidConfig` | `invalid_config` | 无域/无敏感列、域名或域键为空、域键与 `Name` 不一致、上限为负、绑定表名/列名为空、绑定到未定义域 |
| `ErrUnknownTable` | `unknown_table` | 事件的表在配置中没有任何敏感列 |
| `ErrInvalidEvent` | `invalid_event` | 事件为 `nil`、表名为空、列名为空、列值类型不受支持 |
| `ErrTokenLimitExceeded` | `token_limit_exceeded` | 分配新编号时超过该域 `TokenLimit`（`0` 表示用 `DefaultTokenLimit = 1_000_000`） |

事件形状校验在触碰令牌状态之前完成；未知表判定在校验之后、加锁之后执行。`Tokenizer` 可被多个执行体并发调用，单把互斥锁保证每次事件对令牌表的影响整事件原子。

### 用法与日志

```go
tk, err := ontology.New(ontology.Config{
    Domains: map[string]ontology.Domain{
        "email": {Name: "email", TokenLimit: 100000},
    },
    Columns: map[ontology.ColumnRef]string{
        {Table: "users", Column: "email"}:      "email",
        {Table: "accounts", Column: "contact"}: "email",
    },
}, ontology.WithLogger(os.Stderr)) // 可选：打印每步输入、令牌与判定依据

out, err := tk.Tokenize(&ontology.Event{
    Table:  "users",
    Before: ontology.Image{"email": "a@example.com"},
    After:  ontology.Image{"email": "a@example.com", "nickname": "ali"},
})
```

日志逐步记录 `assign`（首次出现）、`reuse basis=same-event|prior-events`（复用来源）、`pass-through`、`null-preserve`、`replace`、`commit`，以及拒绝时的 `rollback revoked=N state-unchanged=true`。

### 本地验证

```bash
# 全量测试（竞态检测 + 详细用例）
go test -race -v ./...

# 只跑脱敏包
go test -race ./ontology

# 格式与静态检查
gofmt -l .
go vet ./...
```

`ontology` 包测试覆盖：跨表同域同牌、出现顺序（事件 → 前镜像 → 后镜像 → 列名字节序）、NULL 与空串区分、超限整事件撤销且编号无空洞、非法配置/未知表/非法事件四类拒绝互不相同且拒绝后状态不变、调用方事件不被修改、并发调用下的双射与连续编号，并与包内的朴素参照实现（`naiveRef`）逐事件比对一致。

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
