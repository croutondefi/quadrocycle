package core

import (
	"context"
	"fmt"
	"math/big"
	"sync/atomic"
	"time"

	"github.com/gobicycle/bicycle/audit"
	"github.com/gobicycle/bicycle/config"
	"github.com/gobicycle/bicycle/db"
	"github.com/gobicycle/bicycle/models"
	"github.com/gofrs/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton/wallet"
)

type WithdrawalsProcessor struct {
	repo             db.Repository
	bc               Blockchain
	wallets          Wallets
	coldWallet       *address.Address
	gracefulShutdown atomic.Bool
}

type internalWithdrawal struct {
	Memo uuid.UUID
	Task models.InternalWithdrawalTask
}

type serviceWithdrawal struct {
	TonAmount models.Coins
	Filled    bool
	Task      models.ServiceWithdrawalTask
}

type withdrawals struct {
	Messages []*wallet.Message
	External []models.ExternalWithdrawalTask
	Internal []internalWithdrawal
	Service  []serviceWithdrawal
}

func NewWithdrawalsProcessor(
	repo db.Repository,
	bc Blockchain,
	wallets Wallets,
	coldWallet *address.Address,
) *WithdrawalsProcessor {
	w := &WithdrawalsProcessor{
		repo:       repo,
		bc:         bc,
		wallets:    wallets,
		coldWallet: coldWallet,
	}
	return w
}

func (p *WithdrawalsProcessor) Start() {
	go p.startWithdrawalsProcessor()
	go p.startInternalTonWithdrawalsProcessor()
	go p.startExpirationProcessor()
}

func (p *WithdrawalsProcessor) Stop() {
	p.gracefulShutdown.Store(true)
}

func (p *WithdrawalsProcessor) startWithdrawalsProcessor() {
	log.Infof("External withdrawal processor started")
	for {
		p.waitSync() // gracefulShutdown break must be after waitSync
		if p.gracefulShutdown.Load() {
			log.Infof("External withdrawal processor stopped")
			break
		}
		time.Sleep(config.ExternalWithdrawalPeriod)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*25) // must be < ExternalWithdrawalPeriod
		err := p.makeColdWalletWithdrawals(ctx)
		if err != nil {
			log.Fatalf("make withdrawals to cold wallet error: %v\n", err)
		}
		w, err := p.buildWithdrawalMessages(ctx)
		if err != nil {
			log.Fatalf("make withdrawal messages error: %v\n", err)
		}
		if len(w.Messages) == 0 {
			cancel()
			continue
		}
		extMsg, err := p.wallets.TonHotWallet.BuildMessageForMany(ctx, w.Messages)
		if err != nil {
			log.Fatalf("build hotwallet external msg error: %v\n", err)
		}
		info, err := getHighLoadWalletExtMsgInfo(extMsg)
		if err != nil {
			log.Fatalf("get external message uuid error: %v\n", err)
		}
		err = p.repo.CreateExternalWithdrawals(ctx, w.External, info.UUID, info.TTL)
		if err != nil {
			log.Fatalf("save external withdrawals error: %v\n", err)
		}
		for _, sw := range w.Service {
			err = p.repo.UpdateServiceWithdrawalRequest(ctx, sw.Task, sw.TonAmount, info.TTL, sw.Filled)
			if err != nil {
				log.Fatalf("update service withdrawal error: %v\n", err)
			}
		}
		for _, iw := range w.Internal {
			err = p.repo.SaveInternalWithdrawalTask(ctx, iw.Task, info.TTL, iw.Memo)
			if err != nil {
				log.Fatalf("save internal withdrawal error: %v\n", err)
			}
		}
		err = p.bc.SendExternalMessage(ctx, extMsg)
		if err != nil {
			log.Errorf("send external msg error: %v\n", err)
		}
		cancel()
	}
}

