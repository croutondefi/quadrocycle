package core

import (
	"context"
	"math/big"

	"github.com/gobicycle/bicycle/models"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton"
	"github.com/xssnick/tonutils-go/ton/jetton"
	"github.com/xssnick/tonutils-go/ton/wallet"
)

type Blockchain interface {
	GetTransactionIDsFromBlock(ctx context.Context, blockID *ton.BlockIDExt) ([]ton.TransactionShortInfo, error)
	GetTransactionFromBlock(ctx context.Context, blockID *ton.BlockIDExt, txID ton.TransactionShortInfo) (*tlb.Transaction, error)
	GenerateWallet(string, wallet.Version) (*wallet.Wallet, byte, uint32, error)
	GenerateHighloadWallet(string) (*wallet.Wallet, byte, uint32, error)
	SendExternalMessage(ctx context.Context, msg *tlb.ExternalMessage) error
	GetAccountCurrentState(ctx context.Context, address *address.Address) (*big.Int, tlb.AccountStatus, error)
	GetWalletData(ctx context.Context, address *address.Address, blockID *ton.BlockIDExt) (*ton.ExecutionResult, error)
	GetLastWalletData(ctx context.Context, address *address.Address) (*ton.ExecutionResult, error)
	StickyContext(ctx context.Context) context.Context
	JettonClient(masterAddr *address.Address) *jetton.Client
	WaitStatus(ctx context.Context, addr *address.Address, status tlb.AccountStatus) error
}

type Wallet interface {
	GenerateDefaultWallet(seed string, isHighload bool) (*wallet.Wallet, byte, uint32, error)
	DeployTonWallet(ctx context.Context, wallet *wallet.Wallet) error
	GetJettonWalletAddress(ctx context.Context, ownerWallet *address.Address, jettonMaster *address.Address) (*address.Address, error)
	GenerateSubWallet(seed string, startSubWalletID uint32) (*wallet.Wallet, uint32, error)
	GenerateDepositJettonWalletForProxy(context.Context, *address.Address, *address.Address, uint32) (*JettonProxy, *address.Address, error)
	GetJettonBalance(ctx context.Context, addr *address.Address, blockID *ton.BlockIDExt) (*big.Int, error)
	GetLastJettonBalance(ctx context.Context, addr *address.Address) (*big.Int, error)
	TonHotWallet() *wallet.Wallet
	TonBasicWallet() *wallet.Wallet
	JettonHotWallets() map[string]models.JettonWallet
	Shard() byte
}
