package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gobicycle/bicycle/api/handlers"
	"github.com/gobicycle/bicycle/api/types"
	"github.com/gobicycle/bicycle/config"
	"github.com/gobicycle/bicycle/models"
	"github.com/gofrs/uuid"
	"github.com/shopspring/decimal"
)

type Client struct {
	client *http.Client
	urlA   string
	urlB   string
	token  string
	userID string
}

func NewClient(urlA, urlB, token, userID string) *Client {
	c := &Client{
		client: &http.Client{Timeout: 10 * time.Second},
		urlA:   urlA,
		urlB:   urlB,
		token:  token,
		userID: userID,
	}
	return c
}

func (s *Client) InitDeposits(host string) (map[string][]string, error) {
	deposits := make(map[string][]string)
	addr, err := s.GetAllAddresses(host)
	if err != nil {
		return nil, err
	}
	if len(addr.Addresses) != 0 {
		for _, wa := range addr.Addresses {
			deposits[wa.Currency] = append(deposits[wa.Currency], wa.Address)
		}
		return deposits, nil
	}
	for i := 0; i < depositsQty; i++ {
		addr, err := s.GetNewAddress(host, models.TonSymbol)
		if err != nil {
			return nil, err
		}
		deposits[models.TonSymbol] = append(deposits[models.TonSymbol], addr)
	}
	for i := 0; i < depositsQty; i++ {
		for cur := range config.Config.Jettons {
			addr, err := s.GetNewAddress(host, cur)
			if err != nil {
				return nil, err
			}
			deposits[cur] = append(deposits[cur], addr)
		}
	}
	log.Printf("Deposits initialized for %s", host)
	return deposits, nil
}

func (s *Client) GetAllAddresses(host string) (handlers.GetAddressesResponse, error) {
	url := fmt.Sprintf("http://%s/v1/address/all?user_id=%s", host, s.userID)
	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return handlers.GetAddressesResponse{}, err
	}
	request.Header.Add("Authorization", "Bearer "+s.token)
	response, err := s.client.Do(request)
	if err != nil {
		return handlers.GetAddressesResponse{}, err
	}
	defer func() {
		err := response.Body.Close()
		if err != nil {
			log.Fatalf("response body close error: %v", err)
		}
	}()
	if response.StatusCode >= 300 {
		return handlers.GetAddressesResponse{}, fmt.Errorf("response status: %v", response.Status)
	}
	content, err := io.ReadAll(response.Body)
	if err != nil {
		return handlers.GetAddressesResponse{}, err
	}
	var res handlers.GetAddressesResponse
	err = json.Unmarshal(content, &res)
	if err != nil {
		return handlers.GetAddressesResponse{}, err
	}
	return res, nil
}

func (s *Client) SendWithdrawal(host, currency, destination string, amount int64) (types.WithdrawalResponse, uuid.UUID, error) {
	url := fmt.Sprintf("http://%s/v1/withdrawal/send", host)
	u, err := uuid.NewV4()
	if err != nil {
		return types.WithdrawalResponse{}, uuid.UUID{}, err
	}
	reqData := types.CreateWithdrawalRequest{
		UserID:      s.userID,
		QueryID:     u.String(),
		Currency:    currency,
		Amount:      decimal.New(amount, 0),
		Destination: destination,
		Comment:     u.String(),
	}
	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return types.WithdrawalResponse{}, uuid.UUID{}, err
	}
	request, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return types.WithdrawalResponse{}, uuid.UUID{}, err
	}
	request.Header.Set("Content-Type", "application/json; charset=UTF-8")
	request.Header.Add("Authorization", "Bearer "+s.token)
	response, err := s.client.Do(request)
	if err != nil {
		return types.WithdrawalResponse{}, uuid.UUID{}, err
	}
	defer func() {
		err := response.Body.Close()
		if err != nil {
			log.Fatalf("response body close error: %v", err)
		}
	}()
	if response.StatusCode >= 300 {
		return types.WithdrawalResponse{}, uuid.UUID{}, fmt.Errorf("response status: %v", response.Status)
	}
	content, err := io.ReadAll(response.Body)
	if err != nil {
		return types.WithdrawalResponse{}, uuid.UUID{}, err
	}
	var res types.WithdrawalResponse
	err = json.Unmarshal(content, &res)
	if err != nil {
		return types.WithdrawalResponse{}, uuid.UUID{}, err
	}
	return res, u, nil
}

func (s *Client) GetNewAddress(host, currency string) (string, error) {
	url := fmt.Sprintf("http://%s/v1/address/new", host)
	reqData := struct {
		UserID   string `json:"user_id"`
		Currency string `json:"currency"`
	}{
		UserID:   s.userID,
		Currency: currency,
	}
	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json; charset=UTF-8")
	request.Header.Add("Authorization", "Bearer "+s.token)
	response, err := s.client.Do(request)
	if err != nil {
		return "", err
	}
	defer func() {
		err := response.Body.Close()
		if err != nil {
			log.Fatalf("response body close error: %v", err)
		}
	}()
	if response.StatusCode >= 300 {
		return "", fmt.Errorf("response status: %v", response.Status)
	}
	content, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	var res struct {
		Address string `json:"address"`
	}
	err = json.Unmarshal(content, &res)
	if err != nil {
		return "", err
	}
	return res.Address, nil
}

func (s *Client) GetWithdrawalStatus(host string, id int64) (types.WithdrawalStatusResponse, error) {
	url := fmt.Sprintf("http://%s/v1/withdrawal/status?id=%v", host, id)
	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return types.WithdrawalStatusResponse{}, err
	}
	request.Header.Add("Authorization", "Bearer "+s.token)
	response, err := s.client.Do(request)
	if err != nil {
		return types.WithdrawalStatusResponse{}, err
	}
	defer func() {
		err := response.Body.Close()
		if err != nil {
			log.Fatalf("response body close error: %v", err)
		}
	}()
	if response.StatusCode >= 300 {
		return types.WithdrawalStatusResponse{}, fmt.Errorf("response status: %v", response.Status)
	}
	content, err := io.ReadAll(response.Body)
	if err != nil {
		return types.WithdrawalStatusResponse{}, err
	}
	var res types.WithdrawalStatusResponse
	err = json.Unmarshal(content, &res)
	if err != nil {
		return types.WithdrawalStatusResponse{}, err
	}
	return res, nil
}
