package pvbinding

import "fmt"

// CheckInvariants 执行全量一致性自检，返回第一个被违反的不变量；nil 表示全部通过。
//
// 不变量：
//  1. 每个 Bound 卷恰被一个 Bound 声明引用，且该声明回指同一卷。
//  2. Available 卷不被任何声明引用。
//  3. Released 卷不被任何声明引用。
//  4. 每个 Bound 声明回指的卷存在、状态为 Bound 且卷的预留名等于声明名。
//  5. 待绑定声明不持有卷名。
//  6. 可用卷索引与 Available 状态的卷集合完全一致。
//  7. 同一存储类索引桶严格按 (容量, 名称) 有序。
//  8. Bound/Released 卷绝不出现在可用索引中。
//  9. 容量、绑定声明请求容量等数值约束不被破坏。
func (c *Controller) CheckInvariants() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	claimRefs := make(map[string]string)
	for _, cl := range c.claims {
		if cl.Bound {
			if cl.VolumeName == "" {
				return fmt.Errorf("bound claim %s has empty volume name", cl.Name)
			}
			v, ok := c.vols[cl.VolumeName]
			if !ok {
				return fmt.Errorf("bound claim %s references missing volume %s", cl.Name, cl.VolumeName)
			}
			if v.Phase != VolumeBound {
				return fmt.Errorf("claim %s references volume %s in phase %s, want Bound",
					cl.Name, v.Name, v.Phase)
			}
			if v.ClaimName != cl.Name {
				return fmt.Errorf("volume %s claims %q but claim %s references it",
					v.Name, v.ClaimName, cl.Name)
			}
			if cl.Spec.RequestCapacity > v.Spec.Capacity {
				return fmt.Errorf("claim %s request %d exceeds volume %s capacity %d",
					cl.Name, cl.Spec.RequestCapacity, v.Name, v.Spec.Capacity)
			}
			claimRefs[v.Name] = cl.Name
		} else if cl.VolumeName != "" {
			return fmt.Errorf("pending claim %s unexpectedly references volume %s",
				cl.Name, cl.VolumeName)
		}
	}

	for _, v := range c.vols {
		switch v.Phase {
		case VolumeBound:
			ref, ok := claimRefs[v.Name]
			if !ok {
				return fmt.Errorf("bound volume %s has no referencing claim", v.Name)
			}
			if v.ClaimName != ref {
				return fmt.Errorf("bound volume %s claimName %q != referencing claim %q",
					v.Name, v.ClaimName, ref)
			}
		case VolumeAvailable:
			if v.ClaimName != "" {
				// Available 卷上的 ClaimName 是预绑定预留，可指向尚未创建的声明；
				// 但必须与规格中的 ReservedClaim 保持一致。
				if v.ClaimName != v.Spec.ReservedClaim {
					return fmt.Errorf("available volume %s claimName %q != reserved %q",
						v.Name, v.ClaimName, v.Spec.ReservedClaim)
				}
			}
		case VolumeReleased:
			if v.ClaimName != "" {
				return fmt.Errorf("released volume %s still carries claim name %q",
					v.Name, v.ClaimName)
			}
		default:
			return fmt.Errorf("volume %s has unknown phase %q", v.Name, v.Phase)
		}
	}

	indexed := make(map[string]*Volume)
	for sc, b := range c.avail.buckets {
		for i := range b {
			v := b[i]
			if i > 0 {
				prev := b[i-1]
				if prev.Spec.Capacity > v.Spec.Capacity ||
					(prev.Spec.Capacity == v.Spec.Capacity && prev.Name >= v.Name) {
					return fmt.Errorf("index bucket %q not ordered at %s (%d) vs %s (%d)",
						sc, prev.Name, prev.Spec.Capacity, v.Name, v.Spec.Capacity)
				}
			}
			if _, dup := indexed[v.Name]; dup {
				return fmt.Errorf("volume %s indexed twice", v.Name)
			}
			indexed[v.Name] = v
			if v.Phase != VolumeAvailable {
				return fmt.Errorf("volume %s in phase %s present in available index",
					v.Name, v.Phase)
			}
			if v.Spec.StorageClass != sc {
				return fmt.Errorf("volume %s indexed under wrong storage class %q", v.Name, sc)
			}
		}
	}
	for name, v := range c.vols {
		if v.Phase == VolumeAvailable {
			if _, ok := indexed[name]; !ok {
				return fmt.Errorf("available volume %s missing from index", name)
			}
		} else if _, ok := indexed[name]; ok {
			return fmt.Errorf("non-available volume %s present in index", name)
		}
	}
	return nil
}
