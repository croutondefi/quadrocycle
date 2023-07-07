package usecases

import (
	"context"

	"github.com/gobicycle/bicycle/db"
)

type SyncUsecases struct {
	repo db.Repository
}

func (u *SyncUsecases) IsSynced(ctx context.Context) (bool, error) {
	block, err := u.repo.GetLastSavedBlock(ctx)
	if err != nil {
		return false, err
	}

	return !block.IsExpired(), nil
}
