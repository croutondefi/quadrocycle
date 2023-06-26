package core

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/gobicycle/bicycle/config"
	"github.com/gobicycle/bicycle/models"
	log "github.com/sirupsen/logrus"
	"github.com/tonkeeper/tongo"
	"github.com/tonkeeper/tongo/boc"
	tongoTlb "github.com/tonkeeper/tongo/tlb"
	"github.com/tonkeeper/tongo/tvm"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton"
	"github.com/xssnick/tonutils-go/ton/wallet"
)

type connection struct {
	client               *ton.APIClient
	defaultWalletVersion wallet.Version
}

type contract struct {
	Address tongo.AccountID
	Code    *boc.Cell
	Data    *boc.Cell
}

// NewConnection creates new Blockchain connection
func NewConnection(ctx context.Context, tonApi *ton.APIClient, defaultVersion wallet.Version) (Blockchain, error) {
	conn := &connection{tonApi, defaultVersion}
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

// GenerateDefaultWallet generates HighloadV2R2 or V3R2 TON wallet with
// default subwallet_id and returns wallet, shard and subwalletID
func (c *connection) GenerateDefaultWallet(seed string, isHighload bool) (
	w *wallet.Wallet,
	shard byte,
	subwalletID uint32, err error,
) {
	words := strings.Split(seed, " ")
	if isHighload {
		w, err = wallet.FromSeed(c.client, words, wallet.HighloadV2R2)
	} else {
		w, err = wallet.FromSeed(c.client, words, c.defaultWalletVersion)
	}
	if err != nil {
		return nil, 0, 0, err
	}
	return w, w.Address().Data()[0], uint32(wallet.DefaultSubwallet), nil
}

// GenerateSubWallet generates subwallet for custom shard and
// subwallet_id >= startSubWalletId and returns wallet and new subwallet_id
func (c *connection) GenerateSubWallet(seed string, shard byte, startSubWalletID uint32) (*wallet.Wallet, uint32, error) {
	words := strings.Split(seed, " ")
	basic, err := wallet.FromSeed(c.client, words, wallet.V3)
	if err != nil {
		return nil, 0, err
	}
	for id := startSubWalletID; id < math.MaxUint32; id++ {
		subWallet, err := basic.GetSubwallet(id)
		if err != nil {
			return nil, 0, err
		}
		addr, err := models.AddressFromTonutilsAddress(subWallet.Address())
		if err != nil {
			return nil, 0, err
		}
		if inShard(addr, shard) {
			return subWallet, id, nil
		}
	}
	return nil, 0, fmt.Errorf("subwallet not found")
}

// GetJettonWalletAddress generates jetton wallet address from owner and jetton master addresses
func (c *connection) GetJettonWalletAddress(
	ctx context.Context,
	owner *address.Address,
	jettonMaster *address.Address,
) (*address.Address, error) {
	contr, err := c.getContract(ctx, jettonMaster)
	if err != nil {
		return nil, err
	}
	emulator, err := newEmulator(contr.Code, contr.Data)
	if err != nil {
		return nil, err
	}
	addr, err := getJettonWalletAddressByTVM(owner, contr.Address, emulator)
	if err != nil {
		return nil, err
	}
	res := addr.ToTonutilsAddressStd(0)
	res.SetTestnetOnly(config.Config.Testnet)
	return res, nil
}

// GenerateDepositJettonWalletForProxy
// Generates jetton wallet address for custom shard and proxy contract as owner with subwallet_id >= startSubWalletId
func (c *connection) GenerateDepositJettonWalletForProxy(
	ctx context.Context,
	shard byte,
	proxyOwner, jettonMaster *address.Address,
	startSubWalletID uint32,
) (
	proxy *JettonProxy,
	addr *address.Address,
	err error,
) {
	contr, err := c.getContract(ctx, jettonMaster)
	if err != nil {
		return nil, nil, err
	}
	emulator, err := newEmulator(contr.Code, contr.Data)
	if err != nil {
		return nil, nil, err
	}

	for id := startSubWalletID; id < math.MaxUint32; id++ {
		proxy, err = NewJettonProxy(id, proxyOwner)
		if err != nil {
			return nil, nil, err
		}
		jettonWalletAddress, err := getJettonWalletAddressByTVM(proxy.Address(), contr.Address, emulator)
		if err != nil {
			return nil, nil, err
		}
		if inShard(jettonWalletAddress, shard) {
			addr = jettonWalletAddress.ToTonutilsAddressStd(0)
			addr.SetTestnetOnly(config.Config.Testnet)
			return proxy, addr, nil
		}
	}
	return nil, nil, fmt.Errorf("jetton wallet address not found")
}

func (c *connection) getContract(ctx context.Context, addr *address.Address) (contract, error) {
	block, err := c.client.CurrentMasterchainInfo(ctx)
	if err != nil {
		return contract{}, err
	}
	account, err := c.GetAccount(ctx, block, addr)
	if err != nil {
		return contract{}, err
	}
	if account == nil || account.Code == nil || account.Data == nil {
		return contract{}, fmt.Errorf("empty account code or data")
	}
	accountID, err := tongo.ParseAccountID(addr.String())
	if err != nil {
		return contract{}, err
	}
	codeCell, err := boc.DeserializeBoc(account.Code.ToBOC())
	if err != nil {
		return contract{}, err
	}
	if len(codeCell) != 1 {
		return contract{}, fmt.Errorf("BOC must have only one root")
	}
	dataCell, err := boc.DeserializeBoc(account.Data.ToBOC())
	if err != nil {
		return contract{}, err
	}
	if len(dataCell) != 1 {
		return contract{}, fmt.Errorf("BOC must have only one root")
	}
	return contract{
		Address: accountID,
		Code:    codeCell[0],
		Data:    dataCell[0],
	}, nil
}

// func getJettonWalletAddress(
// 	ctx context.Context,
// 	owner *address.Address,
// 	jettonMaster *address.Address,
// 	client *ton.APIClient,
// ) (*address.Address, error) {
// 	client.RunGetMethod(ctx)
// 	ownerAccountID, err := tongo.ParseAccountID(owner.String())
// 	if err != nil {
// 		return models.Address{}, err
// 	}
// 	slice, err := tongoTlb.TlbStructToVmCellSlice(ownerAccountID.ToMsgAddress())
// 	if err != nil {
// 		return models.Address{}, err
// 	}

// 	code, result, err := emulator.RunSmcMethod(context.Background(), jettonMaster, "get_wallet_address",
// 		tongoTlb.VmStack{slice})
// 	if err != nil {
// 		return models.Address{}, err
// 	}
// 	if code != 0 || len(result) != 1 || result[0].SumType != "VmStkSlice" {
// 		return models.Address{}, fmt.Errorf("tvm execution failed")
// 	}

// 	var msgAddress tongoTlb.MsgAddress
// 	err = result[0].VmStkSlice.UnmarshalToTlbStruct(&msgAddress)
// 	if err != nil {
// 		return models.Address{}, err
// 	}
// 	if msgAddress.SumType != "AddrStd" {
// 		return models.Address{}, fmt.Errorf("not std jetton wallet address")
// 	}
// 	if msgAddress.AddrStd.WorkchainId != models.DefaultWorkchain {
// 		return models.Address{}, fmt.Errorf("not default workchain for jetton wallet address")
// 	}
// 	return models.Address(msgAddress.AddrStd.Address), nil
// 	return nil, nil
// }

func getJettonWalletAddressByTVM(
	owner *address.Address,
	jettonMaster tongo.AccountID,
	emulator *tvm.Emulator,
) (models.Address, error) {
	ownerAccountID, err := tongo.ParseAccountID(owner.String())
	if err != nil {
		return models.Address{}, err
	}
	slice, err := tongoTlb.TlbStructToVmCellSlice(ownerAccountID.ToMsgAddress())
	if err != nil {
		return models.Address{}, err
	}

	code, result, err := emulator.RunSmcMethod(context.Background(), jettonMaster, "get_wallet_address",
		tongoTlb.VmStack{slice})
	if err != nil {
		return models.Address{}, err
	}
	if code != 0 || len(result) != 1 || result[0].SumType != "VmStkSlice" {
		return models.Address{}, fmt.Errorf("tvm execution failed")
	}

	var msgAddress tongoTlb.MsgAddress
	err = result[0].VmStkSlice.UnmarshalToTlbStruct(&msgAddress)
	if err != nil {
		return models.Address{}, err
	}
	if msgAddress.SumType != "AddrStd" {
		return models.Address{}, fmt.Errorf("not std jetton wallet address")
	}
	if msgAddress.AddrStd.WorkchainId != models.DefaultWorkchain {
		return models.Address{}, fmt.Errorf("not default workchain for jetton wallet address")
	}
	return models.Address(msgAddress.AddrStd.Address), nil
}

func newEmulator(code, data *boc.Cell) (*tvm.Emulator, error) {
	emulator, err := tvm.NewEmulator(code, data, config.Config.BlockchainConfig, tvm.WithBalance(1_000_000_000))
	if err != nil {
		return nil, err
	}
	// TODO: try tvm.WithLazyC7Optimization()
	err = emulator.SetVerbosityLevel(1)
	if err != nil {
		return nil, err
	}
	return emulator, nil
}

// GetJettonBalance
// Get method get_wallet_data() returns (int balance, slice owner, slice jetton, cell jetton_wallet_code)
// Returns jetton balance for custom block in basic units
func (c *connection) GetJettonBalance(ctx context.Context, address models.Address, blockID *ton.BlockIDExt) (*big.Int, error) {
	jettonWallet := address.ToTonutilsAddressStd(0)
	stack, err := c.RunGetMethod(ctx, blockID, jettonWallet, "get_wallet_data")
	if err != nil {
		if strings.Contains(err.Error(), "contract is not initialized") {
			return big.NewInt(0), nil
		}
		return nil, fmt.Errorf("get wallet data error: %v", err)
	}
	res := stack.AsTuple()
	if len(res) != 4 {
		return nil, fmt.Errorf("invalid stack size")
	}
	switch res[0].(type) {
	case *big.Int:
		return res[0].(*big.Int), nil
	default:
		return nil, fmt.Errorf("invalid balance type")
	}
}

// GetLastJettonBalance
// Returns jetton balance for last block in basic units
func (c *connection) GetLastJettonBalance(ctx context.Context, address *address.Address) (*big.Int, error) {
	masterID, err := c.client.CurrentMasterchainInfo(ctx)
	if err != nil {
		return nil, err
	}
	addr, err := models.AddressFromTonutilsAddress(address)
	if err != nil {
		return nil, err
	}
	return c.GetJettonBalance(ctx, addr, masterID)
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

// DeployTonWallet
// Deploys wallet contract and wait its activation
func (c *connection) DeployTonWallet(ctx context.Context, wallet *wallet.Wallet) error {
	balance, status, err := c.GetAccountCurrentState(ctx, wallet.Address())
	if err != nil {
		return err
	}
	if balance.Cmp(big.NewInt(0)) == 0 {
		return fmt.Errorf("empty balance")
	}
	if status != tlb.AccountStatusActive {
		err = wallet.TransferNoBounce(ctx, wallet.Address(), tlb.FromNanoTONU(0), "")
		if err != nil {
			return err
		}
	} else {
		return nil
	}
	return c.WaitStatus(ctx, wallet.Address(), tlb.AccountStatusActive)
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

func inShard(addr models.Address, shard byte) bool {
	return addr[0] == shard
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
