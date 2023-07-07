package core

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/gobicycle/bicycle/audit"
	"github.com/gobicycle/bicycle/config"
	"github.com/gobicycle/bicycle/db"
	"github.com/gobicycle/bicycle/models"
	"github.com/gofrs/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton"
	"github.com/xssnick/tonutils-go/tvm/cell"
)

type BlockScanner struct {
	repo         db.Repository
	addressBook  db.AddressBook
	wallets      Wallet
	blockchain   Blockchain
	notificators []models.Notificator
	blocksChan   chan *models.ShardBlockHeader
}

type transactions struct {
	Address      models.Address
	WalletType   models.WalletType
	Transactions []*tlb.Transaction
}

type jettonTransferNotificationMsg struct {
	Amount  models.Coins
	Sender  *address.Address
	Comment string
}

type JettonTransferMsg struct {
	Amount      models.Coins
	Destination *address.Address
	Comment     string
}

type HighLoadWalletExtMsgInfo struct {
	UUID     uuid.UUID
	TTL      time.Time
	Messages *cell.Dictionary
}

type incomeNotification struct {
	Deposit   string `json:"deposit_address"`
	Timestamp int64  `json:"time"`
	Amount    string `json:"amount"`
	Source    string `json:"source_address,omitempty"`
	Comment   string `json:"comment,omitempty"`
}

func NewBlockScanner(
	repo db.Repository,
	bcClient Blockchain,
	wallets Wallet,
	notificators []models.Notificator,
	blocksChan chan *models.ShardBlockHeader,
) *BlockScanner {
	t := &BlockScanner{
		repo:         repo,
		blockchain:   bcClient,
		wallets:      wallets,
		notificators: notificators,
		blocksChan:   blocksChan,
	}
	return t
}

func (s *BlockScanner) Start(ctx context.Context) {
	log.Printf("Block scanner started")
	for {
		select {
		case <-ctx.Done():
			return
		case block := <-s.blocksChan:
			if block == nil {
				continue
			}
			ctx, _ := context.WithTimeout(ctx, time.Second*15)
			err := s.processBlock(ctx, *block)

			if err != nil {
				log.Errorf("block processing error: %v", err)
			}
			log.Printf("Block %d scanned successfuly", block.SeqNo)
		}
	}
}

func (s *BlockScanner) Stop() {
}

func (s *BlockScanner) processBlock(ctx context.Context, block models.ShardBlockHeader) error {
	log.Infof("processing block: %v", block.SeqNo)
	txIDs, err := s.blockchain.GetTransactionIDsFromBlock(ctx, block.BlockIDExt)
	if err != nil {
		return err
	}
	filteredTXs, err := s.filterTXs(ctx, block.BlockIDExt, txIDs)
	if err != nil {
		return err
	}
	e, err := s.processTXs(ctx, filteredTXs, block)
	if err != nil {
		return err
	}
	err = s.pushNotifications(e)
	if err != nil {
		return err
	}
	return s.SaveParsedBlockData(ctx, e)
}

func (s *BlockScanner) SaveParsedBlockData(ctx context.Context, events models.BlockEvents) error {
	return s.repo.RunInTx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		for _, ei := range events.ExternalIncomes {
			err = s.repo.SaveExternalIncome(ctx, tx, ei)
			if err != nil {
				return err
			}
		}
		for _, ii := range events.InternalIncomes {
			err = s.repo.SaveInternalIncome(ctx, tx, ii)
			if err != nil {
				return err
			}
		}
		for _, sc := range events.SendingConfirmations {
			err = s.repo.ApplySendingConfirmations(ctx, tx, sc)
			if err != nil {
				return err
			}
		}
		for _, iw := range events.InternalWithdrawals {
			err = s.repo.UpdateInternalWithdrawal(ctx, tx, iw)
			if err != nil {
				return err
			}
		}
		for _, ew := range events.ExternalWithdrawals {
			err = s.repo.UpdateExternalWithdrawal(ctx, tx, ew)
			if err != nil {
				return err
			}
		}
		for _, wc := range events.WithdrawalConfirmations {
			err = s.repo.ApplyJettonWithdrawalConfirmation(ctx, tx, wc)
			if err != nil {
				return err
			}
		}
		return s.repo.SaveBlock(ctx, tx, events.Block)
	})
}

