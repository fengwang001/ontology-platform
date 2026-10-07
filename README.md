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

## 属性索引增量维护子系统

`ontology/`（包 `ontologyindex`）实现消费属性变更流的倒排索引增量维护：

- 合法串行顺序由事件的逻辑生效时刻 `(EffectiveAt, EventID)` 唯一确定，
  与物理到达顺序无关；重复投递按 `EventID` 幂等去重。
- 索引依据字段随类型版本迁移改名/替换时，以 `BeginSwitch(cutoverAt)`
  宣布的逻辑时刻为界做**原子**版本切换；切换中查询返回
  `ErrSwitchInProgress`，查询端不可能看到新旧依据混杂。
- 新版本约束校验失败整体回滚，切换窗口缓冲的事件从同一份事实日志重放，
  重归入旧版本继续生效，不丢事件。
- 四类错误互分且按 E1(废弃无替代) > E2(切换校验失败) > E3(属性未定义)
  > E4(切换中查询) 的优先级只报告其一。
- 单把读写锁线性化所有并发操作；查询为哈希定位（平均 O(1)，与累计
  事件总数无关）。
- 独立朴素批量重建模型 + 随机差分测试逐条对照；每次判定均有审计记录。

详见 `docs/design.md`（关键取舍/被放弃方案）与 `docs/testing.md`（测试矩阵）。

```bash
# 全量测试（含竞态检测）
go test -race -count=1 ./...

# 端到端演示
go run ./cmd/indexdemo

# 查询复杂度自动断言（较慢）与基准
ONT_RUN_SLOW=1 go test -run TestLookupConstantTime -v ./ontology
go test -bench=BenchmarkLookup -benchmem ./ontology
```
