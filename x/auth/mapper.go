package auth

import (
	"sort"
	"sync"

	codec "github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/hashicorp/golang-lru"
	"github.com/tendermint/tendermint/crypto"
)

var globalAccountNumberKey = []byte("globalAccountNumber")

// This AccountKeeper encodes/decodes accounts using the
// go-amino (binary) encoding/decoding library.
type AccountKeeper struct {

	// The (unexposed) key used to access the store from the Context.
	key sdk.StoreKey

	// The prototypical Account constructor.
	proto func() Account

	// The codec codec for binary encoding/decoding of accounts.
	cdc *codec.Codec
}

// NewAccountKeeper returns a new sdk.AccountKeeper that
// uses go-amino to (binary) encode and decode concrete sdk.Accounts.
// nolint
func NewAccountKeeper(cdc *codec.Codec, key sdk.StoreKey, proto func() Account) AccountKeeper {
	return AccountKeeper{
		key:   key,
		proto: proto,
		cdc:   cdc,
	}
}

// Implaements sdk.AccountKeeper.
func (am AccountKeeper) NewAccountWithAddress(ctx sdk.Context, addr sdk.AccAddress) Account {
	acc := am.proto()
	err := acc.SetAddress(addr)
	if err != nil {
		// Handle w/ #870
		panic(err)
	}
	err = acc.SetAccountNumber(am.GetNextAccountNumber(ctx))
	if err != nil {
		// Handle w/ #870
		panic(err)
	}
	return acc
}

// New Account
func (am AccountKeeper) NewAccount(ctx sdk.Context, acc Account) Account {
	err := acc.SetAccountNumber(am.GetNextAccountNumber(ctx))
	if err != nil {
		// TODO: Handle with #870
		panic(err)
	}
	return acc
}

// Turn an address to key used to get it from the account store
func AddressStoreKey(addr sdk.AccAddress) []byte {
	return append([]byte("account:"), addr.Bytes()...)
}

// Implements sdk.AccountKeeper.
func (am AccountKeeper) GetAccount(ctx sdk.Context, addr sdk.AccAddress) Account {
	cache := ctx.AccountCache()

	cVal := cache.GetAccount(addr)
	if acc, ok := cVal.(Account); ok {
		return acc
	}
	return nil
}

// Implements sdk.AccountKeeper.
func (am AccountKeeper) SetAccount(ctx sdk.Context, acc Account) {
	addr := acc.GetAddress()
	cache := ctx.AccountCache()
	cache.SetAccount(addr, acc)
}

// RemoveAccount removes an account for the account mapper store.
func (am AccountKeeper) RemoveAccount(ctx sdk.Context, acc Account) {
	addr := acc.GetAddress()
	cache := ctx.AccountCache()
	cache.Delete(addr)
}

// Implements sdk.AccountKeeper.
func (am AccountKeeper) IterateAccounts(ctx sdk.Context, process func(Account) (stop bool)) {
	store := ctx.KVStore(am.key)
	iter := sdk.KVStorePrefixIterator(store, []byte("account:"))
	defer iter.Close()
	for {
		if !iter.Valid() {
			return
		}
		val := iter.Value()
		acc := am.decodeAccount(val)
		if process(acc) {
			return
		}
		iter.Next()
	}
}

// Returns the PubKey of the account at address
func (am AccountKeeper) GetPubKey(ctx sdk.Context, addr sdk.AccAddress) (crypto.PubKey, sdk.Error) {
	acc := am.GetAccount(ctx, addr)
	if acc == nil {
		return nil, sdk.ErrUnknownAddress(addr.String())
	}
	return acc.GetPubKey(), nil
}

// Returns the Sequence of the account at address
func (am AccountKeeper) GetSequence(ctx sdk.Context, addr sdk.AccAddress) (int64, sdk.Error) {
	acc := am.GetAccount(ctx, addr)
	if acc == nil {
		return 0, sdk.ErrUnknownAddress(addr.String())
	}
	return acc.GetSequence(), nil
}

func (am AccountKeeper) setSequence(ctx sdk.Context, addr sdk.AccAddress, newSequence int64) sdk.Error {
	acc := am.GetAccount(ctx, addr)
	if acc == nil {
		return sdk.ErrUnknownAddress(addr.String())
	}
	err := acc.SetSequence(newSequence)
	if err != nil {
		// Handle w/ #870
		panic(err)
	}
	am.SetAccount(ctx, acc)
	return nil
}

// Returns and increments the global account number counter
func (am AccountKeeper) GetNextAccountNumber(ctx sdk.Context) int64 {
	var accNumber int64
	store := ctx.KVStore(am.key)
	bz := store.Get(globalAccountNumberKey)
	if bz == nil {
		accNumber = 0
	} else {
		err := am.cdc.UnmarshalBinary(bz, &accNumber)
		if err != nil {
			panic(err)
		}
	}

	bz = am.cdc.MustMarshalBinary(accNumber + 1)
	store.Set(globalAccountNumberKey, bz)

	return accNumber
}

//----------------------------------------
// misc.

func (am AccountKeeper) encodeAccount(acc Account) []byte {
	bz, err := am.cdc.MarshalBinaryBare(acc)
	if err != nil {
		panic(err)
	}
	return bz
}

func (am AccountKeeper) decodeAccount(bz []byte) (acc Account) {
	err := am.cdc.UnmarshalBinaryBare(bz, &acc)
	if err != nil {
		panic(err)
	}
	return
}

