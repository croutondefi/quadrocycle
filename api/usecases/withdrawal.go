package usecases

import (
	"context"
	"fmt"

	"github.com/gobicycle/bicycle/api/helpers"
	"github.com/gobicycle/bicycle/api/types"
	"github.com/gobicycle/bicycle/db"
	"github.com/gobicycle/bicycle/models"
	"github.com/gofrs/uuid"
)

type WithdrawalUsecases struct {
	withdrawalRepo db.WithdrawalRepository
	addressBook    db.AddressBook
}

func (u *WithdrawalUsecases) GetWithdrawalStatus(ctx context.Context, id int64) (models.WithdrawalStatus, error) {
	return u.withdrawalRepo.GetExternalWithdrawalStatus(ctx, id)
}

func (u *WithdrawalUsecases) CreateServiceTonWithdrawal(ctx context.Context, req types.ServiceTonWithdrawalRequest) (uuid.UUID, error) {
	w, err := u.convertTonServiceWithdrawal(req)
	if err != nil {
		return uuid.Nil, err
	}
	return u.withdrawalRepo.CreateServiceWithdrawalRequest(ctx, w)
}

func (u *WithdrawalUsecases) CreateServiceJettonWithdrawal(ctx context.Context, req types.ServiceJettonWithdrawalRequest) (uuid.UUID, error) {
	w, err := u.convertJettonServiceWithdrawal(req)
	if err != nil {
		return uuid.Nil, err
	}
	return u.withdrawalRepo.CreateServiceWithdrawalRequest(ctx, w)
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

func (u *WithdrawalUsecases) convertTonServiceWithdrawal(w types.ServiceTonWithdrawalRequest) (models.ServiceWithdrawalRequest, error) {
	from, err := helpers.ParseAddress(w.From)
	if err != nil {
		return models.ServiceWithdrawalRequest{}, fmt.Errorf("invalid from address: %v", err)
	}
	t, ok := u.addressBook.GetWalletType(from)
	if !ok {
		return models.ServiceWithdrawalRequest{}, fmt.Errorf("unknown deposit address")
	}
	if t != models.JettonOwner {
		return models.ServiceWithdrawalRequest{},
			fmt.Errorf("service withdrawal allowed only for Jetton deposit owner")
	}
	return models.ServiceWithdrawalRequest{
		From: from,
	}, nil
}

func (u *WithdrawalUsecases) convertJettonServiceWithdrawal(w types.ServiceJettonWithdrawalRequest) (models.ServiceWithdrawalRequest, error) {
	from, err := helpers.ParseAddress(w.Owner)
	if err != nil {
		return models.ServiceWithdrawalRequest{}, fmt.Errorf("invalid from address: %v", err)
	}
	t, ok := u.addressBook.GetWalletType(from)
	if !ok {
		return models.ServiceWithdrawalRequest{}, fmt.Errorf("unknown deposit address")
	}
	if t != models.JettonOwner && t != models.TonDepositWallet {
		return models.ServiceWithdrawalRequest{},
			fmt.Errorf("service withdrawal allowed only for Jetton deposit owner or TON deposit")
	}
	jetton, err := helpers.ParseAddress(w.JettonMaster)
	if err != nil {
		return models.ServiceWithdrawalRequest{}, fmt.Errorf("invalid jetton master address: %v", err)
	}
	// currency type checks by withdrawal processor
	return models.ServiceWithdrawalRequest{
		From:         from,
		JettonMaster: &jetton,
	}, nil
}
