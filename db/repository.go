package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gobicycle/bicycle/audit"
	"github.com/gobicycle/bicycle/config"
	"github.com/gobicycle/bicycle/models"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xssnick/tonutils-go/ton"
	"github.com/xssnick/tonutils-go/ton/wallet"
)

type repository struct {
	client *pgxpool.Pool
}

func NewRepository(ctx context.Context, client *pgxpool.Pool) (Repository, error) {
	conn := repository{client}
	return &conn, nil
}

// GetLastSubwalletID returns last (greatest) used subwallet_id from DB
// numeration starts from wallet.DefaultSubwallet (this number reserved for main hot wallet)
// returns wallet.DefaultSubwallet if table is empty
func (c *repository) GetLastSubwalletID(ctx context.Context) (uint32, error) {
	var id uint32
	err := c.client.QueryRow(ctx, `
		SELECT COALESCE(MAX(subwallet_id), $1)
		FROM payments.ton_wallets
	`, wallet.DefaultSubwallet).Scan(&id)
	return id, err
}

func (c *repository) SaveTonWallet(ctx context.Context, walletData models.WalletData) error {
	_, err := c.client.Exec(ctx, `
		INSERT INTO payments.ton_wallets (
		user_id,
		subwallet_id,
		type,
		address)
		VALUES ($1, $2, $3,$4)         
	`,
		walletData.UserID,
		walletData.SubwalletID,
		walletData.Type,
		walletData.Address,
	)
	if err != nil {
		return err
	}
	c.addressBook.put(walletData.Address, models.AddressInfo{Type: walletData.Type, Owner: nil})
	return nil
}

func (c *repository) GetJettonWallet(ctx context.Context, address models.Address) (*models.WalletData, bool, error) {
	d := models.WalletData{
		Address: address,
	}
	err := c.client.QueryRow(ctx, `
		SELECT subwallet_id, user_id, currency, type
		FROM payments.jetton_wallets
		WHERE  address = $1
	`, address).Scan(&d.SubwalletID, &d.UserID, &d.Currency, &d.Type)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &d, true, nil
}

func (c *repository) SaveJettonWallet(
	ctx context.Context,
	ownerAddress models.Address,
	walletData models.WalletData,
	notSaveOwner bool,
) error {
	tx, err := c.client.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if !notSaveOwner {
		_, err = tx.Exec(ctx, `
		INSERT INTO payments.ton_wallets (
		user_id,
		subwallet_id,
		type,
		address)
		VALUES ($1, $2, $3,$4)            
	`,
			walletData.UserID,
			walletData.SubwalletID,
			models.JettonOwner,
			ownerAddress,
		)
		if err != nil {
			return err
		}
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO payments.jetton_wallets (
		user_id,
		subwallet_id,
		currency,
		type,
		address)
		VALUES ($1, $2, $3, $4, $5)
	`,
		walletData.UserID,
		walletData.SubwalletID,
		walletData.Currency,
		walletData.Type,
		walletData.Address,
	)
	if err != nil {
		return err
	}

	err = tx.Commit(ctx)
	if err != nil {
		return err
	}

	if walletData.Type == models.JettonDepositWallet {
		// only jetton deposit owners tracked by address book
		// hot TON wallet also owner of jetton hot wallets
		// cold wallets excluded from address book
		c.addressBook.put(ownerAddress, models.AddressInfo{Type: models.JettonOwner, Owner: nil})
	}
	c.addressBook.put(walletData.Address, models.AddressInfo{Type: walletData.Type, Owner: &ownerAddress})
	return nil
}

func (c *repository) GetTonWalletsAddresses(
	ctx context.Context,
	userID string,
	types []models.WalletType,
) (
	[]models.Address,
	error,
) {
	if types == nil {
		types = make([]models.WalletType, 0)
	}
	rows, err := c.client.Query(ctx, `
		SELECT address
		FROM payments.ton_wallets
		WHERE user_id = $1 AND type=ANY($2)
	`, userID, types)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []models.Address
	for rows.Next() {
		var a models.Address
		err = rows.Scan(&a)
		if err != nil {
			return nil, err
		}
		res = append(res, a)
	}
	return res, nil
}

func (c *repository) GetJettonOwnersAddresses(
	ctx context.Context,
	userID string,
	types []models.WalletType,
) (
	[]models.OwnerWallet,
	error,
) {
	if types == nil {
		types = make([]models.WalletType, 0)
	}
	rows, err := c.client.Query(ctx, `
		SELECT tw.address, jw.currency
		FROM payments.jetton_wallets jw
		LEFT JOIN payments.ton_wallets tw ON jw.subwallet_id = tw.subwallet_id 
		WHERE jw.user_id = $1 AND jw.type=ANY($2)
	`, userID, types)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []models.OwnerWallet
	for rows.Next() {
		var ow models.OwnerWallet
		err = rows.Scan(&ow.Address, &ow.Currency)
		if err != nil {
			return nil, err
		}
		res = append(res, ow)
	}
	return res, nil
}

func saveExternalIncome(ctx context.Context, tx pgx.Tx, inc models.ExternalIncome) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO payments.external_incomes (
		lt,
		utime,
		deposit_address,
		payer_address,
		amount,
		comment,
		payer_workchain)
		VALUES ($1, $2, $3, $4, $5, $6, $7)                                               
	`,
		inc.Lt,
		time.Unix(int64(inc.Utime), 0),
		inc.To,
		inc.From,
		inc.Amount,
		inc.Comment,
		inc.FromWorkchain,
	)
	return err
}

