package types

import (
	"github.com/gobicycle/bicycle/models"
	"github.com/gofrs/uuid"
)

type ServiceTonWithdrawalRequest struct {
	From string `json:"from"`
}

type ServiceJettonWithdrawalRequest struct {
	Owner        string `json:"owner"`
	JettonMaster string `json:"jetton_master"`
}

type ServiceWithdrawalResponse struct {
	Memo uuid.UUID `json:"memo"`
}

type WithdrawalResponse struct {
	ID int64 `json:"ID"`
}

type WithdrawalStatusResponse struct {
	Status models.WithdrawalStatus `json:"status"`
}

type CreateWithdrawalRequest struct {
	UserID      string       `json:"user_id"`
	QueryID     string       `json:"query_id"`
	Currency    string       `json:"currency"`
	Amount      models.Coins `json:"amount"`
	Destination string       `json:"destination"`
	Comment     string       `json:"comment"`
}

type GetHistoryRequest struct {
	UserID   int64  `schema:"user_id"`
	Currency string `schema:"currency"`

	Page    int `schema:"page"`
	PerPage int `schema:"per_page"`
}

type GetHistoryResponse struct {
	Incomes []Income `json:"incomes"`
}

type Income struct {
	DepositAddress string `json:"deposit_address"`
	Time           int64  `json:"time"`
	SourceAddress  string `json:"source_address,omitempty"`
	Amount         string `json:"amount"`
	Comment        string `json:"comment,omitempty"`
}

type GetIncomeResponse struct {
	Side         string        `json:"counting_side"`
	TotalIncomes []TotalIncome `json:"total_income"`
}

type TotalIncome struct {
	Address  string `json:"deposit_address"`
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

type CreateAddressRequest struct {
	UserID   int64  `json:"user_id"`
	Currency string `json:"currency"`
}

type CreateAddressResponse struct {
	Address string `json:"address"`
}

type GetAddressesResponse struct {
	Addresses []WalletAddress `json:"addresses"`
}

type WalletAddress struct {
	Address  string `json:"address"`
	Currency string `json:"currency"`
}
