package keeper

import (
	"encoding/binary"
	"fmt"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/oracle/types"
	"github.com/cosmos/cosmos-sdk/x/params"
)

// Keeper maintains the link to data storage and
// exposes getter/setter methods for the various parts of the state machine
type Keeper struct {
	cdc      *codec.Codec
	storeKey sdk.StoreKey

	// The reference to the Paramstore to get and set gov specific params
	paramSpace params.Subspace

	stakeKeeper types.StakingKeeper

	claimTypeToName map[sdk.ClaimType]string
	nameToClaimType map[string]sdk.ClaimType
	claimHooksMap   map[sdk.ClaimType]sdk.ClaimHooks
}

// Parameter store
const (
	DefaultParamSpace = "oracle"
)

var (
	ParamStoreKeyProphecyParams = []byte("prophecyParams")
)

func ParamTypeTable() params.TypeTable {
	return params.NewTypeTable(
		ParamStoreKeyProphecyParams, types.ProphecyParams{},
	)
}

// NewKeeper creates new instances of the oracle Keeper
func NewKeeper(
	cdc *codec.Codec, storeKey sdk.StoreKey, paramSpace params.Subspace, stakeKeeper types.StakingKeeper,
) Keeper {
	return Keeper{
		cdc:             cdc,
		storeKey:        storeKey,
		paramSpace:      paramSpace.WithTypeTable(ParamTypeTable()),
		stakeKeeper:     stakeKeeper,
		claimTypeToName: make(map[sdk.ClaimType]string),
		nameToClaimType: make(map[string]sdk.ClaimType),
		claimHooksMap:   make(map[sdk.ClaimType]sdk.ClaimHooks),
	}
}

func (k Keeper) GetProphecyParams(ctx sdk.Context) types.ProphecyParams {
	var depositParams types.ProphecyParams
	k.paramSpace.Get(ctx, ParamStoreKeyProphecyParams, &depositParams)
	return depositParams
}

func (k Keeper) SetProphecyParams(ctx sdk.Context, params types.ProphecyParams) {
	k.paramSpace.Set(ctx, ParamStoreKeyProphecyParams, &params)
}

// GetProphecy gets the entire prophecy data struct for a given id
func (k Keeper) GetProphecy(ctx sdk.Context, id string) (types.Prophecy, bool) {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get([]byte(id))
	if bz == nil {
		return types.Prophecy{}, false
	}

	var dbProphecy types.DBProphecy
	k.cdc.MustUnmarshalBinaryBare(bz, &dbProphecy)

	deSerializedProphecy, err := dbProphecy.DeserializeFromDB()
	if err != nil {
		return types.Prophecy{}, false
	}

	return deSerializedProphecy, true
}

// DeleteProphecy delete prophecy for a given id
func (k Keeper) DeleteProphecy(ctx sdk.Context, id string) {
	store := ctx.KVStore(k.storeKey)
	store.Delete([]byte(id))
}

// setProphecy saves a prophecy with an initial claim
func (k Keeper) setProphecy(ctx sdk.Context, prophecy types.Prophecy) {
	store := ctx.KVStore(k.storeKey)
	serializedProphecy, err := prophecy.SerializeForDB()
	if err != nil {
		panic(err)
	}

	store.Set([]byte(prophecy.ID), k.cdc.MustMarshalBinaryBare(serializedProphecy))
}

func (k Keeper) RegisterClaimType(claimType sdk.ClaimType, claimTypeName string, hooks sdk.ClaimHooks) error {
	if claimTypeName == "" {
		return fmt.Errorf("claim type name should not be empty")
	}

	if _, ok := k.claimTypeToName[claimType]; ok {
		return fmt.Errorf("claim type %d already exists", claimType)
	}
	if _, ok := k.nameToClaimType[claimTypeName]; ok {
		return fmt.Errorf("claim type name %s already exists", claimTypeName)
	}
	if _, ok := k.claimHooksMap[claimType]; ok {
		return fmt.Errorf("hooks of claim type %d already exists", claimType)
	}

	k.claimTypeToName[claimType] = claimTypeName
	k.nameToClaimType[claimTypeName] = claimType
	k.claimHooksMap[claimType] = hooks
	return nil
}

