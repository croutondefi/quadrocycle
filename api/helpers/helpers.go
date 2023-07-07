package helpers

import (
	"fmt"

	"github.com/gobicycle/bicycle/config"
	"github.com/gobicycle/bicycle/models"
	"github.com/xssnick/tonutils-go/address"
)

func ParseAddress(addr string) (models.Address, error) {
	if addr == "" {
		return models.Address{}, fmt.Errorf("empty address")
	}
	a, err := address.ParseAddr(addr)
	if err != nil {
		return models.Address{}, fmt.Errorf("invalid address: %v", err)
	}
	if a.IsTestnetOnly() && !config.Config.Testnet {
		return models.Address{}, fmt.Errorf("address for testnet only")
	}
	if a.Workchain() != models.DefaultWorkchain {
		return models.Address{}, fmt.Errorf("address must be in %d workchain",
			models.DefaultWorkchain)
	}
	res, err := models.AddressFromTonutilsAddress(a)
	return res, err
}

func IsValidCurrency(cur string) bool {
	if _, ok := config.Config.Jettons[cur]; ok || cur == models.TonSymbol {
		return true
	}
	return false
}