func (p *WithdrawalsProcessor) buildWithdrawalMessages(ctx context.Context) (withdrawals, error) {
	var (
		usedAddresses []models.Address
		res           withdrawals
	)

	balances, err := p.getHotWalletBalances(ctx)
	if err != nil {
		return withdrawals{}, fmt.Errorf("get hot wallet balance error: %s", err.Error())
	}

	serviceTasks, err := p.repo.GetServiceHotWithdrawalTasks(ctx, 250)
	if err != nil {
		return withdrawals{}, err
	}
	for _, t := range serviceTasks {
		if decreaseBalances(balances, models.TonSymbol, config.JettonTransferTonAmount.NanoTON()) {
			continue
		}
		msg, w, err := p.buildServiceWithdrawalMessage(ctx, t)
		if err != nil {
			return withdrawals{}, err
		}
		if len(msg) != 0 {
			// block scanner determines the uniqueness of the message in the batch by the dest address
			// the dest address will be the address of the proxy contract
			// TON deposit address is the dest addr for TON deposit filling message
			// so the address `t.From` is the dest address when checking the uniqueness
			usedAddresses = append(usedAddresses, t.From)
			res.Messages = append(res.Messages, msg...)
			res.Service = append(res.Service, w)
		} else {
			// save rejected service withdrawals
			err = p.repo.UpdateServiceWithdrawalRequest(ctx, w.Task, w.TonAmount, time.Now(), w.Filled)
			if err != nil {
				return withdrawals{}, err
			}
		}
	}

	// `internalTask.From` address is the address of deposit Jetton wallet
	// the dest address for uniqueness check is proxy contract address
	// so the proxy contract address must be deduplicated with usedAddresses in db query
	internalTasks, err := p.repo.GetJettonInternalWithdrawalTasks(ctx, usedAddresses, 250)
	if err != nil {
		return withdrawals{}, err
	}
	for _, t := range internalTasks {
		if len(res.Messages) > 250 {
			break
		}
		if decreaseBalances(balances, models.TonSymbol, config.JettonTransferTonAmount.NanoTON()) {
			continue
		}
		msg, memo, err := p.buildJettonInternalWithdrawalMessage(ctx, t)
		if err != nil {
			return withdrawals{}, err
		}
		if len(msg) != 0 {
			res.Messages = append(res.Messages, msg...)
			res.Internal = append(res.Internal, internalWithdrawal{
				Task: t,
				Memo: memo,
			})
		}
	}

	// not filter usedAddresses by DB and perform internal addresses checking and logging
	externalTasks, err := p.repo.GetExternalWithdrawalTasks(ctx, 250)
	if err != nil {
		return withdrawals{}, err
	}
	for _, w := range externalTasks {
		if len(res.Messages) > 250 {
			break
		}
		t, ok := p.repo.GetWalletType(w.Destination)
		if ok {
			audit.Log(audit.Warning, string(models.TonHotWallet), models.ExternalWithdrawalEvent,
				fmt.Sprintf("withdrawal task to internal %s address %s", t, w.Destination.ToUserFormat()))
			continue
		}
		if decreaseBalances(balances, w.Currency, w.Amount.BigInt()) {
			continue
		}
		msg := p.buildExternalWithdrawalMessage(w)
		res.Messages = append(res.Messages, msg)
		res.External = append(res.External, w)
	}
	return res, nil
}

func (p *WithdrawalsProcessor) getHotWalletBalances(ctx context.Context) (map[string]*big.Int, error) {
	res := make(map[string]*big.Int)
	balance, _, err := p.bc.GetAccountCurrentState(ctx, p.wallets.TonHotWallet.Address())
	if err != nil {
		return nil, err
	}
	res[models.TonSymbol] = balance
	for cur, w := range p.wallets.JettonHotWallets {
		balance, err := p.bc.GetLastJettonBalance(ctx, w.Address)
		if err != nil {
			return nil, err
		}
		res[cur] = balance
	}
	return res, nil
}

// decreaseBalances returns true if balance < amount
func decreaseBalances(balances map[string]*big.Int, currency string, amount *big.Int) bool {
	if currency == models.TonSymbol {
		if balances[models.TonSymbol].Cmp(amount) == -1 { // balance < amount
			return true
		}
		balances[models.TonSymbol].Sub(balances[models.TonSymbol], amount)
		return false
	}
	if balances[currency].Cmp(amount) == -1 || // balance < amount
		balances[models.TonSymbol].Cmp(config.JettonTransferTonAmount.NanoTON()) == -1 { // balance < JettonTransferTonAmount
		return true
	}
	balances[currency].Sub(balances[currency], amount)
	balances[models.TonSymbol].Sub(balances[models.TonSymbol], config.JettonTransferTonAmount.NanoTON())
	return false
}

