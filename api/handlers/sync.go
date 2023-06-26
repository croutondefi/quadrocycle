package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	log "github.com/sirupsen/logrus"
	"github.com/uptrace/bunrouter"
)

func (h *Handler) getSync(resp http.ResponseWriter, req bunrouter.Request) error {
	isSynced, err := h.repo.IsActualBlockData(req.Context())
	if err != nil {
		writeHttpError(resp, http.StatusInternalServerError, fmt.Sprintf("get sync from db err: %v", err))
		return err
	}
	getSyncResponse := struct {
		IsSynced bool `json:"is_synced"`
	}{
		IsSynced: isSynced,
	}
	resp.WriteHeader(http.StatusOK)
	err = json.NewEncoder(resp).Encode(getSyncResponse)
	if err != nil {
		log.Errorf("json encode error: %v", err)
	}

	return err
}
