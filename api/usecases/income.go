package usecases

import (
	"context"
	"fmt"

	"github.com/gobicycle/bicycle/api/helpers"
	"github.com/gobicycle/bicycle/api/types"
	"github.com/gobicycle/bicycle/config"
	"github.com/gobicycle/bicycle/db"
	"github.com/gobicycle/bicycle/models"
	"github.com/xssnick/tonutils-go/address"
)

type IncomeUsecases struct {
	repo        db.Repository
	addressBook db.AddressBook
}

func (u *IncomeUsecases) GetIncome(ctx context.Context, id int64, internal bool) (*types.GetIncomeResponse, error) {
	var ii []models.TotalIncome
	var err error
	if internal {
		ii, err = u.repo.GetInternalIncome(ctx, id)
	} else {
		ii, err = u.repo.GetExternalIncome(ctx, id)
	}
	if err != nil {
		return nil, err
	}

	return u.convertIncome(ii)
}

func (u *IncomeUsecases) GetHistory(ctx context.Context, data *types.GetHistoryRequest) (*types.GetHistoryResponse, error) {
	if !helpers.IsValidCurrency(data.Currency) {
		return nil, fmt.Errorf("invalid currency: %s", data.Currency)
	}
	q := db.HistoryFilterQuery{}
	q.Page = data.Page
	q.Currency = data.Currency
	q.PerPage = data.PerPage
	q.UserID = data.UserID
	hh, err := u.repo.GetIncomeHistory(ctx, q)
	if err != nil {
		return nil, err
	}

	return u.convertHistory(data.Currency, hh)
}

func (u *IncomeUsecases) convertHistory(currency string, incomes []models.ExternalIncome) (*types.GetHistoryResponse, error) {
	var res = types.GetHistoryResponse{
		Incomes: []types.Income{},
	}
	for _, i := range incomes {
		inc := types.Income{
			Time:    int64(i.Utime),
			Amount:  i.Amount.String(),
			Comment: i.Comment,
		}
		if currency == models.TonSymbol {
			inc.DepositAddress = i.To.ToUserFormat()
		} else {
			owner := u.addressBook.GetOwner(i.To)
			if owner == nil {
				return nil, fmt.Errorf("can not find owner for deposit: %s", i.To.ToUserFormat())
			}
			inc.DepositAddress = owner.ToUserFormat()
		}
		// show only std address
		if len(i.From) == 32 && i.FromWorkchain != nil {
			addr := address.NewAddress(0, byte(*i.FromWorkchain), i.From)
			addr.SetTestnetOnly(config.Config.Testnet)
			inc.SourceAddress = addr.String()
		}
		res.Incomes = append(res.Incomes, inc)
	}
	return &res, nil
}

func (u *IncomeUsecases) convertIncome(totalIncomes []models.TotalIncome) (*types.GetIncomeResponse, error) {
	var res = types.GetIncomeResponse{
		TotalIncomes: []types.TotalIncome{},
	}
	if config.Config.IsDepositSideCalculation {
		res.Side = models.SideDeposit
	} else {
		res.Side = models.SideHotWallet
	}

	for _, b := range totalIncomes {
		totIncome := types.TotalIncome{
			Amount:   b.Amount.String(),
			Currency: b.Currency,
		}
		if b.Currency == models.TonSymbol {
			totIncome.Address = b.Deposit.ToUserFormat()
		} else {
			owner := u.addressBook.GetOwner(b.Deposit)
			if owner == nil {
				return nil, fmt.Errorf("can not find owner for deposit: %s", b.Deposit)
			}
			totIncome.Address = owner.ToUserFormat()
		}
		res.TotalIncomes = append(res.TotalIncomes, totIncome)
	}
	return &res, nil
}
