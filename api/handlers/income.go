package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gobicycle/bicycle/config"
	"github.com/gobicycle/bicycle/db"
	"github.com/gobicycle/bicycle/models"
	log "github.com/sirupsen/logrus"
	"github.com/uptrace/bunrouter"
	"github.com/xssnick/tonutils-go/address"
)

type GetIncomeResponse struct {
	Side         string        `json:"counting_side"`
	TotalIncomes []totalIncome `json:"total_income"`
}

type GetHistoryResponse struct {
	Incomes []income `json:"incomes"`
}

type totalIncome struct {
	Address  string `json:"deposit_address"`
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

type income struct {
	DepositAddress string `json:"deposit_address"`
	Time           int64  `json:"time"`
	SourceAddress  string `json:"source_address,omitempty"`
	Amount         string `json:"amount"`
	Comment        string `json:"comment,omitempty"`
}

func (h *Handler) getIncome(resp http.ResponseWriter, req bunrouter.Request) error {
	id := req.URL.Query().Get("user_id")
	if id == "" {
		writeHttpError(resp, http.StatusBadRequest, "need to provide user ID")
		return nil
	}
	totalIncomes, err := h.repo.GetIncome(req.Context(), id, config.Config.IsDepositSideCalculation)
	if err != nil {
		writeHttpError(resp, http.StatusInternalServerError, fmt.Sprintf("get balances err: %v", err))
		return err
	}
	resp.WriteHeader(http.StatusOK)
	err = json.NewEncoder(resp).Encode(convertIncome(h.repo, totalIncomes))
	if err != nil {
		log.Errorf("json encode error: %v", err)
	}

	return err
}

func (h *Handler) getIncomeHistory(resp http.ResponseWriter, req bunrouter.Request) error {
	id := req.URL.Query().Get("user_id")
	if id == "" {
		writeHttpError(resp, http.StatusBadRequest, "need to provide user ID")
		return nil
	}
	currency := req.URL.Query().Get("currency")
	if currency == "" {
		writeHttpError(resp, http.StatusBadRequest, "need to provide currency")
		return nil
	}
	if !isValidCurrency(currency) {
		writeHttpError(resp, http.StatusBadRequest, "invalid currency type")
		return nil
	}
	limit, err := strconv.Atoi(req.URL.Query().Get("limit"))
	if err != nil {
		writeHttpError(resp, http.StatusBadRequest, "invalid limit parameter")
		return err
	}
	offset, err := strconv.Atoi(req.URL.Query().Get("offset"))
	if err != nil {
		writeHttpError(resp, http.StatusBadRequest, "invalid offset parameter")
		return err
	}
	history, err := h.repo.GetIncomeHistory(req.Context(), id, currency, limit, offset)
	if err != nil {
		writeHttpError(resp, http.StatusInternalServerError, fmt.Sprintf("get history err: %v", err))
		return err
	}

	resp.WriteHeader(http.StatusOK)
	err = json.NewEncoder(resp).Encode(convertHistory(h.repo, currency, history))
	if err != nil {
		log.Errorf("json encode error: %v", err)
	}
	return err
}

func convertIncome(repo db.Repository, totalIncomes []models.TotalIncome) GetIncomeResponse {
	var res = GetIncomeResponse{
		TotalIncomes: []totalIncome{},
	}
	if config.Config.IsDepositSideCalculation {
		res.Side = models.SideDeposit
	} else {
		res.Side = models.SideHotWallet
	}

	for _, b := range totalIncomes {
		totIncome := totalIncome{
			Amount:   b.Amount.String(),
			Currency: b.Currency,
		}
		if b.Currency == models.TonSymbol {
			totIncome.Address = b.Deposit.ToUserFormat()
		} else {
			owner := repo.GetOwner(b.Deposit)
			if owner == nil {
				// TODO: remove fatal
				log.Fatalf("can not find owner for deposit: %s", b.Deposit.ToUserFormat())
			}
			totIncome.Address = owner.ToUserFormat()
		}
		res.TotalIncomes = append(res.TotalIncomes, totIncome)
	}
	return res
}

func convertHistory(repo db.Repository, currency string, incomes []models.ExternalIncome) GetHistoryResponse {
	var res = GetHistoryResponse{
		Incomes: []income{},
	}
	for _, i := range incomes {
		inc := income{
			Time:    int64(i.Utime),
			Amount:  i.Amount.String(),
			Comment: i.Comment,
		}
		if currency == models.TonSymbol {
			inc.DepositAddress = i.To.ToUserFormat()
		} else {
			owner := repo.GetOwner(i.To)
			if owner == nil {
				// TODO: remove fatal
				log.Fatalf("can not find owner for deposit: %s", i.To.ToUserFormat())
			}
			inc.DepositAddress = owner.ToUserFormat()
		}
		// show only std address
		if len(i.From) == 32 && i.FromWorkchain != nil {
			addr := address.NewAddress(0, byte(*i.FromWorkchain), i.From)
			addr.SetTestnetOnly(config.Config.Testnet)
			inc.SourceAddress = addr.String()
		}
		res.Incomes = append(res.Incomes, inc)
	}
	return res
}