func (c *repository) saveInternalIncome(ctx context.Context, tx pgx.Tx, inc models.InternalIncome) error {
	memo, err := uuid.FromString(inc.Memo)
	if err != nil {
		return err
	}

	wType, ok := c.GetWalletType(inc.From)
	var from models.Address
	if ok && wType == models.JettonOwner { // convert jetton owner address to jetton wallet address
		err = tx.QueryRow(ctx, `
			SELECT jw.address
			FROM payments.ton_wallets tw
			LEFT JOIN payments.jetton_wallets jw ON tw.subwallet_id = jw.subwallet_id
			WHERE tw.address = $1
		`, inc.From).Scan(&from)
		if err != nil {
			return err
		}
	} else {
		from = inc.From
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO payments.internal_incomes (
		lt,
		utime,
		deposit_address,
		amount,
		memo)
		VALUES ($1, $2, $3, $4, $5)                                               
	`,
		inc.Lt,
		time.Unix(int64(inc.Utime), 0),
		from,
		inc.Amount,
		memo,
	)
	return err
}

func saveBlock(ctx context.Context, tx pgx.Tx, block models.ShardBlockHeader) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO payments.block_data (
		shard,
		seqno,
		root_hash,
		file_hash,
		gen_utime                                 
		) VALUES ($1, $2, $3, $4, $5)                                               
	`,
		block.Shard,
		block.SeqNo,
		block.RootHash,
		block.FileHash,
		time.Unix(int64(block.GenUtime), 0),
	)
	return err
}

func updateInternalWithdrawal(ctx context.Context, tx pgx.Tx, w models.InternalWithdrawal) error {
	memo, err := uuid.FromString(w.Memo)
	if err != nil {
		return err
	}

	var (
		sendingLt     *int64
		alreadyFailed bool
	)

	err = tx.QueryRow(ctx, `
		SELECT failed, sending_lt
		FROM payments.internal_withdrawals
		WHERE  memo = $1
	`, memo).Scan(&alreadyFailed, &sendingLt)

	if alreadyFailed {
		audit.Log(audit.Error, "internal withdrawal message", models.InternalWithdrawalEvent,
			fmt.Sprintf("successful withdrawal for expired internal withdrawal message. memo: %v", w.Memo))
		return fmt.Errorf("invalid behavior of the expiration processor")
	}

	if sendingLt == nil {
		audit.Log(audit.Error, "internal withdrawal message", models.InternalWithdrawalEvent,
			fmt.Sprintf("successful withdrawal without sending confirmation. memo: %v", w.Memo))
		return fmt.Errorf("invalid event order")
	}

	if w.IsFailed {
		_, err = tx.Exec(ctx, `
			UPDATE payments.internal_withdrawals
			SET
		    	failed = true
			WHERE  memo = $1
		`, memo)
		return err
	}
	_, err = tx.Exec(ctx, `
			UPDATE payments.internal_withdrawals
			SET
		    	finish_lt = $1,
		    	finished_at = $2,
		    	amount = amount + $3
			WHERE  memo = $4
		`, w.Lt, time.Unix(int64(w.Utime), 0), w.Amount, memo)
	return err
}

func (c *repository) SaveParsedBlockData(ctx context.Context, events models.BlockEvents) error {
	tx, err := c.client.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, ei := range events.ExternalIncomes {
		err = saveExternalIncome(ctx, tx, ei)
		if err != nil {
			return err
		}
	}
	for _, ii := range events.InternalIncomes {
		err = c.saveInternalIncome(ctx, tx, ii)
		if err != nil {
			return err
		}
	}
	for _, sc := range events.SendingConfirmations {
		err = applySendingConfirmations(ctx, tx, sc)
		if err != nil {
			return err
		}
	}
	for _, iw := range events.InternalWithdrawals {
		err = updateInternalWithdrawal(ctx, tx, iw)
		if err != nil {
			return err
		}
	}
	for _, ew := range events.ExternalWithdrawals {
		err = updateExternalWithdrawal(ctx, tx, ew)
		if err != nil {
			return err
		}
	}
	for _, wc := range events.WithdrawalConfirmations {
		err = applyJettonWithdrawalConfirmation(ctx, tx, wc)
		if err != nil {
			return err
		}
	}
	err = saveBlock(ctx, tx, events.Block)
	if err != nil {
		return err
	}
	err = tx.Commit(ctx)
	return err
}

