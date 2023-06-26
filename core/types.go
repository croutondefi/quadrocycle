package core

import (
	"context"
	"math/big"

	"github.com/gobicycle/bicycle/models"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton"
	"github.com/xssnick/tonutils-go/ton/wallet"
)

type Blockchain interface {
	GetJettonWalletAddress(ctx context.Context, ownerWallet *address.Address, jettonMaster *address.Address) (*address.Address, error)
	GetTransactionIDsFromBlock(ctx context.Context, blockID *ton.BlockIDExt) ([]ton.TransactionShortInfo, error)
	GetTransactionFromBlock(ctx context.Context, blockID *ton.BlockIDExt, txID ton.TransactionShortInfo) (*tlb.Transaction, error)
	GenerateDefaultWallet(seed string, isHighload bool) (*wallet.Wallet, byte, uint32, error)
	GetJettonBalance(ctx context.Context, address models.Address, blockID *ton.BlockIDExt) (*big.Int, error)
	SendExternalMessage(ctx context.Context, msg *tlb.ExternalMessage) error
	GetAccountCurrentState(ctx context.Context, address *address.Address) (*big.Int, tlb.AccountStatus, error)
	GetLastJettonBalance(ctx context.Context, address *address.Address) (*big.Int, error)
	DeployTonWallet(ctx context.Context, wallet *wallet.Wallet) error
	StickyContext(ctx context.Context) context.Context
	GenerateSubWallet(seed string, shard byte, startSubWalletID uint32) (*wallet.Wallet, uint32, error)
	GenerateDepositJettonWalletForProxy(context.Context, byte, *address.Address, *address.Address, uint32) (*JettonProxy, *address.Address, error)
}
