package usecases

import (
	"context"
	"fmt"

	"github.com/gobicycle/bicycle/api/helpers"
	"github.com/gobicycle/bicycle/api/types"
	"github.com/gobicycle/bicycle/db"
	"github.com/gobicycle/bicycle/models"
)

type WithdrawalUsecases struct {
	withdrawalRepo db.WithdrawalRepository
}

func (u *WithdrawalUsecases) CreateWithdrawal(ctx context.Context, body *types.CreateWithdrawalRequest) (int64, error) {

	// if !isValidCurrency(w.Currency) {
	// 	return models.WithdrawalRequest{}, fmt.Errorf("invalid currency")
	// }
	addr, err := helpers.ParseAddress(body.Destination)
	if err != nil {
		return 0, fmt.Errorf("invalid destination address: %v", err)
	}
	// if !(w.Amount.Cmp(decimal.New(0, 0)) == 1) {
	// 	return models.WithdrawalRequest{}, fmt.Errorf("amount must be > 0")
	// }
	w := models.WithdrawalRequest{
		UserID:      body.UserID,
		QueryID:     body.QueryID,
		Currency:    body.Currency,
		Amount:      body.Amount,
		Destination: addr,
		// Bounceable:  addr.Is, //todo
		Comment:    body.Comment,
		IsInternal: false,
	}

	fmt.Println(w)

	// _, ok := u.repo.GetWalletType(w.Destination)
	// if ok {
	// 	return 0, fmt.Errorf("withdrawal to service internal addresses not supported")
	// }
	// id, err := u.repo.CreateWithdrawalRequest(ctx, w)
	// if err != nil {
	// 	return 0, fmt.Errorf("save withdrawal request err: %v", err)
	// }

	return 0, nil
}
