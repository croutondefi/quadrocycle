package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gobicycle/bicycle/api/types"
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
	id, err := req.Params().Int64("id")
	if err != nil {
		writeHttpError(resp, http.StatusBadRequest, fmt.Sprintf("convert request ID err: %v", err))
		return err
	}

	status, err := h.withdrawalUsecases.GetWithdrawalStatus(req.Context(), id)
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
	memo, err := h.withdrawalUsecases.CreateServiceTonWithdrawal(req.Context(), body)
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
	memo, err := h.withdrawalUsecases.CreateServiceJettonWithdrawal(req.Context(), body)
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