func NewAccountSotreCache(cdc *codec.Codec, store sdk.KVStore, cap int) sdk.AccountStoreCache {
	cache, err := lru.New(cap)
	if err != nil {
		panic(err)
	}

	return &accountStoreCache{
		cdc:   cdc,
		cache: cache,
		store: store,
	}
}

type accountStoreCache struct {
	cdc   *codec.Codec
	cache *lru.Cache
	store sdk.KVStore
}

func (ac *accountStoreCache) getAccountFromCache(addr sdk.AccAddress) (acc interface{}, ok bool) {
	cacc, ok := ac.cache.Get(addr.String())
	if !ok {
		return nil, ok
	}
	if acc, ok := cacc.(Account); ok {
		return acc.Clone(), ok
	}
	return nil, false
}

func (ac *accountStoreCache) setAccountToCache(addr sdk.AccAddress, acc Account) {
	ac.cache.Add(addr.String(), acc.Clone())
}

func (ac *accountStoreCache) GetAccount(addr sdk.AccAddress) interface{} {
	println("cache: get account from store, ", addr.String())
	if acc, ok := ac.getAccountFromCache(addr); ok {
		return acc
	}

	bz := ac.store.Get(AddressStoreKey(addr))
	if bz == nil {
		return nil
	}
	acc := ac.decodeAccount(bz)
	ac.setAccountToCache(addr, acc)
	return acc
}

func (ac *accountStoreCache) SetAccount(addr sdk.AccAddress, acc interface{}) {
	cacc, ok := acc.(Account)
	if !ok {
		return
	}

	println("cache: set account to store, ", addr.String())
	bz := ac.encodeAccount(cacc)
	ac.setAccountToCache(addr, cacc)
	ac.store.Set(AddressStoreKey(addr), bz)
}

func (ac *accountStoreCache) Delete(addr sdk.AccAddress) {
	ac.setAccountToCache(addr, nil)
	ac.store.Delete(AddressStoreKey(addr))
}

func (ac *accountStoreCache) encodeAccount(acc Account) []byte {
	bz, err := ac.cdc.MarshalBinaryBare(acc)
	if err != nil {
		panic(err)
	}
	return bz
}

func (ac *accountStoreCache) decodeAccount(bz []byte) (acc Account) {
	err := ac.cdc.UnmarshalBinaryBare(bz, &acc)
	if err != nil {
		panic(err)
	}
	return
}

type cValue struct {
	acc     interface{}
	deleted bool
	dirty   bool
}

func NewAccountCache(parent sdk.AccountStoreCache) sdk.AccountCache {
	return &accountCache{
		cache:  make(map[string]cValue),
		parent: parent,
	}
}

type accountCache struct {
	mtx    sync.Mutex
	cache  map[string]cValue
	parent sdk.AccountStoreCache
}

func (ac *accountCache) GetAccount(addr sdk.AccAddress) interface{} {
	ac.mtx.Lock()
	defer ac.mtx.Unlock()

	cacheVal, ok := ac.cache[addr.String()]
	if !ok {
		acc := ac.parent.GetAccount(addr)
		ac.setAccountToCache(addr, acc, false, false)
		println("cache: miss cache")
		return acc
	} else {
		println("cache: hit cache")
		return cacheVal.acc
	}
}

func (ac *accountCache) SetAccount(addr sdk.AccAddress, acc interface{}) {
	ac.mtx.Lock()
	defer ac.mtx.Unlock()

	println("cache: set account to cache, ", addr.String())

	ac.setAccountToCache(addr, acc, false, true)
}

func (ac *accountCache) Delete(addr sdk.AccAddress) {
	ac.mtx.Lock()
	defer ac.mtx.Unlock()

	ac.setAccountToCache(addr, nil, true, true)
}

func (ac *accountCache) Write() {
	ac.mtx.Lock()
	defer ac.mtx.Unlock()

	// We need a copy of all of the keys.
	// Not the best, but probably not a bottleneck depending.
	keys := make([]string, 0, len(ac.cache))
	for key, dbValue := range ac.cache {
		if dbValue.dirty {
			keys = append(keys, key)
		}
	}

	sort.Strings(keys)

	// TODO: Consider allowing usage of Batch, which would allow the write to
	// at least happen atomically.
	for _, key := range keys {
		cacheValue := ac.cache[key]
		addr, _ := sdk.AccAddressFromBech32(key)

		println("cache: write cache to parent, ", key, cacheValue.acc == nil, cacheValue.dirty)
		if cacheValue.deleted {
			ac.parent.Delete(addr)
		} else if cacheValue.acc == nil {
			// Skip, it already doesn't exist in parent.
		} else {
			ac.parent.SetAccount(addr, cacheValue.acc)
		}
	}

	// Clear the cache
	ac.cache = make(map[string]cValue)
}

func (ac *accountCache) getAccountFromCache(addr sdk.AccAddress) (acc interface{}, ok bool) {
	var cacc interface{}

	cacheVal, ok := ac.cache[addr.String()]
	if !ok {
		cacc = ac.parent.GetAccount(addr)
		ac.setAccountToCache(addr, acc, false, false)
	} else {
		cacc = cacheVal.acc
	}

	if acc, ok := cacc.(Account); ok {
		return acc.Clone(), ok
	}
	return nil, false
}

func (ac *accountCache) setAccountToCache(addr sdk.AccAddress, acc interface{}, deleted bool, dirty bool) {
	if cacc, ok := acc.(Account); ok {
		ac.cache[addr.String()] = cValue{
			acc:     cacc.Clone(),
			deleted: deleted,
			dirty:   dirty,
		}
	}
}
