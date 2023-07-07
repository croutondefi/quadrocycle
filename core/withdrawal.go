package core

import (
	"context"
	"fmt"
	"math/big"
	"math/rand"

	"github.com/gobicycle/bicycle/config"
	"github.com/gobicycle/bicycle/models"
	"github.com/gofrs/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton/wallet"
	"github.com/xssnick/tonutils-go/tvm/cell"
)

// WithdrawTON
// Send all TON from one wallet (and deploy it if needed) to another and destroy "from" wallet contract.
// Wallet must be not empty.
func WithdrawTON(ctx context.Context, from, to *wallet.Wallet, comment string) error {
	if from == nil || to == nil || to.Address() == nil {
		return fmt.Errorf("nil wallet")
	}
	var body *cell.Cell
	if comment != "" {
		body = buildComment(comment)
	}
	return from.Send(ctx, &wallet.Message{
		Mode: 128 + 32, // 128 + 32 send all and destroy
		InternalMessage: &tlb.InternalMessage{
			IHRDisabled: true,
			Bounce:      false,
			DstAddr:     to.Address(),
			Amount:      tlb.FromNanoTONU(0),
			Body:        body,
		},
	}, false)
}

func WithdrawJetton(
	ctx context.Context,
	from, to *wallet.Wallet,
	jettonWallet *address.Address,
	forwardAmount tlb.Coins,
	amount models.Coins,
	comment string,
) error {
	if from == nil || to == nil || to.Address() == nil {
		return fmt.Errorf("nil wallet")
	}
	body := makeJettonTransferMessage(
		to.Address(),
		to.Address(),
		amount.BigInt(),
		forwardAmount,
		rand.Int63(),
		comment,
	)
	return from.Send(ctx, &wallet.Message{
		Mode: 128 + 32, // 128 + 32 send all and destroy
		InternalMessage: &tlb.InternalMessage{
			IHRDisabled: true,
			Bounce:      true,
			DstAddr:     jettonWallet, // jetton wallet address
			Amount:      tlb.FromNanoTONU(0),
			Body:        body,
		},
	}, false)
}

func BuildTonWithdrawalMessage(t models.ExternalWithdrawalTask) *wallet.Message {
	return &wallet.Message{
		Mode: 3,
		InternalMessage: &tlb.InternalMessage{
			IHRDisabled: true,
			Bounce:      t.Bounceable,
			DstAddr:     t.Destination.ToTonutilsAddressStd(0),
			Amount:      tlb.FromNanoTON(t.Amount.BigInt()),
			Body:        buildComment(t.Comment),
		},
	}
}

func BuildJettonWithdrawalMessage(
	t models.ExternalWithdrawalTask,
	highloadWallet *wallet.Wallet,
	fromJettonWallet *address.Address,
) *wallet.Message {
	body := makeJettonTransferMessage(
		t.Destination.ToTonutilsAddressStd(0),
		highloadWallet.Address(),
		t.Amount.BigInt(),
		config.JettonForwardAmount,
		t.QueryID,
		t.Comment,
	)
	return &wallet.Message{
		Mode: 3,
		InternalMessage: &tlb.InternalMessage{
			IHRDisabled: true,
			Bounce:      true,
			DstAddr:     fromJettonWallet,
			Amount:      config.JettonTransferTonAmount,
			Body:        body,
		},
	}
}

func BuildJettonProxyWithdrawalMessage(
	proxy JettonProxy,
	jettonWallet, tonWallet *address.Address,
	forwardAmount tlb.Coins,
	amount *big.Int,
	comment string,
) *wallet.Message {
	jettonTransferPayload := makeJettonTransferMessage(
		tonWallet,
		tonWallet,
		amount,
		forwardAmount,
		rand.Int63(),
		comment,
	)
	msg, err := proxy.BuildMessage(jettonWallet, jettonTransferPayload).ToCell()
	if err != nil {
		log.Fatalf("build proxy message cell error: %v", err)
	}
	body := cell.BeginCell().MustStoreRef(msg).EndCell()
	return &wallet.Message{
		Mode: 3,
		InternalMessage: &tlb.InternalMessage{
			IHRDisabled: true,
			Bounce:      true,
			DstAddr:     proxy.Address(),
			Amount:      config.JettonTransferTonAmount,
			Body:        body,
			StateInit:   proxy.StateInit(),
		},
	}
}

func buildJettonProxyServiceTonWithdrawalMessage(
	proxy JettonProxy,
	tonWallet *address.Address,
	memo uuid.UUID,
) *wallet.Message {
	msg, err := proxy.BuildMessage(tonWallet, buildComment(memo.String())).ToCell()
	if err != nil {
		log.Fatalf("build proxy message cell error: %v", err)
	}
	body := cell.BeginCell().MustStoreRef(msg).EndCell()
	return &wallet.Message{
		Mode: 3,
		InternalMessage: &tlb.InternalMessage{
			IHRDisabled: true,
			Bounce:      true,
			DstAddr:     proxy.Address(),
			Amount:      config.JettonTransferTonAmount,
			Body:        body,
			StateInit:   proxy.StateInit(),
		},
	}
}

func makeJettonTransferMessage(
	destination, responseDest *address.Address,
	amount *big.Int,
	forwardAmount tlb.Coins,
	queryId int64,
	comment string,
) *cell.Cell {

	builder := cell.BeginCell().
		MustStoreUInt(0x0f8a7ea5, 32).             // transfer#0f8a7ea5
		MustStoreUInt(uint64(queryId), 64).        // query_id:uint64
		MustStoreBigCoins(amount).                 // amount:(VarUInteger 16) Jettons amount.
		MustStoreAddr(destination).                // destination:MsgAddress
		MustStoreAddr(responseDest).               // response_destination:MsgAddress
		MustStoreBoolBit(false).                   // custom_payload:(Maybe ^Cell)
		MustStoreBigCoins(forwardAmount.NanoTON()) // forward_ton_amount:(VarUInteger 16)

	if comment != "" {
		return builder.
			MustStoreBoolBit(true). // forward_payload:(Either Cell ^Cell)
			MustStoreRef(buildComment(comment)).
			EndCell()

	}
	return builder.
		MustStoreBoolBit(false). // forward_payload:(Either Cell ^Cell)
		EndCell()

}
