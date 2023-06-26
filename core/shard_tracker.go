package core

import (
	"context"
	"math/bits"
	"strings"
	"time"

	"github.com/gobicycle/bicycle/models"
	log "github.com/sirupsen/logrus"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton"
)

const ErrBlockNotApplied = "block is not applied"

type ShardTracker struct {
	tonApi              *ton.APIClient
	shard               byte
	lastKnownShardBlock *ton.BlockIDExt
	lastMasterBlock     *ton.BlockIDExt
	blocksChan          chan *models.ShardBlockHeader
}

// NewShardTracker creates new tracker to get blocks with specific shard attribute
func NewShardTracker(
	shard byte,
	startBlock *ton.BlockIDExt,
	tonApi *ton.APIClient,
	blocksChan chan *models.ShardBlockHeader,
) *ShardTracker {
	t := &ShardTracker{
		tonApi:              tonApi,
		shard:               shard,
		lastKnownShardBlock: startBlock,
		blocksChan:          blocksChan,
	}
	return t
}

// Start scans for blocks.
func (s *ShardTracker) Start(ctx context.Context) {
	// the interval between blocks can be up to 40 seconds
	ctx = s.tonApi.Client().StickyContext(ctx)

	for {
		masterBlock, err := s.getCurrentMasterBlock(ctx)
		if err != nil {
			log.Printf("getNextMasterBlockID err - %v", err)

			continue
		}
		err = s.loadShardBlocksBatch(ctx, masterBlock)
		if err != nil {
			log.Printf("loadShardBlocksBatch err - %v", err)

			continue
		}
	}
}

// Stop initiates graceful shutdown
func (s *ShardTracker) Stop() {
}

func (s *ShardTracker) getCurrentMasterBlock(ctx context.Context) (*ton.BlockIDExt, error) {
	for {
		masterBlock, err := s.tonApi.GetMasterchainInfo(ctx)
		if err != nil {
			// exit by context timeout
			return nil, err
		}
		if s.lastMasterBlock == nil {
			s.lastMasterBlock = masterBlock
			return masterBlock, nil
		}
		if masterBlock.SeqNo == s.lastMasterBlock.SeqNo {
			time.Sleep(time.Second * 30)
			continue
		}
		s.lastMasterBlock = masterBlock
		return masterBlock, nil
	}
}

func (s *ShardTracker) loadShardBlocksBatch(ctx context.Context, masterBlock *ton.BlockIDExt) error {
	var (
		blocksShardsInfo []*ton.BlockIDExt
		err              error
	)
	for {
		blocksShardsInfo, err = s.tonApi.GetBlockShardsInfo(ctx, masterBlock)
		if err != nil && isNotReadyError(err) { // TODO: clarify error type
			time.Sleep(time.Second)
			continue
		} else if err != nil {
			return err
		}
		break
	}
	err = s.getShardBlocks(ctx, filterByShard(blocksShardsInfo, s.shard))
	if err != nil {
		return err
	}

	return nil
}

func (s *ShardTracker) getShardBlocks(ctx context.Context, i *ton.BlockIDExt) error {
	var currentBlock *ton.BlockIDExt = i
	start := time.Now()

	var diff = int(i.SeqNo - s.lastKnownShardBlock.SeqNo)

	log.Printf("Shard tracker. Seqno diff: %v", diff)

	for {
		isKnown := (s.lastKnownShardBlock.Shard == currentBlock.Shard) && (s.lastKnownShardBlock.SeqNo == currentBlock.SeqNo)
		if isKnown {
			s.lastKnownShardBlock = i
			break
		}

		h, err := s.getShardBlocksHeader(ctx, currentBlock, s.shard)

		if err != nil {
			return err
		}

		s.blocksChan <- &h
		currentBlock = h.Parent
	}

	log.Printf("Shard tracker. Blocks processed: %v Elapsed time: %v sec", diff, time.Since(start).Seconds())

	return nil
}

func isInShard(blockShardPrefix uint64, shard byte) bool {
	if blockShardPrefix == 0 {
		log.Fatalf("invalid shard_prefix")
	}
	prefixLen := 64 - 1 - bits.TrailingZeros64(blockShardPrefix) // without one insignificant bit
	if prefixLen > 8 {
		log.Fatalf("more than 256 shards is not supported")
	}
	res := (uint64(shard) << (64 - 8)) ^ blockShardPrefix

	return bits.LeadingZeros64(res) >= prefixLen
}

func filterByShard(headers []*ton.BlockIDExt, shard byte) *ton.BlockIDExt {
	for _, h := range headers {
		if isInShard(uint64(h.Shard), shard) {
			return h
		}
	}
	log.Fatalf("must be at least one suitable shard block")
	return nil
}

func convertBlockToShardHeader(block *tlb.Block, info *ton.BlockIDExt, shard byte) (models.ShardBlockHeader, error) {
	parents, err := block.BlockInfo.GetParentBlocks()
	if err != nil {
		return models.ShardBlockHeader{}, nil
	}
	parent := filterByShard(parents, shard)
	return models.ShardBlockHeader{
		NotMaster:  block.BlockInfo.NotMaster,
		GenUtime:   block.BlockInfo.GenUtime,
		StartLt:    block.BlockInfo.StartLt,
		EndLt:      block.BlockInfo.EndLt,
		Parent:     parent,
		BlockIDExt: info,
	}, nil
}

// get shard block header for specific shard attribute with one parent
func (s *ShardTracker) getShardBlocksHeader(ctx context.Context, shardBlockInfo *ton.BlockIDExt, shard byte) (models.ShardBlockHeader, error) {
	var (
		err   error
		block *tlb.Block
	)
	for {
		block, err = s.tonApi.GetBlockData(ctx, shardBlockInfo)
		if err != nil && isNotReadyError(err) {
			continue
		} else if err != nil {
			return models.ShardBlockHeader{}, err
			// exit by context timeout
		}
		break
	}
	return convertBlockToShardHeader(block, shardBlockInfo, shard)
}

func isNotReadyError(err error) bool {
	return strings.Contains(err.Error(), ErrBlockNotApplied)
}
