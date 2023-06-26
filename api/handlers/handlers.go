package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gobicycle/bicycle/api/usecases"
	"github.com/gobicycle/bicycle/config"
	"github.com/gobicycle/bicycle/core"
	"github.com/gobicycle/bicycle/db"
	"github.com/gobicycle/bicycle/models"
	log "github.com/sirupsen/logrus"
	"github.com/uptrace/bunrouter"
	"github.com/xssnick/tonutils-go/address"
)

type Handler struct {
	repo               db.Repository
	blockchain         core.Blockchain
	shard              byte
	hotWalletAddress   address.Address
	withdrawalUsecases usecases.WithdrawalUsecases
}

func NewHandler(repo db.Repository, b core.Blockchain, shard byte, hotWalletAddress address.Address) *Handler {
	return &Handler{repo: repo, blockchain: b, shard: shard, hotWalletAddress: hotWalletAddress}
}

func (h *Handler) Register(g *bunrouter.Group) {
	g.POST("/address", h.createAddress)
	g.GET("/address/all", h.getAddresses)

	g.POST("/withdrawal", h.createWithdrawal)
	g.POST("/withdrawal/service/ton", h.serviceTonWithdrawal)
	g.POST("/withdrawal/service/jetton", h.serviceJettonWithdrawal)
	g.GET("/withdrawal/status", h.getWithdrawalStatus)

	g.GET("/system/sync", h.getSync)

	g.GET("/income", h.getIncome)

	g.GET("/deposit/history", h.getIncomeHistory)
}

func isValidCurrency(cur string) bool {
	if _, ok := config.Config.Jettons[cur]; ok || cur == models.TonSymbol {
		return true
	}
	return false
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