func (p *WithdrawalsProcessor) buildJettonInternalWithdrawalMessage(
	ctx context.Context,
	task models.InternalWithdrawalTask,
) (
	[]*wallet.Message,
	uuid.UUID,
	error,
) {
	proxy, err := NewJettonProxy(task.SubwalletID, p.wallets.TonHotWallet.Address())
	if err != nil {
		return nil, uuid.UUID{}, err
	}
	jettonWalletAddress := task.From.ToTonutilsAddressStd(0)
	balance, err := p.bc.GetLastJettonBalance(ctx, jettonWalletAddress)
	if err != nil {
		return nil, uuid.UUID{}, err
	}
	if balance.Cmp(config.Config.Jettons[task.Currency].WithdrawalCutoff) == 1 { // balance > MinimalJettonWithdrawalAmount
		memo, err := uuid.NewV4()
		if err != nil {
			return nil, uuid.UUID{}, err
		}
		msg := BuildJettonProxyWithdrawalMessage(
			*proxy,
			jettonWalletAddress,
			p.wallets.TonHotWallet.Address(),
			config.JettonForwardAmount,
			balance,
			memo.String(),
		)
		return []*wallet.Message{msg}, memo, nil
	}
	return []*wallet.Message{}, uuid.UUID{}, nil
}

func (p *WithdrawalsProcessor) buildServiceWithdrawalMessage(
	ctx context.Context,
	task models.ServiceWithdrawalTask,
) (
	[]*wallet.Message,
	serviceWithdrawal,
	error,
) {
	t, ok := p.repo.GetWalletType(task.From)
	if !ok || !(t == models.JettonOwner || t == models.TonDepositWallet) {
		return nil, serviceWithdrawal{}, fmt.Errorf("invalid service withdrawal address")
	}
	if t == models.TonDepositWallet { // only fill TON deposit to send Jetton transfer message later
		return p.buildServiceFilling(ctx, task)
	}

	if task.JettonMaster == nil { // full TON withdrawal from Jetton proxy
		return p.buildServiceTonWithdrawal(ctx, task)
	}
	// Jetton withdrawal from Jetton wallet
	return p.buildServiceJettonWithdrawal(ctx, task)
}

func (p *WithdrawalsProcessor) buildServiceFilling(
	ctx context.Context,
	task models.ServiceWithdrawalTask,
) (
	[]*wallet.Message,
	serviceWithdrawal,
	error,
) {
	deposit := task.From.ToTonutilsAddressStd(0)

	jettonWallet, err := p.bc.GetJettonWalletAddress(
		ctx,
		deposit,
		task.JettonMaster.ToTonutilsAddressStd(0))
	if err != nil {
		return nil, serviceWithdrawal{}, err
	}
	jettonBalance, err := p.bc.GetLastJettonBalance(ctx, jettonWallet)
	if err != nil {
		return nil, serviceWithdrawal{}, err
	}

	if jettonBalance.Cmp(big.NewInt(0)) == 0 {
		audit.Log(audit.Warning, string(models.TonDepositWallet), models.ServiceWithdrawalEvent,
			fmt.Sprintf("zero balance of Jettons %s on TON deposit address %s",
				task.JettonMaster.ToTonutilsAddressStd(0).String(),
				models.TonutilsAddressToUserFormat(deposit)))
		return nil, serviceWithdrawal{
			TonAmount: models.ZeroCoins(),
			Task:      task,
		}, nil
	}
	msg := buildTonFillMessage(deposit, config.JettonTransferTonAmount, task.Memo)
	task.JettonAmount = models.NewCoins(jettonBalance)
	return []*wallet.Message{msg}, serviceWithdrawal{
		TonAmount: models.ZeroCoins(),
		Task:      task,
		Filled:    true,
	}, nil
}

