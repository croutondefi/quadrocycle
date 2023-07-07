package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gobicycle/bicycle/api/types"
	"github.com/uptrace/bunrouter"
)

func (h *Handler) createAddress(w http.ResponseWriter, req bunrouter.Request) error {
	var data = types.CreateAddressRequest{}

	err := json.NewDecoder(req.Body).Decode(&data)
	if err != nil {
		writeHttpError(w, http.StatusBadRequest, fmt.Sprintf("decode payload data err: %v", err))
		return err
	}

	addr, err := h.AddressUsecases.CreateAddress(req.Context(), data.UserID, data.Currency)
	if err != nil {
		writeHttpError(w, http.StatusInternalServerError, fmt.Sprintf("generate address err: %v", err))
		return nil
	}
	res := types.CreateAddressResponse{
		Address: addr,
	}
	w.WriteHeader(http.StatusOK)
	return bunrouter.JSON(w, res)
}

func (h *Handler) getAddresses(w http.ResponseWriter, req bunrouter.Request) error {
	userID, err := req.Params().Int64("user_id")
	if err != nil {
		writeHttpError(w, http.StatusBadRequest, fmt.Sprintf("failed to parse user_id: %v", err))
		return err
	}

	res, err := h.AddressUsecases.GetAddresses(req.Context(), userID)
	if err != nil {
		writeHttpError(w, http.StatusInternalServerError, fmt.Sprintf("get addresses err: %v", err))
		return err
	}
	w.WriteHeader(http.StatusOK)

	return bunrouter.JSON(w, res)
}