func (s *BlockScanner) pushNotifications(e models.BlockEvents) error {
	if len(s.notificators) == 0 {
		return nil
	}

	if config.Config.IsDepositSideCalculation {
		for _, ei := range e.ExternalIncomes {
			err := s.pushNotification(ei.To, ei.Amount, ei.Utime, ei.From, ei.FromWorkchain, ei.Comment)
			if err != nil {
				return err
			}
		}
	} else {
		for _, ii := range e.InternalIncomes {
			err := s.pushNotification(ii.From, ii.Amount, ii.Utime, nil, nil, "")
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *BlockScanner) pushNotification(
	addr models.Address,
	amount models.Coins,
	timestamp uint32,
	from []byte,
	fromWorkchain *int32,
	comment string,
) error {
	owner := s.addressBook.GetOwner(addr)
	if owner != nil {
		addr = *owner
	}
	notification := incomeNotification{
		Deposit:   addr.ToUserFormat(),
		Amount:    amount.String(),
		Timestamp: int64(timestamp),
		Comment:   comment,
	}
	if len(from) == 32 && fromWorkchain != nil {
		// supports only std address
		src := address.NewAddress(0, byte(*fromWorkchain), from)
		src.SetTestnetOnly(config.Config.Testnet)
		notification.Source = src.String()
	}

	for _, n := range s.notificators {
		err := n.Publish(notification)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *BlockScanner) filterTXs(
	ctx context.Context,
	blockID *ton.BlockIDExt,
	ids []ton.TransactionShortInfo,
) (
	[]transactions, error,
) {
	txMap := make(map[models.Address][]*tlb.Transaction)
	for _, id := range ids {
		a, err := models.AddressFromBytes(id.Account) // must be int256 for lite api
		if err != nil {
			return nil, err
		}
		_, ok := s.addressBook.GetWalletType(a)
		if ok {
			tx, err := s.blockchain.GetTransactionFromBlock(ctx, blockID, id)
			if err != nil {
				return nil, err
			}
			txMap[a] = append(txMap[a], tx)
		}
	}
	var res []transactions
	for a, txs := range txMap {
		wType, _ := s.addressBook.GetWalletType(a)
		res = append(res, transactions{a, wType, txs})
	}
	return res, nil
}

func isTxSuccessful(tx *tlb.Transaction) bool {
	switch v := tx.Description.Description.(type) {
	case tlb.TransactionDescriptionOrdinary:
		fmt.Println(v)
		// if v.BouncePhase.Phase {
		// 	return false
		// }
		// cph := v.ComputePhase.Phase
		// cph.Phase
		// if cph.SumType == "TrPhaseComputeSkipped" && cph.TrPhaseComputeSkipped.Reason != ComputeSkipReasonNoState {
		// 	return false
		// }
		// if cph.SumType == "TrPhaseComputeVm" && (!cph.TrPhaseComputeVm.Success || (cph.TrPhaseComputeVm.Vm.ExitCode != 0 && cph.TrPhaseComputeVm.Vm.ExitCode != 1)) {
		// 	return false
		// }
		// if v.ActionPhase && !v.Action.Value.Value.Success {
		// 	return false
		// }
		return true
	case tlb.TransactionDescriptionTickTock:
		// cph := v.ComputePhase
		// if cph.SumType == "TrPhaseComputeSkipped" && cph.TrPhaseComputeSkipped.Reason != ComputeSkipReasonNoState {
		// 	return false
		// }
		// if cph.SumType == "TrPhaseComputeVm" && (!cph.TrPhaseComputeVm.Success || (cph.TrPhaseComputeVm.Vm.ExitCode != 0 && cph.TrPhaseComputeVm.Vm.ExitCode != 1)) {
		// 	return false
		// }
		// if t.Action.Exists && !t.Action.Value.Value.Success {
		// 	return false
		// }
		return true
	default:
		return true //todo: add logic for other types
	}
}

func (s *BlockScanner) processTXs(
	ctx context.Context,
	txs []transactions,
	block models.ShardBlockHeader,
) (
	models.BlockEvents, error,
) {
	blockEvents := models.BlockEvents{Block: block}
	for _, t := range txs {
		switch t.WalletType {
		// TODO: check order of Lt for different accounts (it is important for intermediate tx Lt)
		case models.TonHotWallet:
			hotWalletEvents, err := s.processTonHotWalletTXs(t)
			if err != nil {
				return models.BlockEvents{}, err
			}
			blockEvents.Append(hotWalletEvents)
		case models.TonDepositWallet:
			tonDepositEvents, err := s.processTonDepositWalletTXs(t)
			if err != nil {
				return models.BlockEvents{}, err
			}
			blockEvents.Append(tonDepositEvents)
		case models.JettonDepositWallet:
			jettonDepositEvents, err := s.processJettonDepositWalletTXs(ctx, t, block.BlockIDExt, block.Parent)
			if err != nil {
				return models.BlockEvents{}, err
			}
			blockEvents.Append(jettonDepositEvents)
		}
	}
	return blockEvents, nil
}

func (s *BlockScanner) processTonHotWalletTXs(txs transactions) (models.Events, error) {
	var events models.Events

	for _, tx := range txs.Transactions {

		if tx.IO.In == nil { // impossible for standard highload TON wallet
			audit.LogTX(audit.Error, string(models.TonHotWallet), tx.Hash, "transaction without in message")
			return models.Events{}, fmt.Errorf("anomalous behavior of the TON hot wallet")
		}

		switch tx.IO.In.MsgType {
		case tlb.MsgTypeExternalIn:
			e, err := s.processTonHotWalletExternalInMsg(tx)
			if err != nil {
				return models.Events{}, err
			}
			events.Append(e)
		case tlb.MsgTypeInternal:
			e, err := s.processTonHotWalletInternalInMsg(tx)
			if err != nil {
				return models.Events{}, err
			}
			events.Append(e)
		default:
			audit.LogTX(audit.Error, string(models.TonHotWallet), tx.Hash,
				"transaction in message must be internal or external in")
			return models.Events{}, fmt.Errorf("anomalous behavior of the TON hot wallet")
		}
	}
	return events, nil
}

func (s *BlockScanner) processTonDepositWalletTXs(txs transactions) (models.Events, error) {
	var events models.Events

	for _, tx := range txs.Transactions {

		if tx.IO.In == nil { // impossible for standard TON V3 wallet
			audit.LogTX(audit.Error, string(models.TonDepositWallet), tx.Hash, "transaction without in message")
			return models.Events{}, fmt.Errorf("anomalous behavior of the deposit TON wallet")
		}

		if !isTxSuccessful(tx) {
			audit.LogTX(audit.Info, string(models.TonDepositWallet), tx.Hash, "failed transaction")
			continue
		}

		switch tx.IO.In.MsgType {
		case tlb.MsgTypeExternalIn:
			// internal withdrawal. spam or invalid external cannot invoke tx
			// theoretically will be up to 4 out messages for TON V3 wallet
			// external_in msg without out_msg very rare or impossible
			// it is not critical for internal transfers (double spending not dangerous).
			e, err := s.processTonDepositWalletExternalInMsg(tx)
			if err != nil {
				return models.Events{}, err
			}
			events.Append(e)
		case tlb.MsgTypeInternal:
			// success external income (without bounce)
			// internal message can not invoke out message for TON wallet V3 except of bounce
			// bounced filtered at !success step
			if len(tx.IO.Out.List.All()) != 0 {
				audit.LogTX(audit.Error, string(models.TonDepositWallet), tx.Hash, "outgoing message from internal incoming")
				return models.Events{}, fmt.Errorf("anomalous behavior of the deposit TON wallet")
			}
			e, err := s.processTonDepositWalletInternalInMsg(tx)
			if err != nil {
				return models.Events{}, err
			}
			events.Append(e)
		default:
			audit.LogTX(audit.Error, string(models.TonDepositWallet), tx.Hash,
				"transaction in message must be internal or external in")
			return models.Events{}, fmt.Errorf("anomalous behavior of the deposit TON wallet")
		}
	}
	return events, nil
}

func (s *BlockScanner) processJettonDepositWalletTXs(
	ctx context.Context,
	txs transactions,
	blockID, prevBlockID *ton.BlockIDExt,
) (models.Events, error) {
	var (
		unknownTransactions []*tlb.Transaction
		events              models.Events
	)

	knownIncomeAmount := big.NewInt(0)
	totalWithdrawalsAmount := big.NewInt(0)

	for _, tx := range txs.Transactions {
		e, knownAmount, outUnknownFound, err := s.processJettonDepositOutMsgs(tx)
		if err != nil {
			return models.Events{}, err
		}
		knownIncomeAmount.Add(knownIncomeAmount, knownAmount)
		events.Append(e)

		e, totalAmount, inUnknownFound, err := s.processJettonDepositInMsg(tx)
		if err != nil {
			return models.Events{}, err
		}
		totalWithdrawalsAmount.Add(totalWithdrawalsAmount, totalAmount)
		events.Append(e)

		if outUnknownFound || inUnknownFound { // if found some unknown messages that potentially can change Jetton balance
			unknownTransactions = append(unknownTransactions, tx)
		}
	}

	unknownIncomeAmount, err := s.calculateJettonAmounts(ctx, txs.Address, prevBlockID, blockID, knownIncomeAmount, totalWithdrawalsAmount)
	if err != nil {
		return models.Events{}, err
	}

	if unknownIncomeAmount.Cmp(big.NewInt(0)) == 1 { // unknownIncomeAmount > 0
		unknownIncomes, err := convertUnknownJettonTxs(unknownTransactions, txs.Address, unknownIncomeAmount)
		if err != nil {
			return models.Events{}, err
		}
		events.ExternalIncomes = append(events.ExternalIncomes, unknownIncomes...)
	}

	return events, nil
}

func (s *BlockScanner) calculateJettonAmounts(
	ctx context.Context,
	address models.Address,
	prevBlockID, blockID *ton.BlockIDExt,
	knownIncomeAmount, totalWithdrawalsAmount *big.Int,
) (
	unknownIncomeAmount *big.Int,
	err error,
) {
	prevBalance, err := s.wallets.GetJettonBalance(ctx, address.ToTonutilsAddressStd(0), prevBlockID)
	if err != nil {
		return nil, err
	}
	currentBalance, err := s.wallets.GetJettonBalance(ctx, address.ToTonutilsAddressStd(0), blockID)
	if err != nil {
		return nil, err
	}
	diff := big.NewInt(0)
	diff.Sub(currentBalance, prevBalance) // diff = currentBalance - prevBalance

	totalIncomeAmount := big.NewInt(0)
	totalIncomeAmount.Add(diff, totalWithdrawalsAmount) // totalIncomeAmount = diff + totalWithdrawalsAmount

	unknownIncomeAmount = big.NewInt(0)
	unknownIncomeAmount.Sub(totalIncomeAmount, knownIncomeAmount) // unknownIncomeAmount = totalIncomeAmount - knownIncomeAmount

	return unknownIncomeAmount, nil
}

func convertUnknownJettonTxs(txs []*tlb.Transaction, addr models.Address, amount *big.Int) ([]models.ExternalIncome, error) {
	var incomes []models.ExternalIncome
	for _, tx := range txs { // unknown sender (jetton wallet owner). do not save message sender as from.
		incomes = append(incomes, models.ExternalIncome{
			Utime:  tx.Now,
			Lt:     tx.LT,
			To:     addr,
			Amount: models.ZeroCoins(),
		})

	}
	if len(txs) > 0 {
		incomes = append(incomes, models.ExternalIncome{
			Utime:  txs[0].Now, // mark unknown tx with first tx time
			Lt:     txs[0].LT,
			To:     addr,
			Amount: models.NewCoins(amount),
		})
	}
	return incomes, nil
}

func decodeJettonTransferNotification(msg *tlb.InternalMessage) (jettonTransferNotificationMsg, error) {
	if msg == nil {
		return jettonTransferNotificationMsg{}, fmt.Errorf("nil msg")
	}
	payload := msg.Payload()
	if payload == nil {
		return jettonTransferNotificationMsg{}, fmt.Errorf("empty payload")
	}
	var notification struct {
		_              tlb.Magic        `tlb:"#7362d09c"`
		QueryID        uint64           `tlb:"## 64"`
		Amount         tlb.Coins        `tlb:"."`
		Sender         *address.Address `tlb:"addr"`
		ForwardPayload *cell.Cell       `tlb:"either . ^"`
	}
	err := tlb.LoadFromCell(&notification, payload.BeginParse())
	if err != nil {
		return jettonTransferNotificationMsg{}, err
	}
	return jettonTransferNotificationMsg{
		Sender:  notification.Sender,
		Amount:  models.NewCoins(notification.Amount.NanoTON()),
		Comment: LoadComment(notification.ForwardPayload),
	}, nil
}

func DecodeJettonTransfer(msg *tlb.InternalMessage) (JettonTransferMsg, error) {
	if msg == nil {
		return JettonTransferMsg{}, fmt.Errorf("nil msg")
	}
	payload := msg.Payload()
	if payload == nil {
		return JettonTransferMsg{}, fmt.Errorf("empty payload")
	}
	var transfer struct {
		_                   tlb.Magic        `tlb:"#0f8a7ea5"`
		QueryID             uint64           `tlb:"## 64"`
		Amount              tlb.Coins        `tlb:"."`
		Destination         *address.Address `tlb:"addr"`
		ResponseDestination *address.Address `tlb:"addr"`
		CustomPayload       *cell.Cell       `tlb:"maybe ^"`
		ForwardTonAmount    tlb.Coins        `tlb:"."`
		ForwardPayload      *cell.Cell       `tlb:"either . ^"`
	}
	err := tlb.LoadFromCell(&transfer, payload.BeginParse())
	if err != nil {
		return JettonTransferMsg{}, err
	}
	return JettonTransferMsg{
		models.NewCoins(transfer.Amount.NanoTON()),
		transfer.Destination,
		LoadComment(transfer.ForwardPayload),
	}, nil
}

func decodeJettonExcesses(msg *tlb.InternalMessage) (uint64, error) {
	if msg == nil {
		return 0, fmt.Errorf("nil msg")
	}
	payload := msg.Payload()
	if payload == nil {
		return 0, fmt.Errorf("empty payload")
	}
	var excesses struct {
		_       tlb.Magic `tlb:"#d53276db"`
		QueryID uint64    `tlb:"## 64"`
	}
	err := tlb.LoadFromCell(&excesses, payload.BeginParse())
	if err != nil {
		return 0, err
	}
	return excesses.QueryID, nil
}

func parseExternalMessage(msg *tlb.ExternalMessage) (
	u uuid.UUID,
	addrMap map[models.Address]struct{},
	isValidWithdrawal bool,
	err error,
) {
	if msg == nil {
		return uuid.UUID{}, nil, false, fmt.Errorf("nil msg")
	}
	addrMap = make(map[models.Address]struct{})

	info, err := getHighLoadWalletExtMsgInfo(msg)
	if err != nil {
		return uuid.UUID{}, nil, false, err
	}

	for _, m := range info.Messages.All() {
		var (
			intMsg tlb.InternalMessage
			addr   models.Address
		)
		msgCell, err := m.Value.BeginParse().LoadRef()
		if err != nil {
			return uuid.UUID{}, nil, false, err
		}
		err = tlb.LoadFromCell(&intMsg, msgCell)
		if err != nil {
			return uuid.UUID{}, nil, false, err
		}
		jettonTransfer, err := DecodeJettonTransfer(&intMsg)
		if err == nil {
			addr, err = models.AddressFromTonutilsAddress(jettonTransfer.Destination)
			if err != nil {
				return uuid.UUID{}, nil, false, nil
			}
		} else {
			addr, err = models.AddressFromTonutilsAddress(intMsg.DstAddr)
			if err != nil {
				return uuid.UUID{}, nil, false, nil
			}
		}
		_, ok := addrMap[addr]
		if ok { // not unique addresses
			return uuid.UUID{}, nil, false, nil
		}
		addrMap[addr] = struct{}{}
	}
	return info.UUID, addrMap, true, nil
}

func (s *BlockScanner) failedWithdrawals(inMap map[models.Address]struct{}, outMap map[models.Address]struct{}, u uuid.UUID) []models.ExternalWithdrawal {
	var w []models.ExternalWithdrawal
	for i := range inMap {
		_, dstOk := s.addressBook.GetWalletType(i)
		if _, ok := outMap[i]; !ok && !dstOk { // !dstOk - not failed internal fee payments
			w = append(w, models.ExternalWithdrawal{ExtMsgUuid: u, To: i, IsFailed: true})
		}
	}
	return w
}

func getHighLoadWalletExtMsgInfo(extMsg *tlb.ExternalMessage) (HighLoadWalletExtMsgInfo, error) {
	body := extMsg.Payload()
	if body == nil {
		return HighLoadWalletExtMsgInfo{}, fmt.Errorf("nil body for external message")
	}
	hash := body.Hash() // must be 32 bytes
	u, err := uuid.FromBytes(hash[:16])
	if err != nil {
		return HighLoadWalletExtMsgInfo{}, err
	}

	var data struct {
		Sign        []byte           `tlb:"bits 512"`
		SubwalletID uint32           `tlb:"## 32"`
		BoundedID   uint64           `tlb:"## 64"`
		Messages    *cell.Dictionary `tlb:"dict 16"`
	}
	err = tlb.LoadFromCell(&data, body.BeginParse())
	if err != nil {
		return HighLoadWalletExtMsgInfo{}, err
	}
	ttl := time.Unix(int64((data.BoundedID>>32)&0x00_00_00_00_FF_FF_FF_FF), 0)
	return HighLoadWalletExtMsgInfo{UUID: u, TTL: ttl, Messages: data.Messages}, nil
}

func (s *BlockScanner) processTonHotWalletExternalInMsg(tx *tlb.Transaction) (models.Events, error) {
	var events models.Events
	inMsg := tx.IO.In.AsExternalIn()
	// withdrawal messages must be only with different recipients for identification
	u, addrMapIn, isValid, err := parseExternalMessage(inMsg)
	if err != nil {
		return models.Events{}, err
	}
	if !isValid {
		audit.LogTX(audit.Error, string(models.TonHotWallet), tx.Hash, "not valid external message")
		return models.Events{}, fmt.Errorf("not valid message")
	}

	addrMapOut := make(map[models.Address]struct{})
	messages, err := tx.IO.Out.ToSlice()
	if err != nil {
		return models.Events{}, err
	}
	for _, m := range messages {
		if m.MsgType != tlb.MsgTypeInternal {
			audit.LogTX(audit.Error, string(models.TonHotWallet), tx.Hash, "not internal out message for transaction")
			return models.Events{}, fmt.Errorf("anomalous behavior of the TON hot wallet")
		}
		msg := m.AsInternal()

		addr, err := models.AddressFromTonutilsAddress(msg.DstAddr)
		if err != nil {
			return models.Events{}, fmt.Errorf("invalid address in withdrawal message")
		}
		dstType, dstOk := s.addressBook.GetWalletTypeByTonutilsAddress(msg.DstAddr)

		if dstOk && dstType == models.JettonHotWallet { // Jetton external withdrawal
			jettonTransfer, err := DecodeJettonTransfer(msg)
			if err != nil {
				audit.LogTX(audit.Error, string(models.TonHotWallet), tx.Hash, "invalid jetton transfer message to hot jetton wallet")
				return models.Events{}, fmt.Errorf("invalid jetton transfer message to hot jetton wallet")
			}
			a, err := models.AddressFromTonutilsAddress(jettonTransfer.Destination)
			if err != nil {
				return models.Events{}, fmt.Errorf("invalid address in withdrawal message")
			}
			events.ExternalWithdrawals = append(events.ExternalWithdrawals, models.ExternalWithdrawal{
				ExtMsgUuid: u,
				Utime:      msg.CreatedAt,
				Lt:         msg.CreatedLT,
				To:         a,
				Amount:     jettonTransfer.Amount,
				Comment:    jettonTransfer.Comment,
				IsFailed:   false,
			})
			addrMapOut[a] = struct{}{}
			continue
		}

		if dstOk && dstType == models.JettonOwner { // Jetton internal withdrawal or service withdrawal
			e, err := s.processTonHotWalletProxyMsg(msg)
			if err != nil {
				return models.Events{}, fmt.Errorf("jetton withdrawal error: %v", err)
			}
			events.Append(e)
			addrMapOut[addr] = struct{}{}
			continue
		}

		if !dstOk { // hot_wallet -> unknown_address. to filter internal fee payments
			events.ExternalWithdrawals = append(events.ExternalWithdrawals, models.ExternalWithdrawal{
				ExtMsgUuid: u,
				Utime:      msg.CreatedAt,
				Lt:         msg.CreatedLT,
				To:         addr,
				Amount:     models.NewCoins(msg.Amount.NanoTON()),
				Comment:    msg.Comment(),
				IsFailed:   false,
			})
		}
		addrMapOut[addr] = struct{}{}
	}
	events.ExternalWithdrawals = append(events.ExternalWithdrawals, s.failedWithdrawals(addrMapIn, addrMapOut, u)...)
	return events, nil
}

func (s *BlockScanner) processTonHotWalletProxyMsg(msg *tlb.InternalMessage) (models.Events, error) {
	var events models.Events
	body := msg.Payload()
	internalPayload, err := body.BeginParse().LoadRef()
	if err != nil {
		return models.Events{}, fmt.Errorf("no internal payload to proxy contract: %v", err)
	}
	var intMsg tlb.InternalMessage
	err = tlb.LoadFromCell(&intMsg, internalPayload)
	if err != nil {
		return models.Events{}, fmt.Errorf("can not decode payload message for proxy contract: %v", err)
	}

	destType, ok := s.addressBook.GetWalletTypeByTonutilsAddress(intMsg.DstAddr)
	// ok && destType == models.TonHotWallet - service TON withdrawal
	// !ok - service Jetton withdrawal
	if ok && destType == models.JettonDepositWallet { // Jetton internal withdrawal
		jettonTransfer, err := DecodeJettonTransfer(&intMsg)
		if err != nil {
			return models.Events{}, fmt.Errorf("invalid jetton transfer message to deposit jetton wallet: %v", err)
		}
		a, err := models.AddressFromTonutilsAddress(jettonTransfer.Destination)
		if err != nil {
			return models.Events{}, fmt.Errorf("invalid address in withdrawal message")
		}
		events.SendingConfirmations = append(events.SendingConfirmations, models.SendingConfirmation{
			Lt:   msg.CreatedLT,
			From: a,
			Memo: jettonTransfer.Comment,
		})
	}
	return events, nil
}

func (s *BlockScanner) processTonHotWalletInternalInMsg(tx *tlb.Transaction) (models.Events, error) {
	var events models.Events
	inMsg := tx.IO.In.AsInternal()
	srcAddr, err := models.AddressFromTonutilsAddress(inMsg.SrcAddr)
	if err != nil {
		return models.Events{}, err
	}
	dstAddr, err := models.AddressFromTonutilsAddress(inMsg.DstAddr)
	if err != nil {
		return models.Events{}, err
	}

	srcType, srcOk := s.addressBook.GetWalletType(srcAddr)
	if !srcOk { // unknown_address -> hot_wallet. to check for external jetton transfer confirmation via excess message
		queryID, err := decodeJettonExcesses(inMsg)
		if err == nil {
			events.WithdrawalConfirmations = append(events.WithdrawalConfirmations,
				models.JettonWithdrawalConfirmation{queryID})
		}
	} else if srcOk && srcType == models.TonDepositWallet { // income TONs from deposit
		income := models.InternalIncome{
			Lt:       inMsg.CreatedLT,
			Utime:    inMsg.CreatedAt,
			From:     srcAddr,
			To:       dstAddr,
			Amount:   models.NewCoins(inMsg.Amount.NanoTON()),
			Memo:     inMsg.Comment(),
			IsFailed: false,
		}
		// TODO: check for partially failed message
		if isTxSuccessful(tx) {
			events.InternalIncomes = append(events.InternalIncomes, income)
		} else {
			income.IsFailed = true
			events.InternalIncomes = append(events.InternalIncomes, income)
		}
	} else if srcOk && srcType == models.JettonHotWallet { // income Jettons notification from Jetton hot wallet
		income, err := decodeJettonTransferNotification(inMsg)
		if err == nil {
			sender, err := models.AddressFromTonutilsAddress(income.Sender)
			if err != nil {
				return models.Events{}, err
			}
			fromType, fromOk := s.addressBook.GetWalletType(sender)
			if !fromOk || fromType != models.JettonOwner { // skip transfers not from deposit wallets
				return events, nil
			}
			events.InternalIncomes = append(events.InternalIncomes, models.InternalIncome{
				Lt:       inMsg.CreatedLT,
				Utime:    inMsg.CreatedAt,
				From:     sender, // sender == owner of jetton deposit wallet
				To:       srcAddr,
				Amount:   income.Amount,
				Memo:     income.Comment,
				IsFailed: false,
			})
		}
	}
	return events, nil
}

func (s *BlockScanner) processTonDepositWalletExternalInMsg(tx *tlb.Transaction) (models.Events, error) {
	var events models.Events

	dstAddr, err := models.AddressFromTonutilsAddress(tx.IO.In.AsExternalIn().DstAddr)
	if err != nil {
		return models.Events{}, err
	}

	messages, err := tx.IO.Out.ToSlice()
	if err != nil {
		return models.Events{}, err
	}

	for _, o := range messages {
		if o.MsgType != tlb.MsgTypeInternal {
			audit.LogTX(audit.Error, string(models.TonDepositWallet), tx.Hash, "not internal out message for transaction")
			return models.Events{}, fmt.Errorf("anomalous behavior of the deposit TON wallet")
		}
		msg := o.AsInternal()
		t, srcOk := s.addressBook.GetWalletTypeByTonutilsAddress(msg.DstAddr)
		if !srcOk || t != models.TonHotWallet {
			audit.LogTX(audit.Warning, string(models.TonDepositWallet), tx.Hash, fmt.Sprintf("TONs withdrawal from %v to %v (not to hot wallet)",
				msg.SrcAddr.String(), msg.DstAddr.String()))
			continue
		}
		events.SendingConfirmations = append(events.SendingConfirmations, models.SendingConfirmation{
			Lt:   msg.CreatedLT,
			From: dstAddr,
			Memo: msg.Comment(),
		})
		events.InternalWithdrawals = append(events.InternalWithdrawals, models.InternalWithdrawal{
			Utime:    msg.CreatedAt,
			Lt:       msg.CreatedLT,
			From:     dstAddr,
			Amount:   models.NewCoins(msg.Amount.NanoTON()),
			Memo:     msg.Comment(),
			IsFailed: false,
		})
	}
	return events, nil
}

func (s *BlockScanner) processTonDepositWalletInternalInMsg(tx *tlb.Transaction) (models.Events, error) {
	var (
		events        models.Events
		from          models.Address
		err           error
		fromWorkchain *int32
	)

	inMsg := tx.IO.In.AsInternal()
	dstAddr, err := models.AddressFromTonutilsAddress(inMsg.DstAddr)
	if err != nil {
		return models.Events{}, err
	}

	isKnownSender := false
	// support only std address
	if inMsg.SrcAddr.Type() == address.StdAddress {
		from, err = models.AddressFromTonutilsAddress(inMsg.SrcAddr)
		if err != nil {
			return models.Events{}, err
		}
		_, isKnownSender = s.addressBook.GetWalletType(from)
		wc := inMsg.SrcAddr.Workchain()
		fromWorkchain = &wc
	}
	if !isKnownSender { // income TONs from payer. exclude internal (hot->deposit, deposit->deposit) transfers.
		events.ExternalIncomes = append(events.ExternalIncomes, models.ExternalIncome{
			Lt:            inMsg.CreatedLT,
			Utime:         inMsg.CreatedAt,
			From:          from.ToBytes(),
			FromWorkchain: fromWorkchain,
			To:            dstAddr,
			Amount:        models.NewCoins(inMsg.Amount.NanoTON()),
			Comment:       inMsg.Comment(),
		})
	}
	return events, nil
}

func (s *BlockScanner) processJettonDepositOutMsgs(tx *tlb.Transaction) (models.Events, *big.Int, bool, error) {
	var events models.Events
	knownIncomeAmount := big.NewInt(0)
	unknownMsgFound := false

	messages, err := tx.IO.Out.ToSlice()
	if err != nil {
		return models.Events{}, nil, false, err
	}

	for _, m := range messages { // checks for JettonTransferNotification

		if m.MsgType != tlb.MsgTypeInternal {
			audit.LogTX(audit.Info, string(models.JettonDepositWallet), tx.Hash, "sends external out message")
			unknownMsgFound = true
			continue
		} // skip external_out msg

		outMsg := m.AsInternal()
		srcAddr, err := models.AddressFromTonutilsAddress(outMsg.SrcAddr)
		if err != nil {
			return models.Events{}, nil, false, err
		}

		notify, err := decodeJettonTransferNotification(outMsg)
		if err != nil {
			unknownMsgFound = true
			continue
		}

		// need not check success. impossible for failed txs.
		_, senderOk := s.addressBook.GetWalletTypeByTonutilsAddress(notify.Sender)
		if senderOk {
			// TODO: check balance calculation for unknown transactions for service transfers
			audit.LogTX(audit.Info, string(models.JettonDepositWallet), tx.Hash, "service Jetton transfer")
			// not set unknownMsgFound = true to prevent service transfers interpretation as unknown
			continue
		} // some kind of internal transfer

		dstAddr, err := models.AddressFromTonutilsAddress(outMsg.DstAddr)
		if err != nil {
			return models.Events{}, nil, false, err
		}
		owner := s.addressBook.GetOwner(srcAddr)
		if owner == nil {
			return models.Events{}, nil, false, fmt.Errorf("no owner for Jetton deposit in addressbook")
		}
		if dstAddr != *owner {
			audit.LogTX(audit.Info, string(models.JettonDepositWallet), tx.Hash,
				"sends transfer notification message not to owner")
			// interpret it as an unknown message
			unknownMsgFound = true
			continue
		}

		var (
			from          []byte
			fromWorkchain *int32
		)
		if notify.Sender != nil &&
			(notify.Sender.Type() == address.StdAddress || notify.Sender.Type() == address.VarAddress) {
			from = notify.Sender.Data()
			wc := notify.Sender.Workchain()
			fromWorkchain = &wc
		}
		events.ExternalIncomes = append(events.ExternalIncomes, models.ExternalIncome{
			Utime:         outMsg.CreatedAt,
			Lt:            outMsg.CreatedLT,
			From:          from,
			FromWorkchain: fromWorkchain,
			To:            srcAddr,
			Amount:        notify.Amount,
			Comment:       notify.Comment,
		})
		knownIncomeAmount.Add(knownIncomeAmount, notify.Amount.BigInt())
	}
	return events, knownIncomeAmount, unknownMsgFound, nil
}

func (s *BlockScanner) processJettonDepositInMsg(tx *tlb.Transaction) (models.Events, *big.Int, bool, error) {
	var events models.Events
	unknownMsgFound := false
	totalWithdrawalsAmount := big.NewInt(0)

	if tx.IO.In == nil { // skip not decodable in_msg
		audit.LogTX(audit.Info, string(models.JettonDepositWallet), tx.Hash, "transaction without in message")
		// interpret it as an unknown message
		return events, totalWithdrawalsAmount, true, nil
	}

	if tx.IO.In.MsgType != tlb.MsgTypeInternal { // skip not decodable in_msg
		audit.LogTX(audit.Info, string(models.JettonDepositWallet), tx.Hash, "not internal in message")
		// interpret it as an unknown message
		return events, totalWithdrawalsAmount, true, nil
	}

	inMsg := tx.IO.In.AsInternal()
	dstAddr, err := models.AddressFromTonutilsAddress(inMsg.DstAddr)
	if err != nil {
		return models.Events{}, nil, false, err
	}

	transfer, err := DecodeJettonTransfer(inMsg)
	if err != nil {
		unknownMsgFound = true
		return events, totalWithdrawalsAmount, unknownMsgFound, nil
	}

	if !isTxSuccessful(tx) { // failed withdrawal from deposit jetton wallet
		events.InternalWithdrawals = append(events.InternalWithdrawals, models.InternalWithdrawal{
			Utime:    inMsg.CreatedAt,
			Lt:       inMsg.CreatedLT,
			From:     dstAddr,
			Amount:   transfer.Amount,
			Memo:     transfer.Comment,
			IsFailed: true,
		})
		return events, totalWithdrawalsAmount, unknownMsgFound, nil
	}

	// success withdrawal from deposit jetton wallet
	if len(tx.IO.Out.List.All()) < 1 {
		audit.LogTX(audit.Error, string(models.JettonDepositWallet), tx.Hash, "success Jettons transfer TX without out message")
		return models.Events{}, nil, true, fmt.Errorf("anomalous behavior of the deposit Jetton wallet")
	}
	totalWithdrawalsAmount.Add(totalWithdrawalsAmount, transfer.Amount.BigInt())
	destType, destOk := s.addressBook.GetWalletTypeByTonutilsAddress(transfer.Destination)
	if !destOk || destType != models.TonHotWallet {
		audit.LogTX(audit.Warning, string(models.JettonDepositWallet), tx.Hash,
			fmt.Sprintf("Jettons withdrawal from %v to %v (not to hot wallet)",
				inMsg.DstAddr.String(), transfer.Destination.String()))
		// TODO: check balance calculation for unknown transactions for service transfers
		// not set unknownMsgFound = true to prevent service transfers interpretation as unknown
		return models.Events{}, totalWithdrawalsAmount, false, nil
	}
	events.InternalWithdrawals = append(events.InternalWithdrawals, models.InternalWithdrawal{
		Utime:    inMsg.CreatedAt,
		Lt:       inMsg.CreatedLT,
		From:     dstAddr,
		Amount:   transfer.Amount,
		Memo:     transfer.Comment,
		IsFailed: false,
	})

	return events, totalWithdrawalsAmount, unknownMsgFound, nil
}