func (k Keeper) GetClaimHooks(claimType sdk.ClaimType) sdk.ClaimHooks {
	return k.claimHooksMap[claimType]
}

func (k Keeper) GetClaimTypeName(claimType sdk.ClaimType) string {
	return k.claimTypeToName[claimType]
}

func (k Keeper) IncreaseSequence(ctx sdk.Context, claimType sdk.ClaimType) int64 {
	currentSequence := k.GetCurrentSequence(ctx, claimType)

	kvStore := ctx.KVStore(k.storeKey)
	nextSeq := currentSequence + 1

	bz := make([]byte, 8)
	binary.BigEndian.PutUint64(bz, uint64(nextSeq))

	kvStore.Set(types.GetClaimTypeSequence(claimType), bz)
	return nextSeq
}

func (k Keeper) GetCurrentSequence(ctx sdk.Context, claimType sdk.ClaimType) int64 {
	kvStore := ctx.KVStore(k.storeKey)
	bz := kvStore.Get(types.GetClaimTypeSequence(claimType))
	if bz == nil {
		return types.StartSequence
	}

	sequence := binary.BigEndian.Uint64(bz)
	return int64(sequence)
}

// ProcessClaim ...
func (k Keeper) ProcessClaim(ctx sdk.Context, claim types.Claim) (types.Prophecy, sdk.Error) {
	activeValidator := k.checkActiveValidator(ctx, claim.ValidatorAddress)
	if !activeValidator {
		return types.Prophecy{}, types.ErrInvalidValidator()
	}

	if claim.ID == "" {
		return types.Prophecy{}, types.ErrInvalidIdentifier()
	}

	if claim.Content == "" {
		return types.Prophecy{}, types.ErrInvalidClaim()
	}

	prophecy, found := k.GetProphecy(ctx, claim.ID)
	if !found {
		prophecy = types.NewProphecy(claim.ID)
	}

	switch prophecy.Status.Text {
	case types.PendingStatusText:
		// continue processing
	default:
		return types.Prophecy{}, types.ErrProphecyFinalized()
	}

	prophecy.AddClaim(claim.ValidatorAddress, claim.Content)
	prophecy = k.processCompletion(ctx, prophecy)

	k.setProphecy(ctx, prophecy)
	return prophecy, nil
}

func (k Keeper) checkActiveValidator(ctx sdk.Context, validatorAddress sdk.ValAddress) bool {
	validator, found := k.stakeKeeper.GetValidator(ctx, validatorAddress)
	if !found {
		return false
	}

	return validator.GetStatus().Equal(sdk.Bonded)
}

// processCompletion looks at a given prophecy
// and assesses whether the claim with the highest power on that prophecy has enough
// power to be considered successful, or alternatively,
// will never be able to become successful due to not enough validation power being
// left to push it over the threshold required for consensus.
func (k Keeper) processCompletion(ctx sdk.Context, prophecy types.Prophecy) types.Prophecy {
	highestClaim, highestClaimPower, totalClaimsPower := prophecy.FindHighestClaim(ctx, k.stakeKeeper)
	totalPower := k.stakeKeeper.GetLastTotalPower(ctx)

	highestConsensusRatio := sdk.NewDec(highestClaimPower).Quo(sdk.NewDec(totalPower))
	remainingPossibleClaimPower := totalPower - totalClaimsPower
	highestPossibleClaimPower := highestClaimPower + remainingPossibleClaimPower

	highestPossibleConsensusRatio := sdk.NewDec(highestPossibleClaimPower).Quo(sdk.NewDec(totalPower))

	prophecyParams := k.GetProphecyParams(ctx)

	if highestConsensusRatio.GTE(prophecyParams.ConsensusNeeded) {
		prophecy.Status.Text = types.SuccessStatusText
		prophecy.Status.FinalClaim = highestClaim
	} else if highestPossibleConsensusRatio.LT(prophecyParams.ConsensusNeeded) {
		prophecy.Status.Text = types.FailedStatusText
	}
	return prophecy
}