func (p *WithdrawalsProcessor) buildServiceTonWithdrawal(
	ctx context.Context,
	task models.ServiceWithdrawalTask,
) (
	[]*wallet.Message,
	serviceWithdrawal,
	error,
) {
	proxy, err := NewJettonProxy(task.SubwalletID, p.wallets.TonHotWallet.Address())
	if err != nil {
		return nil, serviceWithdrawal{}, err
	}
	tonBalance, _, err := p.bc.GetAccountCurrentState(ctx, proxy.address)
	if err != nil {
		return nil, serviceWithdrawal{}, err
	}
	res := serviceWithdrawal{
		TonAmount: models.NewCoins(tonBalance),
		Task:      task,
	}
	if tonBalance.Cmp(big.NewInt(0)) == 0 {
		audit.Log(audit.Warning, string(models.JettonOwner), models.ServiceWithdrawalEvent,
			fmt.Sprintf("zero balance of TONs on proxy address %s", models.TonutilsAddressToUserFormat(proxy.address)))
		return nil, res, nil
	}
	msg := buildJettonProxyServiceTonWithdrawalMessage(*proxy, p.wallets.TonHotWallet.Address(), task.Memo)
	return []*wallet.Message{msg}, res, nil
}

func (p *WithdrawalsProcessor) buildServiceJettonWithdrawal(
	ctx context.Context,
	task models.ServiceWithdrawalTask,
) (
	[]*wallet.Message,
	serviceWithdrawal,
	error,
) {
	proxy, err := NewJettonProxy(task.SubwalletID, p.wallets.TonHotWallet.Address())
	if err != nil {
		return nil, serviceWithdrawal{}, err
	}
	jettonWallet, err := p.bc.GetJettonWalletAddress(ctx, proxy.address, task.JettonMaster.ToTonutilsAddressStd(0))
	if err != nil {
		return nil, serviceWithdrawal{}, err
	}
	t, ok := p.repo.GetWalletTypeByTonutilsAddress(jettonWallet)
	if ok {
		audit.Log(audit.Warning, string(models.JettonOwner), models.ServiceWithdrawalEvent,
			fmt.Sprintf("service withdrawal from known internal %s address %s rejected",
				t, models.TonutilsAddressToUserFormat(jettonWallet)))
		return nil, serviceWithdrawal{
			TonAmount: models.ZeroCoins(),
			Task:      task,
		}, nil
	}

	jettonBalance, err := p.bc.GetLastJettonBalance(ctx, jettonWallet)
	if err != nil {
		return nil, serviceWithdrawal{}, err
	}

	if jettonBalance.Cmp(big.NewInt(0)) == 0 {
		audit.Log(audit.Warning, string(models.JettonOwner), models.ServiceWithdrawalEvent,
			fmt.Sprintf("zero %s Jetton balance on proxy address %s",
				task.JettonMaster.ToTonutilsAddressStd(0).String(),
				models.TonutilsAddressToUserFormat(proxy.address)))
		return nil, serviceWithdrawal{
			TonAmount: models.ZeroCoins(),
			Task:      task,
		}, nil
	}
	task.JettonAmount = models.NewCoins(jettonBalance)
	res := serviceWithdrawal{
		TonAmount: models.ZeroCoins(),
		Task:      task,
	}

	msg := BuildJettonProxyWithdrawalMessage(
		*proxy,
		jettonWallet,
		p.wallets.TonHotWallet.Address(),
		tlb.FromNanoTONU(0), // zero forward amount to prevent notification sending and incorrect internal income invoking
		jettonBalance,
		task.Memo.String(),
	)
	return []*wallet.Message{msg}, res, nil
}

func (p *WithdrawalsProcessor) buildExternalWithdrawalMessage(wt models.ExternalWithdrawalTask) *wallet.Message {
	if wt.Currency == models.TonSymbol {
		return BuildTonWithdrawalMessage(wt)
	}
	jw := p.wallets.JettonHotWallets[wt.Currency]
	return BuildJettonWithdrawalMessage(wt, p.wallets.TonHotWallet, jw.Address)
}

