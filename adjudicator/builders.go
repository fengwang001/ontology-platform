package adjudicator

// 本文件提供面向本体平台领域的快照构造辅助函数，
// 把"对象实例依赖类型定义、链接依赖两端对象、动作依赖涉及的对象与链接"
// 这些固定依赖关系沉淀为显式构造器，供调用方与测试使用。

// TypeDef 构造一条对象类型定义记录。
func TypeDef(id string) Record {
	return Record{Ref: RecordRef{Category: CategoryObjectTypeDef, ID: id}}
}

// Object 构造一条对象实例记录，它依赖其所属的对象类型定义。
func Object(id, typeID string) Record {
	return Record{
		Ref:       RecordRef{Category: CategoryObjectInstance, ID: id},
		DependsOn: []RecordRef{{Category: CategoryObjectTypeDef, ID: typeID}},
	}
}

// Link 构造一条链接实例记录，它依赖两端引用的对象实例。
func Link(id, srcObjectID, dstObjectID string) Record {
	return Record{
		Ref: RecordRef{Category: CategoryLinkInstance, ID: id},
		DependsOn: []RecordRef{
			{Category: CategoryObjectInstance, ID: srcObjectID},
			{Category: CategoryObjectInstance, ID: dstObjectID},
		},
	}
}

// Action 构造一条动作执行记录，它依赖其涉及的全部对象与链接。
func Action(id string, involves ...RecordRef) Record {
	return Record{
		Ref:       RecordRef{Category: CategoryActionRecord, ID: id},
		DependsOn: involves,
	}
}

// ObjectRef 返回对象实例的引用，便于组装动作记录的依赖。
func ObjectRef(id string) RecordRef {
	return RecordRef{Category: CategoryObjectInstance, ID: id}
}

// LinkRef 返回链接实例的引用，便于组装动作记录的依赖。
func LinkRef(id string) RecordRef {
	return RecordRef{Category: CategoryLinkInstance, ID: id}
}

// Healthy 构造一个整体可用、无损坏的类别备份。
func Healthy(records ...Record) CategoryBackup {
	return CategoryBackup{Present: true, Records: records}
}

// PartiallyDamaged 构造一个整体可用但部分记录损坏的类别备份。
func PartiallyDamaged(damagedIDs []string, records ...Record) CategoryBackup {
	damaged := make(map[string]bool, len(damagedIDs))
	for _, id := range damagedIDs {
		damaged[id] = true
	}
	return CategoryBackup{Present: true, Damaged: damaged, Records: records}
}

// WhollyCorrupt 构造一个存在但整体损坏的类别备份。
// 记录列表仅用于表达"备份中本应有哪些记录"，
// 整体损坏时这些记录同样全部不可重建。
func WhollyCorrupt(records ...Record) CategoryBackup {
	return CategoryBackup{Present: true, WhollyCorrupt: true, Records: records}
}

// Missing 构造一个整体缺失的类别备份。
func Missing() CategoryBackup {
	return CategoryBackup{}
}
