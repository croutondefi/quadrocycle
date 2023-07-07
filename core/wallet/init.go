package wallet

import (
	"context"
	"errors"
	"fmt"

	"github.com/gobicycle/bicycle/audit"
	"github.com/gobicycle/bicycle/config"
	"github.com/gobicycle/bicycle/core"
	"github.com/gobicycle/bicycle/db"
	"github.com/gobicycle/bicycle/models"
	log "github.com/sirupsen/logrus"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton/wallet"
)

// InitWallets
// Generates highload hot-wallet and map[currency]models.JettonWallet Jetton wallets, and saves to DB
// TON highload hot-wallet (for seed and default subwallet_id) must be already active for success initialization.
func InitWallets(
	ctx context.Context,
	repo db.Repository,
	bc core.Blockchain,
	seed string,
	jettons map[string]config.Jetton,
	defaultWalletVersion wallet.Version,
) (core.Wallet, error) {
	var w = &wallets{
		bc:                   bc,
		jettonHotWallets:     map[string]models.JettonWallet{},
		defaultWalletVersion: defaultWalletVersion,
	}
	tonHotWallet, shard, subwalletId, err := w.initTonHotWallet(ctx, repo, seed)
	if err != nil {
		return nil, err
	}

	tonBasicWallet, _, _, err := w.GenerateDefaultWallet(seed, false)
	if err != nil {
		return nil, err
	}
	// don't set TTL here because spec is not inherited by GetSubwallet method

	for currency, j := range jettons {
		jw, err := w.initJettonHotWallet(ctx, repo, tonHotWallet.Address(), j.Master, currency, subwalletId)
		if err != nil {
			return nil, err
		}
		w.jettonHotWallets[currency] = jw
	}

	w.shard = shard
	w.tonHotWallet = tonHotWallet
	w.tonBasicWallet = tonBasicWallet

	return w, nil
}

func (w *wallets) initTonHotWallet(
	ctx context.Context,
	repo db.Repository,
	seed string,
) (
	tonHotWallet *wallet.Wallet,
	shard byte,
	subwalletId uint32,
	err error,
) {
	tonHotWallet, shard, subwalletId, err = w.GenerateDefaultWallet(seed, true)
	if err != nil {
		return nil, 0, 0, err
	}
	hotSpec := tonHotWallet.GetSpec().(*wallet.SpecHighloadV2R2)
	hotSpec.SetMessagesTTL(uint32(config.ExternalMessageLifetime.Seconds()))

	addr := models.AddressMustFromTonutilsAddress(tonHotWallet.Address())
	alreadySaved := false
	addrFromDb, err := repo.GetTonHotWalletAddress(ctx)
	if err == nil && addr != addrFromDb {
		audit.Log(audit.Error, string(models.TonHotWallet), models.InitEvent,
			fmt.Sprintf("Hot TON wallet address is not equal to the one stored in the database. Maybe seed was being changed. %s != %s",
				tonHotWallet.Address().String(), addrFromDb.ToTonutilsAddressStd(0).String()))
		return nil, 0, 0,
			fmt.Errorf("saved hot wallet not equal generated hot wallet. Maybe seed was being changed")
	} else if !errors.Is(err, models.ErrNotFound) && err != nil {
		return nil, 0, 0, err
	} else if err == nil {
		alreadySaved = true
	}

	log.Infof("Shard: %v", shard)
	log.Infof("TON hot wallet address: %v", tonHotWallet.Address().String())

	balance, status, err := w.bc.GetAccountCurrentState(ctx, tonHotWallet.Address())
	if err != nil {
		return nil, 0, 0, err
	}
	if balance.Cmp(config.Config.Ton.HotWalletMin) == -1 { // hot wallet balance < TonHotWalletMinimumBalance
		return nil, 0, 0,
			fmt.Errorf("hot wallet balance must be at least %v nanoTON", config.Config.Ton.HotWalletMin)
	}
	if status != tlb.AccountStatusActive {
		err = w.DeployTonWallet(ctx, tonHotWallet)
		if err != nil {
			return nil, 0, 0, err
		}
	}
	if !alreadySaved {
		err = repo.SaveTonWallet(ctx, models.WalletData{
			SubwalletID: uint32(wallet.DefaultSubwallet),
			Currency:    models.TonSymbol,
			Type:        models.TonHotWallet,
			Address:     addr,
		})
		if err != nil {
			return nil, 0, 0, err
		}
	}
	return tonHotWallet, shard, subwalletId, nil
}

func (w *wallets) initJettonHotWallet(
	ctx context.Context,
	repo db.Repository,
	tonHotWallet, jettonMaster *address.Address,
	currency string,
	subwalletId uint32,
) (models.JettonWallet, error) {
	// not init or check balances of Jetton wallets, it is not required for the service to work
	a, err := w.GetJettonWalletAddress(ctx, tonHotWallet, jettonMaster)
	if err != nil {
		return models.JettonWallet{}, err
	}
	res := models.JettonWallet{Address: a, Currency: currency}
	log.Infof("%v jetton hot wallet address: %v", currency, a.String())

	ownerAddr, err := models.AddressFromTonutilsAddress(tonHotWallet)
	if err != nil {
		return models.JettonWallet{}, err
	}
	jettonWalletAddr, err := models.AddressFromTonutilsAddress(a)
	if err != nil {
		return models.JettonWallet{}, err
	}

	walletData, isPresented, err := repo.GetJettonWallet(ctx, jettonWalletAddr)
	if err != nil {
		return models.JettonWallet{}, err
	}

	if isPresented && walletData.Currency == currency {
		return res, nil
	} else if isPresented && walletData.Currency != currency {
		audit.Log(audit.Error, string(models.JettonHotWallet), models.InitEvent,
			fmt.Sprintf("Hot Jetton wallets %s and %s have the same address %s",
				walletData.Currency, currency, a.String()))
		return models.JettonWallet{}, fmt.Errorf("jetton hot wallet address duplication")
	}

	err = repo.SaveJettonWallet(
		ctx,
		ownerAddr,
		models.WalletData{
			SubwalletID: subwalletId,
			Currency:    currency,
			Type:        models.JettonHotWallet,
			Address:     jettonWalletAddr,
		},
		true,
	)
	if err != nil {
		return models.JettonWallet{}, err
	}
	return res, nil
}
