# 小区停车场共享管理服务

Go 包 `parking` 提供月租车位错时共享、临停准入、腾让归位和确定性计费。

## 核心 API

- `NewManager(cfg Config) (*Manager, error)`：初始化公共/月租车位数量与费率。
- `RegisterMonthly(now, plate, start, end)`：按整数日闭开区间登记月租，返回固定车位。
- `RenewMonthly(now, plate, end)`：到期前或宽限期内续租并保留车位。
- `SetShare(now, ownerPlate, interval)`：设置日共享分钟区间，支持跨午夜。
- `Enter(now, plate)`：月租有效则归位/发起腾让，否则按公共车位优先、共享月租位其次入场。
- `Exit(now, plate)`：临停车结算费用；腾让完成后月租车自动归位。
- `Logs()`：返回逐步输入时间、动作、车牌、判定依据和是否拒绝。

## 一致性与复杂度

- 所有公开操作由互斥锁保护，结果等价于某个串行顺序。
- 固定错误次序：参数非法、时钟回退、对象不存在、状态非法、无可用车位、租约无效。
- 被拒绝操作不推进时钟，也不留下车辆或车位痕迹。
- 公共/月租空闲位使用 O(1) 取最小编号的有序空闲集合。
- 共享车位使用固定 1440 个分钟桶；分配只查看当前分钟，不扫描车位总数。

## 验证

```bash
GOCACHE=/tmp/go-cache-ontology go test ./...
GOCACHE=/tmp/go-cache-ontology go test -race -v ./...
GOCACHE=/tmp/go-cache-ontology go vet ./...
```

测试覆盖跨午夜端点、免费时长、计费进位、跨日封顶、腾让 T 分钟、等待与自动归位、租约到期、宽限最后一日、拒绝次序、拒绝无痕、并发安全，并与独立朴素模型对拍随机序列。
