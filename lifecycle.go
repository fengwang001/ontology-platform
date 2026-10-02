package main

import (
	"errors"
	"math/big"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClockRolledBack = errors.New("clock rolled back")
	ErrObjectNotFound  = errors.New("object not found")
)

type LifecycleConfig struct {
	HotPricePerDay         int64
	CoolPricePerDay        int64
	ColdPricePerDay        int64
	CoolTransitionDays     int64
	ColdTransitionDays     int64
	MinCoolStayDays        int64
	MinColdStayDays        int64
	CoolRetrievalPrice     int64
	ColdRetrievalPrice     int64
	FreeRetrievalPerPeriod int64
}

type LifecycleObject struct {
	Size       int64
	LastAccess int64
}

type Charges struct {
	StorageFee   *big.Int
	EarlyExitFee *big.Int
	RetrievalFee *big.Int
}

type GetManyResult struct {
	Items []Charges
	Total Charges
}

type LifecycleBilling struct {
	mu      sync.Mutex
	config  LifecycleConfig
	objects map[string]storedObject
	maxNow  int64
	period  int64
	used    int64
}

type storedObject struct {
	size       int64
	lastAccess int64
}

func NewLifecycleBilling(config LifecycleConfig) (*LifecycleBilling, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &LifecycleBilling{
		config:  config,
		objects: make(map[string]storedObject),
	}, nil
}

func (c LifecycleConfig) validate() error {
	prices := []int64{
		c.HotPricePerDay,
		c.CoolPricePerDay,
		c.ColdPricePerDay,
		c.MinCoolStayDays,
		c.MinColdStayDays,
		c.CoolRetrievalPrice,
		c.ColdRetrievalPrice,
	}
	for _, value := range prices {
		if value < 0 || value > 1_000_000 {
			return ErrInvalidArgument
		}
	}
	if c.CoolTransitionDays < 1 || c.CoolTransitionDays > 1_000_000 ||
		c.ColdTransitionDays <= c.CoolTransitionDays || c.ColdTransitionDays > 1_000_000 ||
		c.FreeRetrievalPerPeriod < 0 || c.FreeRetrievalPerPeriod > 1_000_000_000_000 {
		return ErrInvalidArgument
	}
	return nil
}

func (b *LifecycleBilling) Settle(object LifecycleObject, now int64) (Charges, error) {
	if !validSize(object.Size) || !validNow(now) || object.LastAccess < 0 || object.LastAccess > now {
		return Charges{}, ErrInvalidArgument
	}
	return settleCharges(b.config, object.Size, now-object.LastAccess), nil
}

