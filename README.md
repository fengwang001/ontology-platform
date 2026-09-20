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

## join：两表等值连接器

`join` 包对两张表（各为 `[]map[string]any`）按一组有序连接键做等值连接，
支持 `Inner` 与 `Left` 两种模式，只用标准库，状态在进程内存中。

```go
res, err := join.Join(left, right, []string{"id"}, join.Left)
// res.Rows：结果行；res.Stats：产出行数、最大单键展开、两类未匹配计数
```

核心语义（完整定义见 `join/doc.go` 的包文档）：

- **空键不自反**：键属性缺失、为 nil、或为 float64 NaN 即视为空，Inner 下
  永不匹配（两侧同缺也不算相等），Left 下左行照常输出；空键未匹配数
  （`Stats.LeftUnmatchedNull`）与"有值无匹配"（`Stats.LeftUnmatchedNoMatch`）
  分开统计。
- **重复键完整展开**：同键左 m 行、右 n 行恰好产出 m*n 行；
  `Stats.MatchedPairs` 等于逐键 m*n 之和，`Stats.MaxKeyExpansion` 为最大单键展开。
- **顺序确定**：按连接键逐列升序，同键内按行标识升序；行标识由行内容派生
  （属性名排序后按"名字+类型标签+规范值"递归拼接），与输入顺序无关。
- **键类型严格**：string/bool/int/int64/float64 可比；int64 与 float64 按数值
  相等判断，NaN 永不相等，+0.0 与 -0.0 相等；类型冲突返回可判定的
  `*join.KeyTypeError`（`errors.Is(err, join.ErrKeyTypeConflict)`）。
- **结果行无歧义**：右表非连接键属性一律以 `right.` 前缀输出，不覆盖左表
  同名属性；Left 未匹配行不含任何右表属性（逗号-ok 可辨认缺失）；结果行
  为深拷贝，与输入互不影响。

演示：`go run ./cmd/demo`（逐项打印 OK/FAIL 与总计，退出码 0）。
