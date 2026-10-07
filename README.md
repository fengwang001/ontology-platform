# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统：对象类型版本迁移与运行中实例双写回填

`ontology/` 包实现对象类型结构变更期间，存量实例异步回填、新旧两种版本
读写同时正确的子系统，由三个模块协作：

- 版本声明与兼容性判定（`ontology/declaration.go`）
- 存量实例异步回填队列（`ontology/backfill.go`）
- 读写路由与一致性仲裁（`ontology/store.go`）

关键性质：旧结构写入先等价转换为新结构写入；未回填实例新读即时现算；
回填与写入/删除竞争时识别并跳过（不覆盖、不复活、不报错）；已生效的属性
对应关系冻结不可修改；全部操作在单锁下全局串行化；未回填实例新读开销仅与
实例自身属性数相关，与历史声明变更次数无关。

完整设计（含关键取舍、被放弃方案、复杂度证明与验证方法）见
[`docs/DESIGN.md`](docs/DESIGN.md)。

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

# 查看逐条"输入/实际输出/判定依据"
go test ./ontology/ -v

# 随机差分（朴素重算模型对照）与重放确定性
go test ./ontology/ -run 'TestRandomDifferential|TestReplayDeterministic' -v

# 视图复杂度证明（历史修订次数不影响现算开销）
go test ./ontology/ -run TestReadViewCost -v
go test ./ontology/ -run '^$' -bench . -benchmem

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

> 若 `GOCACHE` 位于只读文件系统，可 `export GOCACHE=/tmp/gocache`。
