package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gobicycle/bicycle/api/types"
	"github.com/gobicycle/bicycle/db"
	"github.com/gobicycle/bicycle/models"
	"github.com/uptrace/bunrouter"
)

func (h *Handler) createWithdrawal(resp http.ResponseWriter, req bunrouter.Request) error {
	var body types.CreateWithdrawalRequest
	err := json.NewDecoder(req.Body).Decode(&body)
	if err != nil {
		writeHttpError(resp, http.StatusBadRequest, fmt.Sprintf("decode payload err: %v", err))
		return err
	}

	id, err := h.withdrawalUsecases.CreateWithdrawal(req.Context(), &body)

	r := types.WithdrawalResponse{ID: id}
	resp.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(resp).Encode(r)

	return err
}

func (h *Handler) getWithdrawalStatus(resp http.ResponseWriter, req bunrouter.Request) error {
	ids := req.URL.Query().Get("id")
	if ids == "" {
		writeHttpError(resp, http.StatusBadRequest, "need to provide request ID")
		return nil
	}
	id, err := strconv.ParseInt(ids, 10, 64)
	if err != nil {
		writeHttpError(resp, http.StatusBadRequest, fmt.Sprintf("convert request ID err: %v", err))
		return err
	}
	status, err := h.repo.GetExternalWithdrawalStatus(req.Context(), id)
	if err == models.ErrNotFound {
		writeHttpError(resp, http.StatusBadRequest, "request ID not found")
		return err
	}
	if err != nil {
		writeHttpError(resp, http.StatusInternalServerError, fmt.Sprintf("get external withdrawal status err: %v", err))
		return err
	}
	resp.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(resp).Encode(types.WithdrawalStatusResponse{Status: status})

	return err
}

func (h *Handler) serviceTonWithdrawal(resp http.ResponseWriter, req bunrouter.Request) error {
	var body types.ServiceTonWithdrawalRequest

	err := json.NewDecoder(req.Body).Decode(&body)
	if err != nil {
		writeHttpError(resp, http.StatusBadRequest, fmt.Sprintf("decode payload err: %v", err))
		return err
	}
	w, err := convertTonServiceWithdrawal(h.repo, body)
	if err != nil {
		writeHttpError(resp, http.StatusBadRequest, fmt.Sprintf("convert service withdrawal err: %v", err))
		return err
	}
	memo, err := h.repo.SaveServiceWithdrawalRequest(req.Context(), w)
	if err != nil {
		writeHttpError(resp, http.StatusInternalServerError, fmt.Sprintf("save service withdrawal request err: %v", err))
		return err
	}
	var response = types.ServiceWithdrawalResponse{
		Memo: memo,
	}
	resp.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(resp).Encode(response)

	return err
}

func (h *Handler) serviceJettonWithdrawal(resp http.ResponseWriter, req bunrouter.Request) error {
	var body types.ServiceJettonWithdrawalRequest

	err := json.NewDecoder(req.Body).Decode(&body)
	if err != nil {
		writeHttpError(resp, http.StatusBadRequest, fmt.Sprintf("decode payload err: %v", err))
		return err
	}
	w, err := convertJettonServiceWithdrawal(h.repo, body)
	if err != nil {
		writeHttpError(resp, http.StatusBadRequest, fmt.Sprintf("convert service withdrawal err: %v", err))
		return err
	}
	memo, err := h.repo.SaveServiceWithdrawalRequest(req.Context(), w)
	if err != nil {
		writeHttpError(resp, http.StatusInternalServerError, fmt.Sprintf("save service withdrawal request err: %v", err))
		return err
	}
	var response = types.ServiceWithdrawalResponse{
		Memo: memo,
	}
	resp.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(resp).Encode(response)

	return err
}

func convertTonServiceWithdrawal(repo db.Repository, w types.ServiceTonWithdrawalRequest) (models.ServiceWithdrawalRequest, error) {
	from, err := parseAddress(w.From)
	if err != nil {
		return models.ServiceWithdrawalRequest{}, fmt.Errorf("invalid from address: %v", err)
	}
	t, ok := repo.GetWalletType(from)
	if !ok {
		return models.ServiceWithdrawalRequest{}, fmt.Errorf("unknown deposit address")
	}
	if t != models.JettonOwner {
		return models.ServiceWithdrawalRequest{},
			fmt.Errorf("service withdrawal allowed only for Jetton deposit owner")
	}
	return models.ServiceWithdrawalRequest{
		From: from,
	}, nil
}

func convertJettonServiceWithdrawal(repo db.Repository, w types.ServiceJettonWithdrawalRequest) (models.ServiceWithdrawalRequest, error) {
	from, err := parseAddress(w.Owner)
	if err != nil {
		return models.ServiceWithdrawalRequest{}, fmt.Errorf("invalid from address: %v", err)
	}
	t, ok := repo.GetWalletType(from)
	if !ok {
		return models.ServiceWithdrawalRequest{}, fmt.Errorf("unknown deposit address")
	}
	if t != models.JettonOwner && t != models.TonDepositWallet {
		return models.ServiceWithdrawalRequest{},
			fmt.Errorf("service withdrawal allowed only for Jetton deposit owner or TON deposit")
	}
	jetton, err := parseAddress(w.JettonMaster)
	if err != nil {
		return models.ServiceWithdrawalRequest{}, fmt.Errorf("invalid jetton master address: %v", err)
	}
	// currency type checks by withdrawal processor
	return models.ServiceWithdrawalRequest{
		From:         from,
		JettonMaster: &jetton,
	}, nil
}
