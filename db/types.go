package db

import (
	"context"
	"time"

	"github.com/gobicycle/bicycle/models"
	"github.com/gofrs/uuid"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/ton"
)

type AddressBook interface {
	GetWalletType(address models.Address) (models.WalletType, bool)
	GetWalletTypeByTonutilsAddress(address *address.Address) (models.WalletType, bool)
	GetOwner(address models.Address) *models.Address
}

type Repository interface {
	GetLastSubwalletID(ctx context.Context) (uint32, error)
	SaveTonWallet(ctx context.Context, walletData models.WalletData) error
	SaveJettonWallet(ctx context.Context, ownerAddress models.Address, walletData models.WalletData, notSaveOwner bool) error
	GetTonWalletsAddresses(ctx context.Context, userID string, types []models.WalletType) ([]models.Address, error)
	GetJettonOwnersAddresses(ctx context.Context, userID string, types []models.WalletType) ([]models.OwnerWallet, error)
	IsActualBlockData(ctx context.Context) (bool, error)
	GetIncome(ctx context.Context, userID string, isDepositSide bool) ([]models.TotalIncome, error)
	GetIncomeHistory(ctx context.Context, userID string, currency string, limit int, offset int) ([]models.ExternalIncome, error)
	GetLastSavedBlockID(context.Context) (*ton.BlockIDExt, error)
	SaveParsedBlockData(ctx context.Context, events models.BlockEvents) error
	GetJettonWallet(ctx context.Context, address models.Address) (*models.WalletData, bool, error)
	GetTonHotWalletAddress(ctx context.Context) (models.Address, error)
	SetExpired(ctx context.Context) error
}

type WithdrawalRepository interface {
	CreateWithdrawalRequest(ctx context.Context, w models.WithdrawalRequest) (int64, error)
	GetExternalWithdrawalStatus(ctx context.Context, id int64) (models.WithdrawalStatus, error)
	SaveServiceWithdrawalRequest(ctx context.Context, w models.ServiceWithdrawalRequest) (uuid.UUID, error)
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
