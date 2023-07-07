package core

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/gobicycle/bicycle/config"
	"github.com/gobicycle/bicycle/models"
	log "github.com/sirupsen/logrus"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton"
	"github.com/xssnick/tonutils-go/ton/jetton"
	"github.com/xssnick/tonutils-go/ton/wallet"
)

type connection struct {
	client *ton.APIClient
}

// NewConnection creates new Blockchain connection
func NewConnection(ctx context.Context, tonApi *ton.APIClient) (Blockchain, error) {
	conn := &connection{tonApi}
	isTimeSynced, err := conn.isTimeSynced(ctx, config.AllowableServiceToNodeTimeDiff)
	if err != nil {
		log.Fatalf("get node time err: %v", err)
	}
	if !isTimeSynced {
		log.Fatalf("Service and Node time not synced")
	}
	return conn, nil
}

func (c *connection) StickyContext(ctx context.Context) context.Context {
	return c.client.Client().StickyContext(ctx)
}

func (c *connection) JettonClient(masterAddr *address.Address) *jetton.Client {
	return jetton.NewJettonMasterClient(c.client, masterAddr)
}

func (c *connection) GetWalletData(ctx context.Context, address *address.Address, blockID *ton.BlockIDExt) (*ton.ExecutionResult, error) {
	stack, err := c.RunGetMethod(ctx, blockID, address, "get_wallet_data")
	if err != nil {
		if strings.Contains(err.Error(), "contract is not initialized") {
			return nil, err
		}
		return nil, fmt.Errorf("get wallet data error: %v", err)
	}
	res := stack.AsTuple()
	if len(res) != 4 {
		return nil, fmt.Errorf("invalid stack size")
	}

	return stack, nil
}

func (c *connection) GetLastWalletData(ctx context.Context, addr *address.Address) (*ton.ExecutionResult, error) {
	masterID, err := c.client.CurrentMasterchainInfo(ctx)
	if err != nil {
		return nil, err
	}

	return c.GetWalletData(ctx, addr, masterID)
}

// GetAccountCurrentState
// Returns TON balance in nanoTONs and account status
func (c *connection) GetAccountCurrentState(ctx context.Context, address *address.Address) (*big.Int, tlb.AccountStatus, error) {
	masterID, err := c.client.CurrentMasterchainInfo(ctx)
	if err != nil {
		return nil, "", err
	}
	account, err := c.GetAccount(ctx, masterID, address)
	if err != nil {
		return nil, "", err
	}
	if !account.IsActive {
		return big.NewInt(0), tlb.AccountStatusNonExist, nil
	}
	return account.State.Balance.NanoTON(), account.State.Status, nil
}

// GenerateWallet generates TON wallet with given version
// default subwallet_id and returns wallet, shard and subwalletID
func (c *connection) GenerateWallet(seed string, v wallet.Version) (
	w *wallet.Wallet,
	shard byte,
	subwalletID uint32, err error,
) {
	words := strings.Split(seed, " ")
	w, err = wallet.FromSeed(c.client, words, v)
	if err != nil {
		return nil, 0, 0, err
	}
	return w, w.Address().Data()[0], uint32(wallet.DefaultSubwallet), nil
}

// GenerateHighloadWallet generates HighloadV2R2 TON wallet with
// default subwallet_id and returns wallet, shard and subwalletID
func (c *connection) GenerateHighloadWallet(seed string) (
	w *wallet.Wallet,
	shard byte,
	subwalletID uint32, err error,
) {
	return c.GenerateWallet(seed, wallet.HighloadV2R2)
}

// GetTransactionIDsFromBlock
// Gets all transactions IDs from custom block
func (c *connection) GetTransactionIDsFromBlock(ctx context.Context, blockID *ton.BlockIDExt) ([]ton.TransactionShortInfo, error) {
	var (
		txIDList []ton.TransactionShortInfo
		after    *ton.TransactionID3
		next     = true
	)
	for next {
		fetchedIDs, more, err := c.client.GetBlockTransactionsV2(ctx, blockID, 256, after)
		if err != nil {
			return nil, err
		}
		txIDList = append(txIDList, fetchedIDs...)
		next = more
		if more {
			// set load offset for next query (pagination)
			after = fetchedIDs[len(fetchedIDs)-1].ID3()
		}
	}
	// sort by LT
	sort.Slice(txIDList, func(i, j int) bool {
		return txIDList[i].LT < txIDList[j].LT
	})
	return txIDList, nil
}

