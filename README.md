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

## 变更捕获的模式演进列映射器（`evolution` 包）

把旧版本事件投影到当前模式：列按名字对齐，类型在整数与字符串之间做可判定转换，结果确定且可复现。

### 对齐与补位规则

- **按列名对齐，与位置无关**：事件的列顺序（map key 顺序）不影响结果，只按名字匹配目标模式中的列。
- **缺失列补零值**：事件缺少某目标列时，必填列（`Required: true`）→ 整体拒收（`required_column_missing`）；可选列 → 补该类型的零值（`int` 为 `int64(0)`，`string` 为 `""`）。
- **类型转换**：`int -> string` 总成功（`strconv.FormatInt`）；`string -> int` 仅当内容能被 `strconv.ParseInt(base=10, bitSize=64)` 完整解析为合法整数时成功，否则整体拒收（`conversion_failed`）。同类型直接复制。
- **已删除列静默丢弃**：事件里有、目标模式里没有的列不出现在结果中，但会在逐列报告里以 `dropped` 记录。
- **整体拒绝**：任一列失败则整个事件拒收，不返回半成品行。

### 拒收原因（`RejectReason`，可区分）

| Reason | 触发条件 |
| --- | --- |
| `invalid_type` | 列声明了不支持的类型（仅支持 `int`、`string`） |
| `empty_column_name` | 列名为空 |
| `duplicate_column` | 同一模式中列名重复 |
| `version_exists` / `no_current_version` | 版本号已存在 / 尚未注册初始模式就演进 |
| `column_exists` / `unknown_column` | `add` 已存在的列 / `update`、`delete` 不存在的列 |
| `invalid_change_op` | 变更操作不是 `add`/`update`/`delete` |
| `version_not_registered` | 查询或事件引用了未注册的版本 |
| `required_column_missing` | 必填列在事件中缺失 |
| `conversion_failed` | 字符串内容不是合法整数 |
| `invalid_value` | 事件值既不是整数也不是字符串 |

演进是原子的：`Evolve` 在私有工作副本上校验并应用整批操作，任一条失败则全部不生效，注册表保持原版本不变。

### 并发与确定性

- 每个版本是不可变快照 `*Snapshot`；注册表读用 `RLock`、演进用 `Lock`。
- `ProjectEvent` 在一次读锁内原子地固定"当前最新版本 + 校验事件版本已注册"，随后投影在锁外基于不可变快照执行。模式持续演进期间并发投影不会出现新旧模式混合，每行都严格对应某一完整版本。
- 同一版本、同一输入永远得到完全相同的结果行与逐列报告（目标列按模式顺序、丢弃列按名字排序）。

### 本地验证：逐列手工映射对照

测试日志会打印事件内容、目标模式、逐列决策（`aligned`/`converted`/`defaulted`/`dropped`/`rejected`）或错误，以及每列的判定依据（`basis`）。运行：

```bash
go test -race -v ./evolution/
```

核对方法：对每个用例，拿一张纸逐列手工映射，再与日志对照——

1. 在事件中按**名字**找该列（忽略顺序）。
2. 找不到：必填 → 期望 `rejected` + `required_column_missing`；可选 → 期望 `defaulted` 且 `out` 为零值。
3. 找到：类型相同 → `aligned` 原值；`int→string` → `converted` 且结果为十进制文本；`string→int` → 内容合法则为 `int64`，否则 `rejected` + `conversion_failed` 且整个事件拒收。
4. 目标模式没有的事件列 → `dropped`，结果行中不存在。

`TestDeterminism` 会把同一事件投影 20 次并逐字段比较结果行和报告；`TestConcurrentProjectionDuringEvolution` 在持续加列的同时用 `-race` 校验每个投影行恰好完整对应其所固定的版本。