func (b *LifecycleBilling) Put(key string, size int64, now int64) (Charges, error) {
	if err := validateKey(key); err != nil {
		return Charges{}, err
	}
	if !validSize(size) || !validNow(now) {
		return Charges{}, ErrInvalidArgument
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if now < b.maxNow {
		return Charges{}, ErrClockRolledBack
	}

	charges := zeroCharges()
	if old, ok := b.objects[key]; ok {
		charges = settleCharges(b.config, old.size, now-old.lastAccess)
	}
	b.objects[key] = storedObject{size: size, lastAccess: now}
	b.maxNow = now
	return charges.clone(), nil
}

func (b *LifecycleBilling) Delete(key string, now int64) (Charges, error) {
	if err := validateKey(key); err != nil {
		return Charges{}, err
	}
	if !validNow(now) {
		return Charges{}, ErrInvalidArgument
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if now < b.maxNow {
		return Charges{}, ErrClockRolledBack
	}
	object, ok := b.objects[key]
	if !ok {
		return Charges{}, ErrObjectNotFound
	}

	charges := settleCharges(b.config, object.size, now-object.lastAccess)
	delete(b.objects, key)
	b.maxNow = now
	return charges, nil
}

func (b *LifecycleBilling) Get(key string, now int64) (Charges, error) {
	if err := validateKey(key); err != nil {
		return Charges{}, err
	}
	if !validNow(now) {
		return Charges{}, ErrInvalidArgument
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if now < b.maxNow {
		return Charges{}, ErrClockRolledBack
	}
	object, ok := b.objects[key]
	if !ok {
		return Charges{}, ErrObjectNotFound
	}

	charges := settleCharges(b.config, object.size, now-object.lastAccess)
	charges.RetrievalFee = b.retrievalFee(object.size, now-object.lastAccess, now)
	b.objects[key] = storedObject{size: object.size, lastAccess: now}
	b.maxNow = now
	return charges, nil
}

func (b *LifecycleBilling) GetMany(keys []string, now int64) (GetManyResult, error) {
	for _, key := range keys {
		if err := validateKey(key); err != nil {
			return GetManyResult{}, err
		}
	}
	if !validNow(now) {
		return GetManyResult{}, ErrInvalidArgument
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if now < b.maxNow {
		return GetManyResult{}, ErrClockRolledBack
	}
	for _, key := range keys {
		if _, ok := b.objects[key]; !ok {
			return GetManyResult{}, ErrObjectNotFound
		}
	}

	result := GetManyResult{
		Items: make([]Charges, 0, len(keys)),
		Total: zeroCharges(),
	}
	for _, key := range keys {
		object := b.objects[key]
		charges := settleCharges(b.config, object.size, now-object.lastAccess)
		charges.RetrievalFee = b.retrievalFee(object.size, now-object.lastAccess, now)
		b.objects[key] = storedObject{size: object.size, lastAccess: now}
		result.Items = append(result.Items, charges.clone())
		result.Total.StorageFee.Add(result.Total.StorageFee, charges.StorageFee)
		result.Total.EarlyExitFee.Add(result.Total.EarlyExitFee, charges.EarlyExitFee)
		result.Total.RetrievalFee.Add(result.Total.RetrievalFee, charges.RetrievalFee)
	}
	b.maxNow = now
	return result, nil
}

func validateKey(key string) error {
	if key == "" || len(key) > 64 {
		return ErrInvalidArgument
	}
	return nil
}

func validSize(size int64) bool {
	return size >= 1 && size <= 1_000_000
}

func validNow(now int64) bool {
	return now >= 0 && now <= 1_000_000_000
}

func zeroCharges() Charges {
	return Charges{
		StorageFee:   big.NewInt(0),
		EarlyExitFee: big.NewInt(0),
		RetrievalFee: big.NewInt(0),
	}
}

func settleCharges(config LifecycleConfig, size int64, delta int64) Charges {
	hotDays := minInt64(delta, config.CoolTransitionDays)
	coolDays := maxInt64(0, minInt64(delta, config.ColdTransitionDays)-config.CoolTransitionDays)
	coldDays := maxInt64(0, delta-config.ColdTransitionDays)

	charges := zeroCharges()
	charges.StorageFee = sumTierFee(
		size,
		hotDays, config.HotPricePerDay,
		coolDays, config.CoolPricePerDay,
		coldDays, config.ColdPricePerDay,
	)

	var remainingStay int64
	var price int64
	switch {
	case delta < config.CoolTransitionDays:
		return charges
	case delta < config.ColdTransitionDays:
		remainingStay = maxInt64(0, config.MinCoolStayDays-(delta-config.CoolTransitionDays))
		price = config.CoolPricePerDay
	default:
		remainingStay = maxInt64(0, config.MinColdStayDays-(delta-config.ColdTransitionDays))
		price = config.ColdPricePerDay
	}
	charges.EarlyExitFee = tierFee(size, remainingStay, price)
	return charges
}

func (b *LifecycleBilling) retrievalFee(size int64, delta int64, now int64) *big.Int {
	var price int64
	switch {
	case delta < b.config.CoolTransitionDays:
		return big.NewInt(0)
	case delta < b.config.ColdTransitionDays:
		price = b.config.CoolRetrievalPrice
	default:
		price = b.config.ColdRetrievalPrice
	}

	period := now / 30
	if period != b.period {
		b.period = period
		b.used = 0
	}
	available := b.config.FreeRetrievalPerPeriod - b.used
	free := minInt64(size, maxInt64(0, available))
	b.used += free
	return tierFee(size-free, 1, price)
}

func sumTierFee(size int64, dayPricePairs ...int64) *big.Int {
	total := big.NewInt(0)
	for index := 0; index < len(dayPricePairs); index += 2 {
		total.Add(total, tierFee(size, dayPricePairs[index], dayPricePairs[index+1]))
	}
	return total
}

func tierFee(size int64, days int64, price int64) *big.Int {
	result := big.NewInt(size)
	result.Mul(result, big.NewInt(days))
	result.Mul(result, big.NewInt(price))
	return result
}

func minInt64(left int64, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

func maxInt64(left int64, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func (c Charges) clone() Charges {
	return Charges{
		StorageFee:   new(big.Int).Set(c.StorageFee),
		EarlyExitFee: new(big.Int).Set(c.EarlyExitFee),
		RetrievalFee: new(big.Int).Set(c.RetrievalFee),
	}
}
