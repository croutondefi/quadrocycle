package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gobicycle/bicycle/api/usecases"
	log "github.com/sirupsen/logrus"
	"github.com/uptrace/bunrouter"
)

type Handler struct {
	withdrawalUsecases usecases.WithdrawalUsecases
	incomeUsecases     usecases.IncomeUsecases
	AddressUsecases    usecases.AddressUsecases
	SyncUsecases       usecases.SyncUsecases
}

func NewHandler() *Handler {
	return &Handler{}
}

func (h *Handler) Register(g *bunrouter.Group) {
	g.POST("/address", h.createAddress)
	g.GET("/address/all", h.getAddresses)

	g.POST("/withdrawal", h.createWithdrawal)
	g.POST("/withdrawal/service/ton", h.serviceTonWithdrawal)
	g.POST("/withdrawal/service/jetton", h.serviceJettonWithdrawal)
	g.GET("/withdrawal/status", h.getWithdrawalStatus)

	g.GET("/system/sync", h.getSync)

	g.GET("/income/:type", h.getIncome)

	g.GET("/deposit/history", h.getIncomeHistory)
}

func writeHttpError(resp http.ResponseWriter, status int, comment string) {
	body := struct {
		Error string `json:"error"`
	}{
		Error: comment,
	}
	resp.WriteHeader(status)
	err := json.NewEncoder(resp).Encode(body)
	if err != nil {
		log.Errorf("json encode error: %v", err)
	}
}
