package db

import (
	"context"
	"time"

	"github.com/gobicycle/bicycle/models"
	"github.com/gofrs/uuid"
	"github.com/xssnick/tonutils-go/address"
)

type HistoryFilterQuery struct {
	FilterQuery

	UserID   int64
	Currency string
}

type FilterQuery struct {
	Page    int
	PerPage int
}

func (q *FilterQuery) Offset() int {
	page := q.Page

	if page == 0 {
		page = 1
	}
	return q.Limit() * (page - 1)
}

func (q *FilterQuery) Limit() int {
	if q.PerPage != 0 {
		return q.PerPage
	} else {
		return 20
	}
}

type AddressBook interface {
	GetWalletType(address models.Address) (models.WalletType, bool)
	GetWalletTypeByTonutilsAddress(address *address.Address) (models.WalletType, bool)
	GetOwner(address models.Address) *models.Address
}

type Repository interface {
	GetLastSubwalletID(ctx context.Context) (uint32, error)
	SaveTonWallet(ctx context.Context, walletData models.WalletData) error
	SaveJettonWallet(ctx context.Context, ownerAddress models.Address, walletData models.WalletData, notSaveOwner bool) error
	GetTonWalletsAddresses(ctx context.Context, userID int64, types []models.WalletType) ([]models.Address, error)
	GetJettonOwnersAddresses(ctx context.Context, userID int64, types []models.WalletType) ([]models.OwnerWallet, error)
	GetInternalIncome(ctx context.Context, userID int64) ([]models.TotalIncome, error)
	GetExternalIncome(ctx context.Context, userID int64) ([]models.TotalIncome, error)
	GetIncomeHistory(ctx context.Context, q HistoryFilterQuery) ([]models.ExternalIncome, error)
	GetLastSavedBlock(context.Context) (*models.ShardBlockHeader, error)
	GetJettonWallet(ctx context.Context, address models.Address) (*models.WalletData, bool, error)
	GetTonHotWalletAddress(ctx context.Context) (models.Address, error)
	SetExpired(ctx context.Context) error
	SaveExternalIncome(ctx context.Context, tx Tx, inc models.ExternalIncome) error
	SaveInternalIncome(ctx context.Context, tx Tx, inc models.InternalIncome) error
	ApplySendingConfirmations(ctx context.Context, tx Tx, w models.SendingConfirmation) error
	UpdateInternalWithdrawal(ctx context.Context, tx Tx, w models.InternalWithdrawal) error
	UpdateExternalWithdrawal(ctx context.Context, tx Tx, w models.ExternalWithdrawal) error
	ApplyJettonWithdrawalConfirmation(ctx context.Context, tx Tx, confirm models.JettonWithdrawalConfirmation) error
	SaveBlock(ctx context.Context, tx Tx, block models.ShardBlockHeader) error
	ITx
}

type WithdrawalRepository interface {
	CreateWithdrawalRequest(ctx context.Context, w models.WithdrawalRequest) (int64, error)
	GetExternalWithdrawalStatus(ctx context.Context, id int64) (models.WithdrawalStatus, error)
	CreateServiceWithdrawalRequest(ctx context.Context, w models.ServiceWithdrawalRequest) (uuid.UUID, error)
	GetTonInternalWithdrawalTasks(ctx context.Context, limit int) ([]models.InternalWithdrawalTask, error)
	SaveInternalWithdrawalTask(ctx context.Context, task models.InternalWithdrawalTask, expiredAt time.Time, memo uuid.UUID) error
	GetServiceDepositWithdrawalTasks(ctx context.Context, limit int) ([]models.ServiceWithdrawalTask, error)
	UpdateServiceWithdrawalRequest(ctx context.Context, t models.ServiceWithdrawalTask, tonAmount models.Coins,
		expiredAt time.Time, filled bool) error
	CreateExternalWithdrawals(ctx context.Context, tasks []models.ExternalWithdrawalTask, extMsgUuid uuid.UUID, expiredAt time.Time) error
	GetServiceHotWithdrawalTasks(ctx context.Context, limit int) ([]models.ServiceWithdrawalTask, error)
	GetJettonInternalWithdrawalTasks(ctx context.Context, forbiddenAddresses []models.Address, limit int) ([]models.InternalWithdrawalTask, error)
	GetInternalWithdrawalStatus(ctx context.Context, dest models.Address, currency string) (models.WithdrawalStatus, error)
	GetExternalWithdrawalTasks(ctx context.Context, limit int) ([]models.ExternalWithdrawalTask, error)
}
