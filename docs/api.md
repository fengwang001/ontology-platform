# expresshub API

## 构造

`New(cfg Config) (*System, error)`

配置要求：

- `MaxItems > 0`
- `MaxWeightGrams > 0`
- `DwellLimitSec >= 0`

## 写入操作

- `AddParcel(AddParcelInput) (AddResult, error)`：需要运单号、目的网点、重量（1 到 10,000,000 克）、品类和非负时刻；必要时在同一原子事务内自动封旧袋、开新袋。
- `SealBag(SealBagInput) (SealResult, error)`：封存指定网点当前开放袋；无开放袋返回 `invalid_state`。
- `DispatchBag(DispatchBagInput) (DispatchResult, error)`：只允许已封存袋出场，必须提供非空车次。
- `VerifyBag(VerifyBagInput) (VerifyResult, error)`：只允许已出场袋拆袋；扫描列表不得包含空字符串或重复运单号。

`AddResult.SealedBagID` 为 0 表示本次未自动封存；`OpenedBagID` 为 0 表示复用原开放袋；`BagID` 始终是快件最终所属袋。

## 查询操作

- `OpenBag(destination string) (BagView, bool, error)`：查看网点当前开放袋；不存在时 `ok=false`。
- `Bag(id int64) (BagView, bool, error)`：按全局袋编号查看完整快照。
- `Location(waybill string) (ParcelLocation, bool, error)`：查看场内运单所在袋；已确认离场时 `ok=false`。

查询返回深拷贝快照，不更新时钟、状态、编号或任何索引。

## 品类与状态常量

品类：

- `CategoryNormal`
- `CategoryFragile`
- `CategoryLiquid`

袋状态：

- `BagStatusOpen`
- `BagStatusSealed`
- `BagStatusDispatched`
- `BagStatusVerified`

运单位置状态：

- `LocationInOpenBag`
- `LocationInSealedBag`
- `LocationInDispatchedBag`
- `LocationPendingInvestigation`
