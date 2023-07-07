package wallet

import (
	"context"
	"fmt"
	"math"
	"math/big"

	"github.com/gobicycle/bicycle/config"
	"github.com/gobicycle/bicycle/core"
	"github.com/gobicycle/bicycle/models"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton"
	"github.com/xssnick/tonutils-go/ton/wallet"
)

type wallets struct {
	shard                byte
	defaultWalletVersion wallet.Version
	bc                   core.Blockchain

	tonHotWallet     *wallet.Wallet
	tonBasicWallet   *wallet.Wallet // basic wallet to make other wallets with different subwallet_id
	jettonHotWallets map[string]models.JettonWallet
}

func (c *wallets) TonHotWallet() *wallet.Wallet {
	return c.tonHotWallet
}

func (c *wallets) TonBasicWallet() *wallet.Wallet {
	return c.tonBasicWallet
}

func (c *wallets) JettonHotWallets() map[string]models.JettonWallet {
	return c.jettonHotWallets
}

func (c *wallets) Shard() byte {
	return c.shard
}

// GenerateDefaultWallet generates HighloadV2R2 or V3R2 TON wallet with
// default subwallet_id and returns wallet, shard and subwalletID
func (c *wallets) GenerateDefaultWallet(seed string, isHighload bool) (
	w *wallet.Wallet,
	shard byte,
	subwalletID uint32,
	err error,
) {
	if isHighload {
		w, shard, subwalletID, err = c.bc.GenerateHighloadWallet(seed)
	} else {
		w, shard, subwalletID, err = c.bc.GenerateWallet(seed, c.defaultWalletVersion)
	}
	return w, shard, subwalletID, err
}

// GetJettonBalance
// Get method get_wallet_data() returns (int balance, slice owner, slice jetton, cell jetton_wallet_code)
// Returns jetton balance for custom block in basic units
func (c *wallets) GetJettonBalance(ctx context.Context, addr *address.Address, blockID *ton.BlockIDExt) (*big.Int, error) {
	stack, err := c.bc.GetWalletData(ctx, addr, blockID)
	if err != nil {
		return nil, err
	}
	return stackToBalance(stack)
}

// GetLastJettonBalance
// Returns jetton balance for last block in basic units
func (c *wallets) GetLastJettonBalance(ctx context.Context, addr *address.Address) (*big.Int, error) {
	stack, err := c.bc.GetLastWalletData(ctx, addr)
	if err != nil {
		return nil, err
	}
	return stackToBalance(stack)
}

func stackToBalance(stack *ton.ExecutionResult) (*big.Int, error) {
	res := stack.AsTuple()
	switch res[0].(type) {
	case *big.Int:
		return res[0].(*big.Int), nil
	default:
		return nil, fmt.Errorf("invalid balance type")
	}
}

// DeployTonWallet
// Deploys wallet contract and wait its activation
func (c *wallets) DeployTonWallet(ctx context.Context, wallet *wallet.Wallet) error {
	balance, status, err := c.bc.GetAccountCurrentState(ctx, wallet.Address())
	if err != nil {
		return err
	}
	if balance.Cmp(big.NewInt(0)) == 0 {
		return fmt.Errorf("empty balance")
	}
	if status != tlb.AccountStatusActive {
		err = wallet.TransferNoBounce(ctx, wallet.Address(), tlb.FromNanoTONU(0), "")
		if err != nil {
			return err
		}
	} else {
		return nil
	}
	return c.bc.WaitStatus(ctx, wallet.Address(), tlb.AccountStatusActive)
}

// GenerateSubWallet generates subwallet for custom shard and
// subwallet_id >= startSubWalletId and returns wallet and new subwallet_id
func (c *wallets) GenerateSubWallet(seed string, startSubWalletID uint32) (*wallet.Wallet, uint32, error) {
	basic, _, _, err := c.bc.GenerateWallet(seed, c.defaultWalletVersion)
	if err != nil {
		return nil, 0, err
	}
	for id := startSubWalletID; id < math.MaxUint32; id++ {
		subWallet, err := basic.GetSubwallet(id)
		if err != nil {
			return nil, 0, err
		}
		addr, err := models.AddressFromTonutilsAddress(subWallet.Address())
		if err != nil {
			return nil, 0, err
		}
		if inShard(addr, c.shard) {
			return subWallet, id, nil
		}
	}
	return nil, 0, fmt.Errorf("subwallet not found")
}

// GetJettonWalletAddress generates jetton wallet address from owner and jetton master addresses
// func (c *wallets) GetJettonWalletAddress(
// 	ctx context.Context,
// 	owner *address.Address,
// 	jettonMaster *address.Address,
// ) (*address.Address, error) {
// 	contr, err := c.bc.GetContract(ctx, jettonMaster)
// 	if err != nil {
// 		return nil, err
// 	}
// 	emulator, err := newEmulator(contr.Code, contr.Data)
// 	if err != nil {
// 		return nil, err
// 	}
// 	addr, err := getJettonWalletAddressByTVM(owner, contr.Address, emulator)
// 	if err != nil {
// 		return nil, err
// 	}
// 	res := addr.ToTonutilsAddressStd(0)
// 	res.SetTestnetOnly(config.Config.Testnet)
// 	return res, nil
// }

// GetJettonWalletAddress generates jetton wallet address from owner and jetton master addresses
func (c *wallets) GetJettonWalletAddress(
	ctx context.Context,
	ownerAddr *address.Address,
	masterAddr *address.Address,
) (*address.Address, error) {

	master := c.bc.JettonClient(masterAddr)

	// get jetton wallet for account
	jettonWallet, err := master.GetJettonWallet(ctx, ownerAddr)
	if err != nil {
		return nil, err
	}

	return jettonWallet.Address(), nil
}

// GenerateDepositJettonWalletForProxy
// Generates jetton wallet address for custom shard and proxy contract as owner with subwallet_id >= startSubWalletId
func (c *wallets) GenerateDepositJettonWalletForProxy(
	ctx context.Context,
	proxyOwner, jettonMaster *address.Address,
	startSubWalletID uint32,
) (
	*core.JettonProxy,
	*address.Address,
	error,
) {
	for id := startSubWalletID; id < math.MaxUint32; id++ {
		proxy, err := core.NewJettonProxy(id, proxyOwner)
		if err != nil {
			return nil, nil, err
		}
		jettonWalletAddress, err := c.GetJettonWalletAddress(ctx, proxy.Address(), jettonMaster)
		if err != nil {
			return nil, nil, err
		}
		if jettonWalletAddress.Data()[0] == c.shard {
			jettonWalletAddress.SetTestnetOnly(config.Config.Testnet)
			return proxy, jettonWalletAddress, nil
		}
	}
	return nil, nil, fmt.Errorf("jetton wallet address not found")
}

func inShard(addr models.Address, shard byte) bool {
	return addr[0] == shard
}
