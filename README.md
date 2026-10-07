# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统：双时态链接历史一致性审计

`bitemporal/` 实现在双时态（有效时间 + 记录时间）链接记录上的历史回放与基数一致性审计：

- 正/反向关系严格互为镜像；对称链接一端记录即双向可见，单侧缺失显式报结构缺陷；
- 审计按记录时刻生效的基数约束版本分段，历史版本永不被覆盖或倒查；
- 四类审计错误固定优先级（区间矛盾 > 对象类型缺失 > 规则版本作废 > 结构镜像缺失），且绝不回写历史；
- 并发操作通过不可变快照 + 原子换版保证可串行化；
- 任意历史点回放为 `O(log^2 F + k)`，不随累计事实/规则版本数线性增长，并有探针可独立验证；
- 判定输入、依据版本与结论全部留痕；回放结果不泄露记录来源。

设计取舍、被放弃方案与复杂度论证见 `DESIGN.md`，使用方式见 `docs/bitemporal.md`。

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