func applyJettonWithdrawalConfirmation(
	ctx context.Context,
	tx pgx.Tx,
	confirm models.JettonWithdrawalConfirmation,
) error {
	_, err := tx.Exec(ctx, `
			UPDATE payments.external_withdrawals
			SET
		    	confirmed = true
			WHERE  query_id = $1 AND processed_lt IS NOT NULL
		`, confirm.QueryId)
	return err
}

func updateExternalWithdrawal(ctx context.Context, tx pgx.Tx, w models.ExternalWithdrawal) error {
	var queryID int64

	var alreadyFailed bool
	err := tx.QueryRow(ctx, `
		SELECT failed
		FROM payments.external_withdrawals
		WHERE  msg_uuid = $1 AND address = $2
	`, w.ExtMsgUuid, w.To).Scan(&alreadyFailed)
	if alreadyFailed {
		audit.Log(audit.Error, "external withdrawal message", models.ExternalWithdrawalEvent,
			fmt.Sprintf("successful withdrawal for expired external withdrawal message. msg uuid: %v", w.ExtMsgUuid.String()))
		return fmt.Errorf("invalid behavior of the expiration processor")
	}

	if w.IsFailed {
		err := tx.QueryRow(ctx, `
			UPDATE payments.external_withdrawals
			SET
		    	failed = true
			WHERE  msg_uuid = $1 AND address = $2
			RETURNING query_id
		`, w.ExtMsgUuid, w.To).Scan(&queryID)
		if err != nil {
			return err
		}

		_, err = tx.Exec(ctx, `
			UPDATE payments.withdrawal_requests
			SET
		    	processing = false
			WHERE  query_id = $1
		`, queryID)
		return err
	}

	err = tx.QueryRow(ctx, `
			UPDATE payments.external_withdrawals
			SET
		    	processed_lt = $1,
		    	processed_at = $2
			WHERE  msg_uuid = $3 AND address = $4
			RETURNING query_id
		`, w.Lt, time.Unix(int64(w.Utime), 0), w.ExtMsgUuid, w.To).Scan(&queryID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
			UPDATE payments.withdrawal_requests
			SET
		    	processed = true
			WHERE  query_id = $1
		`, queryID)
	return err
}

func applySendingConfirmations(ctx context.Context, tx pgx.Tx, w models.SendingConfirmation) error {
	var alreadyFailed bool
	memo, err := uuid.FromString(w.Memo)
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `
			UPDATE payments.internal_withdrawals
			SET
		    	sending_lt = $1
			WHERE  memo = $2
			RETURNING failed
		`, w.Lt, memo).Scan(&alreadyFailed)
	if err != nil {
		return err
	}
	if alreadyFailed {
		audit.Log(audit.Error, "internal withdrawal message", models.InternalWithdrawalEvent,
			fmt.Sprintf("successful sending for expired internal withdrawal message. memo: %v", w.Memo))
		return fmt.Errorf("invalid behavior of the expiration processor")
	}
	return err
}

func (c *repository) GetTonHotWalletAddress(ctx context.Context) (models.Address, error) {
	var addr models.Address
	err := c.client.QueryRow(ctx, `
		SELECT address 
		FROM payments.ton_wallets
		WHERE TYPE = $1
	`, models.TonHotWallet).Scan(&addr)
	if errors.Is(err, pgx.ErrNoRows) {
		err = models.ErrNotFound
	}
	return addr, err
}

func (c *repository) GetLastSavedBlockID(ctx context.Context) (*ton.BlockIDExt, error) {
	var blockID ton.BlockIDExt
	err := c.client.QueryRow(ctx, `
		SELECT 
		    seqno, 
		    shard, 
		    root_hash, 
		    file_hash
		FROM payments.block_data
		ORDER BY seqno DESC
		LIMIT 1
	`).Scan(
		&blockID.SeqNo,
		&blockID.Shard,
		&blockID.RootHash,
		&blockID.FileHash,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, models.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	blockID.Workchain = models.DefaultWorkchain
	return &blockID, nil
}

// SetExpired TODO: maybe add block related expiration
func (c *repository) SetExpired(ctx context.Context) error {
	_, err := c.client.Exec(ctx, `
			UPDATE payments.internal_withdrawals
			SET
		    	failed = true		    	
			WHERE  expired_at < $1 AND sending_lt IS NULL AND failed = false
	`, time.Now().Add(-config.AllowableBlockchainLagging))
	if err != nil {
		return err
	}

	tx, err := c.client.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
			UPDATE payments.external_withdrawals
			SET
		    	failed = true		    	
			WHERE  expired_at < $1 AND processed_lt IS NULL AND failed = false
			RETURNING query_id
	`, time.Now().Add(-config.AllowableBlockchainLagging))

	if err != nil {
		return err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var queryID int64
		err = rows.Scan(&queryID)
		if err != nil {
			return err
		}
		ids = append(ids, queryID)
	}

	for _, id := range ids {
		_, err = tx.Exec(ctx, `
			UPDATE payments.withdrawal_requests
			SET
		    	processing = false
			WHERE  query_id = $1
		`, id)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (c *repository) IsActualBlockData(ctx context.Context) (bool, error) {
	var lastBlockTime time.Time
	err := c.client.QueryRow(ctx, `
		SELECT 
		    gen_utime
		FROM payments.block_data
		ORDER BY seqno DESC
		LIMIT 1
	`).Scan(&lastBlockTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return time.Since(lastBlockTime) < config.AllowableBlockchainLagging, nil
}

// GetIncome returns list of incomes by user_id
func (c *repository) GetIncome(
	ctx context.Context,
	userID string,
	isDepositSide bool,
) (
	[]models.TotalIncome,
	error,
) {
	var sqlStatement string
	if isDepositSide {
		sqlStatement = `
			SELECT COALESCE(jw.address,tw.address) as deposit, COALESCE(SUM(i.amount),0) as balance, COALESCE(jw.currency,$1) as currency
			FROM payments.ton_wallets tw
         		LEFT JOIN payments.jetton_wallets jw ON jw.subwallet_id = tw.subwallet_id
         		LEFT JOIN payments.external_incomes i ON i.deposit_address = COALESCE(jw.address,tw.address)
			WHERE tw.user_id = $2 AND tw.type = ANY($3)
			GROUP BY deposit, tw.address, jw.currency
		`
	} else {
		sqlStatement = `
			SELECT COALESCE(jw.address,tw.address) as deposit, COALESCE(SUM(i.amount),0) as balance, COALESCE(jw.currency,$1) as currency
			FROM payments.ton_wallets tw
         		LEFT JOIN payments.jetton_wallets jw ON jw.subwallet_id = tw.subwallet_id
         		LEFT JOIN payments.internal_incomes i ON i.deposit_address = COALESCE(jw.address,tw.address)
			WHERE tw.user_id = $2 AND tw.type = ANY($3)
			GROUP BY deposit, tw.address, jw.currency
		`
	}

	rows, err := c.client.Query(
		ctx,
		sqlStatement,
		models.TonSymbol,
		userID,
		[]models.WalletType{models.TonDepositWallet, models.JettonOwner},
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make([]models.TotalIncome, 0)
	for rows.Next() {
		var deposit models.TotalIncome
		err = rows.Scan(&deposit.Deposit, &deposit.Amount, &deposit.Currency)
		if err != nil {
			return nil, err
		}
		res = append(res, deposit)
	}
	return res, nil
}

// GetIncomeHistory returns list of external incomes for deposit side by user_id and currency
func (c *repository) GetIncomeHistory(
	ctx context.Context,
	userID string,
	currency string,
	limit int,
	offset int,
) (
	[]models.ExternalIncome,
	error,
) {
	var (
		res          []models.ExternalIncome
		sqlStatement string
		walletType   models.WalletType
	)

	if currency == models.TonSymbol {
		sqlStatement = `
			SELECT utime, lt, payer_address, deposit_address, amount, comment, payer_workchain
			FROM payments.external_incomes i
				LEFT JOIN payments.ton_wallets tw ON i.deposit_address = tw.address
			WHERE tw.type = $1 AND tw.user_id = $2 AND $3 = $3
			ORDER BY lt DESC
			LIMIT $4
			OFFSET $5
		`
		walletType = models.TonDepositWallet
	} else {
		sqlStatement = `
			SELECT utime, lt, payer_address, deposit_address, amount, comment, payer_workchain
			FROM payments.external_incomes i
			    LEFT JOIN payments.jetton_wallets jw ON i.deposit_address = jw.address
			WHERE jw.type = $1 AND jw.user_id = $2 AND jw.currency = $3
			ORDER BY lt DESC
			LIMIT $4
			OFFSET $5
		`
		walletType = models.JettonDepositWallet
	}

	rows, err := c.client.Query(ctx, sqlStatement, walletType, userID, currency, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			income models.ExternalIncome
			t      time.Time
		)
		err = rows.Scan(&t, &income.Lt, &income.From, &income.To, &income.Amount, &income.Comment, &income.FromWorkchain)
		if err != nil {
			return nil, err
		}
		income.Utime = uint32(t.Unix())
		res = append(res, income)
	}
	return res, nil
}
