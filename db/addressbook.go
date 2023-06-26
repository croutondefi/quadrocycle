package db

import (
	"context"
	"sync"

	"github.com/gobicycle/bicycle/models"
	"github.com/jackc/pgx/v5/pgxpool"
	log "github.com/sirupsen/logrus"
	"github.com/xssnick/tonutils-go/address"
)

func NewAddressBook(ctx context.Context, client *pgxpool.Pool) (AddressBook, error) {
	ab := &addressBook{}

	err := ab.loadAddressBook(ctx, client)
	if err != nil {
		return nil, err
	}
	return &addressBook{}, nil
}

type addressBook struct {
	addresses map[models.Address]models.AddressInfo
	mutex     sync.Mutex
}

func (ab *addressBook) GetWalletType(address models.Address) (models.WalletType, bool) {
	info, ok := ab.get(address)
	return info.Type, ok
}

// GetOwner returns owner for jetton deposit from address book and nil for other types
func (ab *addressBook) GetOwner(address models.Address) *models.Address {
	info, ok := ab.get(address)
	if ok && info.Type == models.JettonDepositWallet && info.Owner == nil {
		log.Fatalf("must be owner address in address book for jetton deposit")
	}
	return info.Owner
}

func (ab *addressBook) GetWalletTypeByTonutilsAddress(address *address.Address) (models.WalletType, bool) {
	a, err := models.AddressFromTonutilsAddress(address)
	if err != nil {
		return "", false
	}
	return ab.GetWalletType(a)
}

func (ab *addressBook) get(address models.Address) (models.AddressInfo, bool) {
	ab.mutex.Lock()
	t, ok := ab.addresses[address]
	ab.mutex.Unlock()
	return t, ok
}

func (ab *addressBook) put(address models.Address, t models.AddressInfo) {
	ab.mutex.Lock()
	ab.addresses[address] = t
	ab.mutex.Unlock()
}

func (ab *addressBook) loadAddressBook(ctx context.Context, db *pgxpool.Pool) error {
	res := make(map[models.Address]models.AddressInfo)
	var (
		addr models.Address
		t    models.WalletType
	)

	rows, err := db.Query(ctx, `
		SELECT address, type
		FROM payments.ton_wallets
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		err = rows.Scan(&addr, &t)
		if err != nil {
			return err
		}
		res[addr] = models.AddressInfo{Type: t, Owner: nil}
	}

	rows, err = db.Query(ctx, `
		SELECT jw.address, jw.type, tw.address
		FROM payments.jetton_wallets jw
		LEFT JOIN payments.ton_wallets tw ON jw.subwallet_id = tw.subwallet_id
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var owner models.Address
		err = rows.Scan(&addr, &t, &owner)
		if err != nil {
			return err
		}
		res[addr] = models.AddressInfo{Type: t, Owner: &owner}
	}

	ab.addresses = res
	log.Infof("Address book loaded: %d", len(ab.addresses))
	return nil
}