func (p *WithdrawalsProcessor) startExpirationProcessor() {
	log.Infof("Expiration processor started")
	for {
		p.waitSync() // gracefulShutdown break must be after waitSync
		if p.gracefulShutdown.Load() {
			log.Infof("Expiration processor stopped")
			break
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*3) // must be < ExpirationProcessorPeriod
		err := p.repo.SetExpired(ctx)
		if err != nil {
			log.Errorf("set expired withdrawals error: %v", err)
		}
		cancel()
		time.Sleep(config.ExpirationProcessorPeriod)
	}
}

func (p *WithdrawalsProcessor) startInternalTonWithdrawalsProcessor() {
	log.Infof("Internal TON withdrawal processor started")
	for {
		p.waitSync() // gracefulShutdown break must be after waitSync
		if p.gracefulShutdown.Load() {
			log.Infof("Internal TON withdrawal processor stopped")
			break
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*25) // must be < InternalWithdrawalPeriod
		serviceTasks, err := p.repo.GetServiceDepositWithdrawalTasks(ctx, 5)
		if err != nil {
			log.Errorf("get service withdrawal tasks error: %v", err)
		}
		for _, task := range serviceTasks {
			err = p.serviceWithdrawJettons(ctx, task)
			if err != nil {
				log.Errorf("Jettons service internal withdrawal error: %v", err)
			}
			time.Sleep(time.Millisecond * 50)
		}

		internalTasks, err := p.repo.GetTonInternalWithdrawalTasks(ctx, 40) // context limitation
		if err != nil {
			log.Fatalf("get internal withdrawal tasks error: %v", err)
		}
		for _, task := range internalTasks {
			err = p.withdrawTONsFromDeposit(ctx, task)
			if err != nil {
				log.Errorf("TONs internal withdrawal error: %v", err)
			}
			time.Sleep(time.Millisecond * 50)
		}
		cancel()
		time.Sleep(config.InternalWithdrawalPeriod)
	}
}

func (p *WithdrawalsProcessor) withdrawTONsFromDeposit(ctx context.Context, task models.InternalWithdrawalTask) error {
	subwallet, err := p.wallets.TonBasicWallet.GetSubwallet(task.SubwalletID)
	if err != nil {
		return err
	}
	spec := subwallet.GetSpec().(*wallet.SpecV4R2)
	spec.SetMessagesTTL(uint32(config.ExternalMessageLifetime.Seconds()))

	balance, state, err := p.bc.GetAccountCurrentState(ctx, subwallet.Address())
	if err != nil {
		return err
	}
	if state == tlb.AccountStatusNonExist {
		return nil
	}
	if balance.Cmp(config.Config.Ton.Withdrawal) == 1 { // Balance > MinimalTonWithdrawalAmount
		memo, err := uuid.NewV4()
		if err != nil {
			return err
		}
		err = p.repo.SaveInternalWithdrawalTask(ctx, task, time.Now().Add(config.ExternalMessageLifetime), memo)
		if err != nil {
			return err
		}
		// time.Now().Add(config.ExternalMessageLifetime) and real TTL
		// should be very close since the withdrawal occurs immediately
		err = WithdrawTONs(ctx, subwallet, p.wallets.TonHotWallet, memo.String())
		if err != nil {
			audit.Log(audit.Info, string(models.TonDepositWallet), models.InternalWithdrawalEvent,
				fmt.Sprintf("TONs internal withdrawal from deposit %s error: %s",
					task.From.ToUserFormat(), err.Error()))
		}
	}
	return nil
}

