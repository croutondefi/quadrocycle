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
	"github.com/gobicycle/bicycle/api/handlers"
	"github.com/gobicycle/bicycle/config"
	"github.com/gobicycle/bicycle/core"
	"github.com/gobicycle/bicycle/core/wallet"
	"github.com/gobicycle/bicycle/db"
	"github.com/gobicycle/bicycle/models"
	"github.com/gobicycle/bicycle/queue"
	"github.com/gobicycle/bicycle/webhook"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	log "github.com/sirupsen/logrus"
	"github.com/uptrace/bunrouter"
	"github.com/xssnick/tonutils-go/liteclient"
	"github.com/xssnick/tonutils-go/ton"
)

var blocksChan chan *models.ShardBlockHeader

func main() {
	config.GetConfig()

	sigChannel := make(chan os.Signal, 1)
	signal.Notify(sigChannel, os.Interrupt, syscall.SIGTERM)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*120)
	defer cancel()

	lClient := liteclient.NewConnectionPool()
	err := lClient.AddConnectionsFromConfigUrl(ctx, config.Config.LiteServerConfigURL)
	if err != nil {
		log.Fatalf("connection err: %v", err.Error())
	}

	tonApi := ton.NewAPIClient(lClient)

	bcClient, err := core.NewConnection(ctx, tonApi)
	if err != nil {
		log.Fatalf("blockchain connection error: %v", err)
	}

	fmt.Println("config.Config.DatabaseURI", config.Config.DatabaseURI)

	conn, err := pgx.Connect(ctx, config.Config.DatabaseURI)
	if err != nil {
		log.Fatalf("DB connection error: %v", err)
	}

	err = conn.Ping(ctx)
	if err != nil {
		log.Fatalf("failed to ping db: %v", err)
	}

	pool, err := pgxpool.New(ctx, config.Config.DatabaseURI)
	if err != nil {
		log.Fatalf("DB connection error: %v", err)
	}

	err = pool.Ping(ctx)
	if err != nil {
		log.Fatalf("failed to ping db: %v", err)
	}

	defer pool.Close()

	repo, err := db.NewRepository(ctx, pool)
	if err != nil {
		log.Fatalf("address book loading error: %v", err)
	}

	startBlock, err := repo.GetLastSavedBlock(ctx)

	fmt.Println("startBlock ", startBlock, err)

	if err != nil && !errors.Is(err, models.ErrNotFound) {
		log.Fatalf("Get last saved block error: %v", err)
	}

	fmt.Println("before wallet.InitWallets")

	wallets, err := wallet.InitWallets(
		ctx,
		repo,
		bcClient,
		config.Config.Seed,
		config.Config.Jettons,
		config.Config.DefaultWalletVersion,
	)

	fmt.Println("after wallet.InitWallets")

	var notificators []models.Notificator

	if config.Config.QueueEnabled {
		queueClient, err := queue.NewAmqpClient(config.Config.QueueURI, config.Config.QueueEnabled, config.Config.QueueName)
		if err != nil {
			log.Fatalf("new queue client creating error: %v", err)
		}
		notificators = append(notificators, queueClient)
	}

	fmt.Println("11")

	if config.Config.WebhookEndpoint != "" {
		webhookClient, err := webhook.NewWebhookClient(config.Config.WebhookEndpoint, config.Config.WebhookToken)
		if err != nil {
			log.Fatalf("new webhook client creating error: %v", err)
		}
		notificators = append(notificators, webhookClient)
	}

	fmt.Println("22")

	scannerCtx, scannerCancel := context.WithCancel(context.Background())
	defer scannerCancel()

	blocksChan = make(chan *models.ShardBlockHeader)

	fmt.Println("33")

	tracker := core.NewShardTracker(wallets.Shard(), startBlock.BlockIDExt, tonApi, blocksChan)

	go tracker.Start(scannerCtx)

	blockScanner := core.NewBlockScanner(repo, bcClient, wallets, notificators, blocksChan)
	go blockScanner.Start(scannerCtx)

	withdrawalsProcessor := core.NewWithdrawalsProcessor(
		repo, bcClient, wallets, config.Config.ColdWallet)
	withdrawalsProcessor.Start()

	router := bunrouter.New(
		bunrouter.Use(api.HeadersMiddleware()),
		bunrouter.Use(api.RecoverMiddleware()),
		bunrouter.Use(api.AuthMiddleware()),
		bunrouter.WithNotFoundHandler(notFoundHandler),
	)
	h := handlers.NewHandler()

	router.WithGroup("/v1", func(group *bunrouter.Group) {
		h.Register(group)
	})

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%s", config.Config.APIPort),
		WriteTimeout: time.Second * 15,
		ReadTimeout:  time.Second * 15,
		IdleTimeout:  time.Second * 60,
		Handler:      router,
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

func notFoundHandler(w http.ResponseWriter, req bunrouter.Request) error {
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(
		w,
		"<html>can't find a route that matches <strong>%s</strong></html>",
		req.URL.Path,
	)
	return nil
}
