package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gobicycle/bicycle/config"
	"github.com/gobicycle/bicycle/core"
	"github.com/gobicycle/bicycle/db"
	"github.com/gobicycle/bicycle/models"
	log "github.com/sirupsen/logrus"
	"github.com/uptrace/bunrouter"
	"github.com/xssnick/tonutils-go/address"
)

type CreateAddressRequest struct {
	UserID   string `json:"user_id"`
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

func (h *Handler) createAddress(resp http.ResponseWriter, req bunrouter.Request) error {
	var data = CreateAddressRequest{}

	err := json.NewDecoder(req.Body).Decode(&data)
	if err != nil {
		writeHttpError(resp, http.StatusBadRequest, fmt.Sprintf("decode payload data err: %v", err))
		return err
	}
	if !isValidCurrency(data.Currency) {
		writeHttpError(resp, http.StatusBadRequest, "invalid currency type")
		return nil
	}
	addr, err := generateAddress(req.Context(), data.UserID, data.Currency, h.shard, h.repo, h.blockchain, h.hotWalletAddress)
	if err != nil {
		writeHttpError(resp, http.StatusInternalServerError, fmt.Sprintf("generate address err: %v", err))
		return nil
	}
	res := CreateAddressResponse{
		Address: addr,
	}
	resp.WriteHeader(http.StatusOK)
	err = json.NewEncoder(resp).Encode(res)
	if err != nil {
		log.Errorf("json encode error: %v", err)
	}

	return err
}

func (h *Handler) getAddresses(resp http.ResponseWriter, req bunrouter.Request) error {
	userID := req.URL.Query().Get("user_id")
	if userID == "" {
		writeHttpError(resp, http.StatusBadRequest, "need to provide user ID")
		return nil
	}
	addresses, err := getAddresses(req.Context(), userID, h.repo)
	if err != nil {
		writeHttpError(resp, http.StatusInternalServerError, fmt.Sprintf("get addresses err: %v", err))
		return err
	}
	resp.WriteHeader(http.StatusOK)
	err = json.NewEncoder(resp).Encode(addresses)
	if err != nil {
		log.Errorf("json encode error: %v", err)
	}

	return err
}

func getAddresses(ctx context.Context, userID string, repo db.Repository) (GetAddressesResponse, error) {
	var res = GetAddressesResponse{
		Addresses: []WalletAddress{},
	}
	tonAddr, err := repo.GetTonWalletsAddresses(ctx, userID, []models.WalletType{models.TonDepositWallet})
	if err != nil {
		return GetAddressesResponse{}, err
	}
	jettonAddr, err := repo.GetJettonOwnersAddresses(ctx, userID, []models.WalletType{models.JettonDepositWallet})
	if err != nil {
		return GetAddressesResponse{}, err
	}
	for _, a := range tonAddr {
		res.Addresses = append(res.Addresses, WalletAddress{Address: a.ToUserFormat(), Currency: models.TonSymbol})
	}
	for _, a := range jettonAddr {
		res.Addresses = append(res.Addresses, WalletAddress{Address: a.Address.ToUserFormat(), Currency: a.Currency})
	}
	return res, nil
}

func generateAddress(
	ctx context.Context,
	userID string,
	currency string,
	shard byte,
	repo db.Repository,
	bc core.Blockchain,
	hotWalletAddress address.Address,
) (
	string,
	error,
) {
	subwalletID, err := repo.GetLastSubwalletID(ctx)
	if err != nil {
		return "", err
	}
	var res string
	if currency == models.TonSymbol {
		w, id, err := bc.GenerateSubWallet(config.Config.Seed, shard, subwalletID+1)
		if err != nil {
			return "", err
		}
		a, err := models.AddressFromTonutilsAddress(w.Address())
		if err != nil {
			return "", err
		}
		err = repo.SaveTonWallet(ctx,
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
		res = a.ToUserFormat()
	} else {
		jetton, ok := config.Config.Jettons[currency]
		if !ok {
			return "", fmt.Errorf("jetton address not found")
		}
		proxy, addr, err := bc.GenerateDepositJettonWalletForProxy(ctx, shard, &hotWalletAddress, jetton.Master, subwalletID+1)
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
		err = repo.SaveJettonWallet(
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
		res = proxyAddr.ToUserFormat()
	}
	return res, nil
}
