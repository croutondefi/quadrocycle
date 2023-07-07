package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/uptrace/bunrouter"
)

type GetSyncResponse struct {
	IsSynced bool `json:"is_synced"`
}

func (h *Handler) getSync(resp http.ResponseWriter, req bunrouter.Request) error {
	isSynced, err := h.SyncUsecases.IsSynced(req.Context())

	if err != nil {
		writeHttpError(resp, http.StatusInternalServerError, fmt.Sprintf("failed to sync: %v", err))
		return err
	}

	resp.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(resp).Encode(GetSyncResponse{
		IsSynced: isSynced,
	})
	return nil
}
