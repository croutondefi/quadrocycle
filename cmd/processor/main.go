package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gobicycle/bicycle/api"
	"github.com/gobicycle/bicycle/blockchain"
	"github.com/gobicycle/bicycle/config"
	"github.com/gobicycle/bicycle/core"
	"github.com/gobicycle/bicycle/db"
	"github.com/gobicycle/bicycle/queue"
	"github.com/gobicycle/bicycle/webhook"
	log "github.com/sirupsen/logrus"
	"github.com/xssnick/tonutils-go/ton"
)

var blocksChan chan *core.ShardBlockHeader

func main() {
	config.GetConfig()

	sigChannel := make(chan os.Signal, 1)
	signal.Notify(sigChannel, os.Interrupt, syscall.SIGTERM)

	bcClient, err := blockchain.NewConnection(config.Config.LiteServerConfigURL, config.Config.DefaultWalletVersion)
	if err != nil {
		log.Fatalf("blockchain connection error: %v", err)
	}

	dbClient, err := db.NewConnection(config.Config.DatabaseURI)
	if err != nil {
		log.Fatalf("DB connection error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*120)
	defer cancel()

	err = dbClient.LoadAddressBook(ctx)
	if err != nil {
		log.Fatalf("address book loading error: %v", err)
	}

	isTimeSynced, err := bcClient.CheckTime(ctx, config.AllowableServiceToNodeTimeDiff)
	if err != nil {
		log.Fatalf("get node time err: %v", err)
	}
	if !isTimeSynced {
		log.Fatalf("Service and Node time not synced")
	}

	wallets, err := core.InitWallets(ctx, dbClient, bcClient, config.Config.Seed, config.Config.Jettons)
	if err != nil {
		log.Fatalf("Hot wallets initialization error: %v", err)
	}

	var notificators []core.Notificator

	if config.Config.QueueEnabled {
		queueClient, err := queue.NewAmqpClient(config.Config.QueueURI, config.Config.QueueEnabled, config.Config.QueueName)
		if err != nil {
			log.Fatalf("new queue client creating error: %v", err)
		}
		notificators = append(notificators, queueClient)
	}

	if config.Config.WebhookEndpoint != "" {
		webhookClient, err := webhook.NewWebhookClient(config.Config.WebhookEndpoint, config.Config.WebhookToken)
		if err != nil {
			log.Fatalf("new webhook client creating error: %v", err)
		}
		notificators = append(notificators, webhookClient)
	}

	scannerCtx, scannerCancel := context.WithCancel(context.Background())
	defer scannerCancel()

	blocksChan = make(chan *core.ShardBlockHeader)

	var startBlock *ton.BlockIDExt
	startBlock, err = dbClient.GetLastSavedBlockID(ctx)

	if err != nil && !errors.Is(err, core.ErrNotFound) {
		log.Fatalf("Get last saved block error: %v", err)
	}
	tracker := blockchain.NewShardTracker(wallets.Shard, startBlock, bcClient, blocksChan)

	go tracker.Start(scannerCtx)

	blockScanner := core.NewBlockScanner(dbClient, bcClient, wallets.Shard, notificators, blocksChan)
	go blockScanner.Start(scannerCtx)

	withdrawalsProcessor := core.NewWithdrawalsProcessor(
		dbClient, bcClient, wallets, config.Config.ColdWallet)
	withdrawalsProcessor.Start()

	apiMux := http.NewServeMux()
	h := api.NewHandler(dbClient, bcClient, config.Config.APIToken, wallets.Shard, *wallets.TonHotWallet.Address())
	api.RegisterHandlers(apiMux, h)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%s", config.Config.APIPort),
		WriteTimeout: time.Second * 15,
		ReadTimeout:  time.Second * 15,
		IdleTimeout:  time.Second * 60,
		Handler:      apiMux,
	}

	go func() {
		<-sigChannel
		log.Printf("SIGTERM received")
		scannerCancel()
		close(blocksChan)
		withdrawalsProcessor.Stop()
	}()

	srv.ListenAndServe()
	if err != nil {
		log.Fatalf("api error: %v", err)
	}
}
