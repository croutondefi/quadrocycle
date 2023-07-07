package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gobicycle/bicycle/api/types"
	"github.com/gorilla/schema"
	"github.com/uptrace/bunrouter"
)

func (h *Handler) getIncome(resp http.ResponseWriter, req bunrouter.Request) error {
	id, err := req.Params().Int64("user_id")
	if err != nil {
		writeHttpError(resp, http.StatusBadRequest, fmt.Sprintf("failed to parse iser_id: %s", err))
		return nil
	}

	incomeType := req.Params().ByName("type")

	isInternal := (incomeType == "internal")

	ii, err := h.incomeUsecases.GetIncome(req.Context(), id, isInternal)
	if err != nil {
		writeHttpError(resp, http.StatusInternalServerError, fmt.Sprintf("get balances err: %v", err))
		return err
	}
	resp.WriteHeader(http.StatusOK)
	json.NewEncoder(resp).Encode(ii)

	return nil
}

func (h *Handler) getIncomeHistory(resp http.ResponseWriter, req bunrouter.Request) error {
	data := &types.GetHistoryRequest{}

	d := schema.NewDecoder()
	err := d.Decode(data, req.URL.Query())

	hh, err := h.incomeUsecases.GetHistory(req.Context(), data)
	if err != nil {
		writeHttpError(resp, http.StatusBadRequest, fmt.Sprintf("GetHistory err: %v", err))
		return err
	}

	resp.WriteHeader(http.StatusOK)
	json.NewEncoder(resp).Encode(hh)
	return nil
}
