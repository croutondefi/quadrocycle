package db

import (
	"context"
	"errors"
	"time"

	"github.com/gobicycle/bicycle/models"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type withdrawalRepository struct {
	client *pgxpool.Pool
}

func NewWithdrawalRepository(client *pgxpool.Pool) WithdrawalRepository {
	return &withdrawalRepository{
		client: client,
	}
}

func (c *withdrawalRepository) GetExternalWithdrawalStatus(ctx context.Context, id int64) (models.WithdrawalStatus, error) {
	var status models.WithdrawalStatus
	err := c.client.QueryRow(ctx, `
		SELECT status
		FROM payments.withdrawal_requests
		WHERE query_id = $1 AND is_internal = false
		LIMIT 1
	`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", models.ErrNotFound
	}

	return status, err
}

func (c *withdrawalRepository) GetInternalWithdrawalStatus(
	ctx context.Context,
	dest models.Address,
	currency string,
) (
	models.WithdrawalStatus,
	error,
) {
	var status models.WithdrawalStatus
	err := c.client.QueryRow(ctx, `
		SELECT status
		FROM payments.withdrawal_requests
		WHERE dest_address = $1 AND 
		      currency = $2 AND 
		      is_internal = true
		LIMIT 1
	`,
		dest,
		currency,
	).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return status, err
}

func (c *withdrawalRepository) CreateExternalWithdrawals(
	ctx context.Context,
	tasks []models.ExternalWithdrawalTask,
	extMsgUuid uuid.UUID,
	expiredAt time.Time,
) error {
	tx, err := c.client.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for _, t := range tasks {
		_, err = tx.Exec(ctx, `
			INSERT INTO payments.external_withdrawals (
				msg_uuid,
				query_id,
				expired_at,
				address
			) VALUES ($1, $2, $3, $4)                                               
		`, extMsgUuid, t.QueryID, expiredAt, t.Destination)
		if err != nil {
			return err
		}

		_, err = tx.Exec(ctx, `
			UPDATE payments.withdrawal_requests
			SET
		    	processing = true		    	
			WHERE  query_id = $1
		`, t.QueryID)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (c *withdrawalRepository) SaveInternalWithdrawalTask(
	ctx context.Context,
	task models.InternalWithdrawalTask,
	expiredAt time.Time,
	memo uuid.UUID,
) error {
	_, err := c.client.Exec(ctx, `
		INSERT INTO payments.internal_withdrawals (
		since_lt,
		from_address,
		expired_at,
		memo
		) VALUES ($1, $2, $3, $4)
	`,
		task.Lt,
		task.From,
		expiredAt,
		memo,
	)
	return err
}

func (c *withdrawalRepository) CreateWithdrawalRequest(ctx context.Context, w models.WithdrawalRequest) (int64, error) {
	var queryID int64
	err := c.client.QueryRow(ctx, `
		INSERT INTO payments.withdrawal_requests (
		user_id,
		user_query_id,
		amount,
		currency,
		bounceable,
		dest_address,
		comment,
		is_internal
	)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING query_id
	`,
		w.UserID,
		w.QueryID,
		w.Amount,
		w.Currency,
		w.Bounceable,
		w.Destination,
		w.Comment,
		w.IsInternal,
	).Scan(&queryID)
	return queryID, err
}

func (c *withdrawalRepository) UpdateWithdrawalRequest(ctx context.Context, tx pgx.Tx, w *models.WithdrawalRequest) (err error) {
	_, err = tx.Exec(ctx, `
		UPDATE payments.withdrawal_requests
		SET
				status = $1
		WHERE  query_id = $2
`, w.Status, w.QueryID)

	return
}

func (c *withdrawalRepository) SaveServiceWithdrawalRequest(ctx context.Context, w models.ServiceWithdrawalRequest) (
	uuid.UUID,
	error,
) {
	var memo uuid.UUID
	err := c.client.QueryRow(ctx, `
		INSERT INTO payments.service_withdrawal_requests (
		from_address,
		jetton_master		
	)
		VALUES ($1, $2)
		RETURNING memo
	`,
		w.From,
		w.JettonMaster,
	).Scan(&memo)
	return memo, err
}

func (c *withdrawalRepository) UpdateServiceWithdrawalRequest(
	ctx context.Context,
	t models.ServiceWithdrawalTask,
	tonAmount models.Coins,
	expiredAt time.Time,
	filled bool,
) error {
	_, err := c.client.Exec(ctx, `
			UPDATE payments.service_withdrawal_requests
			SET
			    ton_amount = $1,
			    jetton_amount = $2,
		    	processed = not $4,
		    	expired_at = $3,
		    	filled = $4
			WHERE  memo = $5
		`, tonAmount, t.JettonAmount, expiredAt, filled, t.Memo)
	return err
}

func (c *withdrawalRepository) GetExternalWithdrawalTasks(ctx context.Context, limit int) ([]models.ExternalWithdrawalTask, error) {
	var res []models.ExternalWithdrawalTask
	rows, err := c.client.Query(ctx, `
		SELECT DISTINCT ON (dest_address) dest_address,
		                                  query_id,
		                                  currency,
		                                  bounceable,
		                                  comment,
		                                  amount
		FROM   payments.withdrawal_requests
		WHERE  processing = false
        ORDER BY dest_address, query_id
		LIMIT  $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var w models.ExternalWithdrawalTask
		err = rows.Scan(&w.Destination, &w.QueryID, &w.Currency, &w.Bounceable, &w.Comment, &w.Amount)
		if err != nil {
			return nil, err
		}
		res = append(res, w)
	}
	return res, nil
}

// GetServiceHotWithdrawalTasks return tasks for Hot wallet withdrawals
func (c *withdrawalRepository) GetServiceHotWithdrawalTasks(ctx context.Context, limit int) ([]models.ServiceWithdrawalTask, error) {
	var tasks []models.ServiceWithdrawalTask
	rows, err := c.client.Query(ctx, `
		SELECT DISTINCT ON (from_address) swr.from_address,
		                                  swr.memo,
		                                  swr.jetton_master,
		                                  tw.subwallet_id
		FROM   payments.service_withdrawal_requests swr
		LEFT JOIN payments.ton_wallets tw ON swr.from_address = tw.address
		WHERE  processed = false and type = ANY($1) and filled = false
        ORDER BY from_address
		LIMIT  $2
	`, []models.WalletType{models.JettonOwner, models.TonDepositWallet}, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var w models.ServiceWithdrawalTask
		err = rows.Scan(&w.From, &w.Memo, &w.JettonMaster, &w.SubwalletID)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, w)
	}
	return tasks, nil
}

// GetServiceDepositWithdrawalTasks return tasks for TON deposit wallets
func (c *withdrawalRepository) GetServiceDepositWithdrawalTasks(ctx context.Context, limit int) ([]models.ServiceWithdrawalTask, error) {
	var tasks []models.ServiceWithdrawalTask
	rows, err := c.client.Query(ctx, `
		SELECT DISTINCT ON (from_address) swr.from_address,
		                                  swr.memo,
		                                  swr.jetton_master,
		                                  swr.jetton_amount,
		                                  tw.subwallet_id
		FROM   payments.service_withdrawal_requests swr
		LEFT JOIN payments.ton_wallets tw ON swr.from_address = tw.address
		WHERE  processed = false AND filled = true AND type = $1
        ORDER BY from_address
		LIMIT $2
	`, models.TonDepositWallet, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var w models.ServiceWithdrawalTask
		err = rows.Scan(&w.From, &w.Memo, &w.JettonMaster, &w.JettonAmount, &w.SubwalletID)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, w)
	}
	return tasks, nil
}

func (c *withdrawalRepository) GetTonInternalWithdrawalTasks(ctx context.Context, limit int) ([]models.InternalWithdrawalTask, error) {
	var tasks []models.InternalWithdrawalTask
	// lt > finish_lt condition because all TONs withdraws
	rows, err := c.client.Query(ctx, `
		SELECT deposit_address, MAX(lt) AS last_lt, tw.subwallet_id
		FROM payments.external_incomes di
			LEFT JOIN (
			SELECT from_address, since_lt, finish_lt
			FROM payments.internal_withdrawals iw1
			WHERE since_lt = (
				SELECT MAX(iw2.since_lt)
				FROM payments.internal_withdrawals iw2
				WHERE iw2.from_address = iw1.from_address AND failed = false
			)
		) as iw3 ON from_address = deposit_address
		JOIN payments.ton_wallets tw ON di.deposit_address = tw.address
		WHERE ((since_lt IS NOT NULL AND finish_lt IS NOT NULL AND lt > finish_lt) OR (since_lt IS NULL)) 
		  AND type = $1
		GROUP BY deposit_address, tw.subwallet_id
		LIMIT $2
	`, models.TonDepositWallet, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var task models.InternalWithdrawalTask
		err = rows.Scan(&task.From, &task.Lt, &task.SubwalletID)
		if err != nil {
			return nil, err
		}
		task.Currency = models.TonSymbol
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func (c *withdrawalRepository) GetJettonInternalWithdrawalTasks(
	ctx context.Context,
	forbiddenAddresses []models.Address,
	limit int,
) (
	[]models.InternalWithdrawalTask, error,
) {
	var tasks []models.InternalWithdrawalTask
	excludedAddr := make([][]byte, 0) // it is important for 'deposit_address = ANY($2)' sql constraint
	for _, a := range forbiddenAddresses {
		excludedAddr = append(excludedAddr, a[:]) // array of models.Address not supported by driver
	}
	rows, err := c.client.Query(ctx, `
		SELECT deposit_address, MAX(lt) AS last_lt, jw.subwallet_id, jw.currency
		FROM payments.external_incomes di
			LEFT JOIN (
			SELECT from_address, since_lt, finish_lt
			FROM payments.internal_withdrawals iw1
			WHERE since_lt = (
				SELECT MAX(iw2.since_lt)
				FROM payments.internal_withdrawals iw2
				WHERE iw2.from_address = iw1.from_address AND failed = false
			)
		) as iw3 ON from_address = deposit_address
		JOIN payments.jetton_wallets jw ON di.deposit_address = jw.address
		LEFT JOIN payments.ton_wallets tw ON jw.subwallet_id = tw.subwallet_id
		WHERE ((since_lt IS NOT NULL AND lt > since_lt AND finish_lt IS NOT NULL)
		   OR (since_lt IS NULL)) AND jw.type = $1 AND NOT tw.address = ANY($2)
		GROUP BY deposit_address, jw.subwallet_id, jw.currency
		LIMIT $3
	`, models.JettonDepositWallet, excludedAddr, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var task models.InternalWithdrawalTask
		err = rows.Scan(&task.From, &task.Lt, &task.SubwalletID, &task.Currency)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}