func (p *WithdrawalsProcessor) serviceWithdrawJettons(ctx context.Context, task models.ServiceWithdrawalTask) error {
	subwallet, err := p.wallets.TonBasicWallet.GetSubwallet(task.SubwalletID)
	if err != nil {
		return err
	}
	spec := subwallet.GetSpec().(*wallet.SpecV4R2)
	spec.SetMessagesTTL(uint32(config.ExternalMessageLifetime.Seconds()))

	_, state, err := p.bc.GetAccountCurrentState(ctx, subwallet.Address())
	if err != nil {
		return err
	}
	if state == tlb.AccountStatusNonExist {
		return nil
	}

	jettonWallet, err := p.bc.GetJettonWalletAddress(ctx, subwallet.Address(), task.JettonMaster.ToTonutilsAddressStd(0))
	if err != nil {
		return err
	}

	err = p.repo.UpdateServiceWithdrawalRequest(ctx, task, models.ZeroCoins(),
		time.Now().Add(config.ExternalMessageLifetime), false)
	if err != nil {
		return err
	}
	// time.Now().Add(config.ExternalMessageLifetime) and real TTL
	// should be very close since the withdrawal occurs immediately
	err = WithdrawJettons(ctx, subwallet, p.wallets.TonHotWallet, jettonWallet, tlb.FromNanoTONU(0),
		task.JettonAmount, task.Memo.String()) // zero forward TON amount to prevent notify message invoking
	if err != nil {
		log.Errorf("Jettons service withdrawal error: %v", err)
		audit.Log(audit.Info, string(models.TonDepositWallet), models.ServiceWithdrawalEvent,
			fmt.Sprintf("Jettons service withdrawal from deposit %s error: %s",
				task.From.ToUserFormat(), err.Error()))
	}
	return nil
}

func (p *WithdrawalsProcessor) waitSync() {
	for {
		if p.gracefulShutdown.Load() {
			log.Infof("WaitSync interrupted")
			break
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
		isSynced, err := p.repo.IsActualBlockData(ctx)
		if err != nil {
			log.Fatalf("check sync error: %v", err)
		}
		if isSynced {
			cancel()
			break
		}
		cancel()
		time.Sleep(time.Second * 3)
	}
}

func (p *WithdrawalsProcessor) makeColdWalletWithdrawals(ctx context.Context) error {
	if p.coldWallet == nil {
		return nil
	}

	tonBalance, _, err := p.bc.GetAccountCurrentState(ctx, p.wallets.TonHotWallet.Address())
	if err != nil {
		return err
	}
	dest := models.AddressMustFromTonutilsAddress(p.coldWallet)

	for cur, jw := range p.wallets.JettonHotWallets {
		status, err := p.repo.GetInternalWithdrawalStatus(ctx, dest, cur)
		if err != nil {
			return err
		}
		if status == models.WithdrawalStatusProcessing {
			continue
		}
		jettonBalance, err := p.bc.GetLastJettonBalance(ctx, jw.Address)
		if err != nil {
			return err
		}
		if jettonBalance.Cmp(config.Config.Jettons[cur].HotWalletMaxCutoff) != 1 { // jettonBalance <= HotWalletMaxCutoff
			continue
		}
		jettonAmount := big.NewInt(0)
		u, err := uuid.NewV4()
		if err != nil {
			return err
		}
		jettonAmount.Sub(jettonBalance, config.Config.Jettons[cur].HotWalletMaxCutoff)
		tonBalance.Sub(tonBalance, config.JettonTransferTonAmount.NanoTON())
		req := models.WithdrawalRequest{
			Currency:    jw.Currency,
			Amount:      models.NewCoins(jettonAmount),
			Bounceable:  true,
			Destination: dest,
			IsInternal:  true,
			QueryID:     u.String(),
		}
		_, err = p.repo.CreateWithdrawalRequest(ctx, req)
		if err != nil {
			return err
		}
		log.Infof("%v withdrawal to cold wallet saved", cur)
	}

	status, err := p.repo.GetInternalWithdrawalStatus(ctx, dest, models.TonSymbol)
	if err != nil {
		return err
	}
	if status == models.WithdrawalStatusProcessing {
		return nil
	}

	if tonBalance.Cmp(config.Config.Ton.HotWalletMax) != 1 { // tonBalance <= HotWalletMax
		return nil
	}

	tonAmount := big.NewInt(0)
	u, err := uuid.NewV4()
	if err != nil {
		return err
	}
	tonAmount.Sub(tonBalance, config.Config.Ton.HotWalletMax)
	req := models.WithdrawalRequest{
		Currency:    models.TonSymbol,
		Amount:      models.NewCoins(tonAmount),
		Bounceable:  p.coldWallet.IsBounceable(),
		Destination: dest,
		IsInternal:  true,
		QueryID:     u.String(),
	}

	_, err = p.repo.CreateWithdrawalRequest(ctx, req)
	if err != nil {
		return err
	}
	log.Infof("TON withdrawal to cold wallet saved")
	return nil
}
