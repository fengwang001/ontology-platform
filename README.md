# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## CDC 模式演进列映射器（`cdc` 包）

将旧版本事件按列名投影到当前（目标）模式，并做可判定的类型转换，
保证每个旧事件的投影结果始终正确且可复现。

### 投影判定规则

- **按列名对齐**：事件字段与目标模式列按名字匹配，与位置、顺序无关。
- **缺失列**：事件缺少目标模式中的某列时，
  该列为必填（`Required`）则整体拒收（`ErrMissingRequired`）；
  否则补该类型的零值（`int` 补 `0`，`string` 补 `""`）。
- **类型转换**：仅支持整数与字符串互转。
  整数转字符串总成功；字符串转整数仅当内容是合法的十进制整数
  （`strconv.ParseInt` 可解析）时成功，否则整体拒收该事件
  （`ErrConversionFailed`）。事件值类型不是 int/int64/string 时
  同样整体拒收（`ErrInvalidValue`）。
- **已删除列**：事件字段在目标模式中不存在时静默丢弃，不影响投影。
- **整体拒收**：任一列判定失败则整个事件被拒，不产生部分结果。

### 模式演进与错误判定

- 演进操作（`AddColumn` / `DropColumn` / `AlterColumn`）基于指定版本
  生成新版本，旧版本快照不可变；`Register` 注册完整模式。
- 下列情况整体拒绝且注册表保持不变，原因可用 `errors.Is` 区分：
  非法列类型（`ErrInvalidColumnType`）、空列名（`ErrEmptyColumnName`）、
  重名列（`ErrDuplicateColumn`）、新增已存在列（`ErrColumnExists`）、
  删改不存在的列（`ErrColumnNotFound`）、版本未注册
  （`ErrVersionNotFound`）。
- 所有方法可并发调用。投影先取目标版本的一次性不可变快照再执行，
  因此模式持续演进期间并发投影始终基于某一完整版本，
  不会出现新旧模式混合；同一版本同一输入的结果完全确定。

### 本地验证方法（逐列手工映射对照）

1. 运行 `go test -race -v ./cdc/`，每个用例会打印事件、目标模式、
   逐列的判定记录（动作 `keep`/`convert`/`zero-fill`/`drop`/`reject`
   及判定依据）或整体拒收的错误。
2. 对照核对：测试中的 `manualProject`（`cdc/project_test.go`）是一份
   独立于实现的手工逐列映射，按上述规则逐列计算期望结果；
   `checkAgainstManual` 对每次投影自动与手工映射逐一比对，
   任何一列不一致都会使测试失败。
3. 手工复核单个事件时，可对照日志中每列的 `reason` 字段逐条检查
   判定依据是否符合上述规则。

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
