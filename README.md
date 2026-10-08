# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组成

- `ontology/`：继承权限与实例乐观并发写入协调器（核心库）。
  - 单父类型继承树，权限规则声明在「类型 × 属性」上，子类型可显式覆盖；
  - 实例写入携带期望版本号做乐观并发控制，拒绝优先级：
    对象不存在 > 版本冲突 > 继承链权限拒绝 > 必需属性缺失；
  - 全部操作在单把互斥锁下完成，并发交错等价于某个全局串行顺序；
  - 每次判定记录输入 / 输出 / 依据到判定日志。
- `cmd/server/`：演示入口，构造类型层级并打印判定日志。
- `docs/DESIGN.md`：设计说明（关键取舍、被放弃的方案、本地验证方法）。

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
