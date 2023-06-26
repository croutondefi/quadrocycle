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