// GetTransactionFromBlock
// Gets transaction from block
func (c *connection) GetTransactionFromBlock(ctx context.Context, blockID *ton.BlockIDExt, txID ton.TransactionShortInfo) (*tlb.Transaction, error) {
	tx, err := c.client.GetTransaction(ctx, blockID, address.NewAddress(0, byte(blockID.Workchain), txID.Account), txID.LT)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

func (c *connection) getCurrentNodeTime(ctx context.Context) (time.Time, error) {
	t, err := c.client.GetTime(ctx)
	if err != nil {
		return time.Time{}, err
	}
	res := time.Unix(int64(t), 0)
	return res, nil
}

// isTimeSynced
// Checks time diff between node and local time. Due to the fact that the request to the node takes time,
// the local time is defined as the average between the beginning and end of the request.
// Returns true if time diff < cutoff.
func (c *connection) isTimeSynced(ctx context.Context, cutoff time.Duration) (bool, error) {
	prevTime := time.Now()
	nodeTime, err := c.getCurrentNodeTime(ctx)
	if err != nil {
		return false, err
	}
	nextTime := time.Now()
	midTime := prevTime.Add(nextTime.Sub(prevTime) / 2)
	nodeTimeShift := midTime.Sub(nodeTime)
	log.Infof("Service-Node time diff: %v", nodeTimeShift)
	if nodeTimeShift > cutoff || nodeTimeShift < -cutoff {
		return false, nil
	}
	return true, nil
}

// WaitStatus
// Waits custom status for account. Returns error if context timeout is exceeded.
// Context must be with timeout to avoid blocking!
func (c *connection) WaitStatus(ctx context.Context, addr *address.Address, status tlb.AccountStatus) error {
	for {
		select {
		case <-ctx.Done():
			return models.ErrTimeoutExceeded
		default:
			_, st, err := c.GetAccountCurrentState(ctx, addr)
			if err != nil {
				return err
			}
			if st == status {
				return nil
			}
			time.Sleep(time.Millisecond * 200)
		}
	}
}

// tonutils TonAPI interface methods

// GetAccount
// The method is being redefined for more stable operation.
// Gets account from prev block if impossible to get it from current block. Be careful with diff calculation between blocks.
func (c *connection) GetAccount(ctx context.Context, block *ton.BlockIDExt, addr *address.Address) (*tlb.Account, error) {
	res, err := c.client.GetAccount(ctx, block, addr)
	if err != nil && strings.Contains(err.Error(), ErrBlockNotApplied) {
		prevBlock, err := c.client.LookupBlock(ctx, block.Workchain, block.Shard, block.SeqNo-1)
		if err != nil {
			return nil, err
		}
		return c.client.GetAccount(ctx, prevBlock, addr)
	}
	return res, err
}

func (c *connection) SendExternalMessage(ctx context.Context, msg *tlb.ExternalMessage) error {
	return c.client.SendExternalMessage(ctx, msg)
}

// RunGetMethod
// The method is being redefined for more stable operation
// Wait until BlockIsApplied. Use context with  timeout.
func (c *connection) RunGetMethod(ctx context.Context, block *ton.BlockIDExt, addr *address.Address, method string, params ...any) (*ton.ExecutionResult, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, models.ErrTimeoutExceeded
		default:
			res, err := c.client.RunGetMethod(ctx, block, addr, method, params...)
			if err != nil && strings.Contains(err.Error(), ErrBlockNotApplied) {
				time.Sleep(time.Millisecond * 200)
				continue
			}
			return res, err
		}
	}
}

func (c *connection) ListTransactions(ctx context.Context, addr *address.Address, num uint32, lt uint64, txHash []byte) ([]*tlb.Transaction, error) {
	return c.client.ListTransactions(ctx, addr, num, lt, txHash)
}

func (c *connection) WaitNextMasterBlock(ctx context.Context, master *ton.BlockIDExt) (*ton.BlockIDExt, error) {
	return c.client.WaitNextMasterBlock(ctx, master)
}

func (c *connection) CurrentMasterchainInfo(ctx context.Context) (*ton.BlockIDExt, error) {
	return c.client.CurrentMasterchainInfo(ctx)
}

func (c *connection) GetMasterchainInfo(ctx context.Context) (*ton.BlockIDExt, error) {
	return c.client.GetMasterchainInfo(ctx)
}
