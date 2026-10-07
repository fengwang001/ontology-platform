# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 批量导入属性级权限守门器

`importguard` 包实现批量导入在写入前的属性级权限与必需属性守门：

- 整批统一声明原子 / 宽松模式；原子模式任一字段无权限则整条记录零写入失败，
  宽松模式跳过无权限字段并对必需属性二次判定（仅更新语义可沿用旧值）。
- 逐记录报告成功 / 部分成功（列出跳过字段）/ 失败（分类化原因），记录间互不
  传染；创建与更新语义分别适用规则。
- 权限判定取自批起始快照（深拷贝 + 有效权限折叠索引），执行期间权限变更对本批
  不可见；单字段判定为一次 map 查找，单记录访问数等于字段数，与批大小和权限
  历史规模无关。
- 每批一个全局临界区、批内并发判定 + 按序串行提交，最终结果等价于某个全局串行
  顺序。
- 每次判定的输入、输出与依据通过 `DecisionLogger` 打印。

设计取舍、放弃的方案与验证方法见 `importguard/DESIGN.md`；快速验证：

```bash
go test -race -v ./importguard/
```

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
