package usecases

import (
	"context"
	"fmt"
	"strconv"

	"github.com/gobicycle/bicycle/api/helpers"
	"github.com/gobicycle/bicycle/api/types"
	"github.com/gobicycle/bicycle/config"
	"github.com/gobicycle/bicycle/core"
	"github.com/gobicycle/bicycle/db"
	"github.com/gobicycle/bicycle/models"
	"github.com/xssnick/tonutils-go/address"
)

type AddressUsecases struct {
	repo        db.Repository
	addressBook db.AddressBook
	blockchain  core.Blockchain

	wallets core.Wallet

	hotWalletAddress address.Address
}

func (u *AddressUsecases) GetAddresses(ctx context.Context, userID int64) (*types.GetAddressesResponse, error) {
	var res = &types.GetAddressesResponse{
		Addresses: []types.WalletAddress{},
	}
	tonAddr, err := u.repo.GetTonWalletsAddresses(ctx, userID, []models.WalletType{models.TonDepositWallet})
	if err != nil {
		return nil, err
	}
	jettonAddr, err := u.repo.GetJettonOwnersAddresses(ctx, userID, []models.WalletType{models.JettonDepositWallet})
	if err != nil {
		return nil, err
	}
	for _, a := range tonAddr {
		res.Addresses = append(res.Addresses, types.WalletAddress{Address: a.ToUserFormat(), Currency: models.TonSymbol})
	}
	for _, a := range jettonAddr {
		res.Addresses = append(res.Addresses, types.WalletAddress{Address: a.Address.ToUserFormat(), Currency: a.Currency})
	}
	return res, nil
}

func (u *AddressUsecases) CreateAddress(ctx context.Context, userID int64, currency string) (addr string, err error) {
	if !helpers.IsValidCurrency(currency) {
		return "", fmt.Errorf("invalid currency")
	}

	subwalletID, err := u.repo.GetLastSubwalletID(ctx)
	if err != nil {
		return "", err
	}

	if currency == models.TonSymbol {
		addr, err = u.createTonAddress(ctx, strconv.FormatInt(userID, 10), currency, subwalletID)
		return
	}

	addr, err = u.createJettonAddress(ctx, strconv.FormatInt(userID, 10), currency, subwalletID)

	return
}

func (u *AddressUsecases) createJettonAddress(
	ctx context.Context,
	userID string,
	currency string,
	subwalletID uint32,
) (
	string,
	error,
) {
	jetton, ok := config.Config.Jettons[currency]
	if !ok {
		return "", fmt.Errorf("jetton address not found")
	}
	proxy, addr, err := u.wallets.GenerateDepositJettonWalletForProxy(ctx, &u.hotWalletAddress, jetton.Master, subwalletID+1)
	if err != nil {
		return "", err
	}
	jettonWalletAddr, err := models.AddressFromTonutilsAddress(addr)
	if err != nil {
		return "", err
	}
	proxyAddr, err := models.AddressFromTonutilsAddress(proxy.Address())
	if err != nil {
		return "", err
	}
	err = u.repo.SaveJettonWallet(
		ctx,
		proxyAddr,
		models.WalletData{
			UserID:      userID,
			SubwalletID: proxy.SubwalletID,
			Currency:    currency,
			Type:        models.JettonDepositWallet,
			Address:     jettonWalletAddr,
		},
		false,
	)
	if err != nil {
		return "", err
	}
	return proxyAddr.ToUserFormat(), nil
}

func (u *AddressUsecases) createTonAddress(
	ctx context.Context,
	userID string,
	currency string,
	subwalletID uint32,
) (
	string,
	error,
) {
	w, id, err := u.wallets.GenerateSubWallet(config.Config.Seed, subwalletID+1)
	if err != nil {
		return "", err
	}
	a, err := models.AddressFromTonutilsAddress(w.Address())
	if err != nil {
		return "", err
	}
	err = u.repo.SaveTonWallet(ctx,
		models.WalletData{
			SubwalletID: id,
			UserID:      userID,
			Currency:    models.TonSymbol,
			Type:        models.TonDepositWallet,
			Address:     a,
		},
	)
	if err != nil {
		return "", err
	}
	return a.ToUserFormat(), nil
}
