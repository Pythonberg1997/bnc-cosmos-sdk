package oracle

import (
	"fmt"
	"strconv"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/oracle/types"
)

func NewHandler(keeper Keeper) sdk.Handler {
	return func(ctx sdk.Context, msg sdk.Msg) sdk.Result {
		switch msg := msg.(type) {
		case ClaimMsg:
			return handleClaimMsg(ctx, keeper, msg)
		default:
			errMsg := "Unrecognized oracle msg type"
			return sdk.ErrUnknownRequest(errMsg).Result()
		}
	}
}

func handleClaimMsg(ctx sdk.Context, oracleKeeper Keeper, msg ClaimMsg) sdk.Result {
	claimTypeName := oracleKeeper.GetClaimTypeName(msg.ClaimType)
	if claimTypeName == "" {
		return types.ErrInvalidClaimType(fmt.Sprintf("claim type %d does not exist", msg.ClaimType)).Result()
	}

	claimHooks := oracleKeeper.GetClaimHooks(msg.ClaimType)
	if claimHooks == nil {
		return types.ErrInvalidClaimType(fmt.Sprintf("hooks of claim type %s does not exist", claimTypeName)).Result()
	}

	sdkErr := claimHooks.CheckClaim(ctx, msg.Claim)
	if sdkErr != nil {
		return sdkErr.Result()
	}

	currentSequence := oracleKeeper.GetCurrentSequence(ctx, msg.ClaimType)
	if msg.Sequence != currentSequence {
		return types.ErrInvalidSequence(fmt.Sprintf("current sequence of claim type %s is %d", claimTypeName, currentSequence)).Result()
	}

	claim := types.Claim{
		ID:               types.GetClaimId(msg.ClaimType, msg.Sequence),
		ValidatorAddress: sdk.ValAddress(msg.ValidatorAddress),
		Content:          msg.Claim,
	}

	prophecy, sdkErr := oracleKeeper.ProcessClaim(ctx, claim)
	if sdkErr != nil {
		return sdkErr.Result()
	}

	if prophecy.Status.Text == types.FailedStatusText {
		oracleKeeper.DeleteProphecy(ctx, prophecy.ID)
		return sdk.Result{}
	}

	if prophecy.Status.Text != types.SuccessStatusText {
		return sdk.Result{}
	}

	tags, sdkErr := claimHooks.ExecuteClaim(ctx, prophecy.Status.FinalClaim)
	if sdkErr != nil {
		return sdkErr.Result()
	}

	// delete prophecy when execute claim success
	oracleKeeper.DeleteProphecy(ctx, prophecy.ID)

	resultTags := sdk.NewTags(
		claimTypeName, []byte(strconv.FormatInt(msg.Sequence, 10)),
	)
	if tags != nil {
		resultTags = resultTags.AppendTags(tags)
	}

	// increase claim type sequence
	oracleKeeper.IncreaseSequence(ctx, msg.ClaimType)

	return sdk.Result{Tags: resultTags}
}