package mcpserver

import (
	"context"
	"sync"
	"time"

	"github.com/mylocalgpt/local-docs-mcp/internal/config"
	"github.com/mylocalgpt/local-docs-mcp/internal/indexer"
)

// BlockingIndexer is a test fake that lets a test pause IndexRepo and
// IndexLocalPath at a deterministic point so race scenarios are reproducible.
//
// Each call records its arguments in Calls() and then blocks on a per-alias
// release channel. Tests prime that channel via Block(alias) before issuing
// the request, then close the returned channel to unblock the call.
//
// If no channel is primed for the alias, the call returns immediately
// (useful for the "non-blocked alias keeps working" assertions).
type BlockingIndexer struct {
	mu     sync.Mutex
	blocks map[string]chan struct{}
	calls  []IndexerCall

	// Optional hook that produces a custom result for the next call to a
	// given alias. Keyed by alias; consumed (deleted) on use.
	results map[string]*indexer.IndexResult
}

// IndexerCall records one observed invocation of the BlockingIndexer.
type IndexerCall struct {
	Alias string
	URL   string
	Paths []string
	Force bool
	Local bool // true if the call was IndexLocalPath
}

// NewBlockingIndexer constructs an empty BlockingIndexer ready for use.
func NewBlockingIndexer() *BlockingIndexer {
	return &BlockingIndexer{
		blocks:  make(map[string]chan struct{}),
		results: make(map[string]*indexer.IndexResult),
	}
}

// Block primes a release channel for the given alias. Returns the channel;
// close it (or send on it) to unblock the call. Each Block call replaces any
// prior block for the same alias.
func (b *BlockingIndexer) Block(alias string) chan struct{} {
	ch := make(chan struct{})
	b.mu.Lock()
	b.blocks[alias] = ch
	b.mu.Unlock()
	return ch
}

// SetResult queues a custom *indexer.IndexResult to return on the next call
// for alias. Cleared after one use. If unset, calls return a synthetic
// success with DocsIndexed=1.
func (b *BlockingIndexer) SetResult(alias string, r *indexer.IndexResult) {
	b.mu.Lock()
	b.results[alias] = r
	b.mu.Unlock()
}

// Calls returns a snapshot of all calls observed so far.
func (b *BlockingIndexer) Calls() []IndexerCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]IndexerCall, len(b.calls))
	copy(out, b.calls)
	return out
}

// CallsFor returns the subset of calls for a specific alias.
func (b *BlockingIndexer) CallsFor(alias string) []IndexerCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []IndexerCall
	for _, c := range b.calls {
		if c.Alias == alias {
			out = append(out, c)
		}
	}
	return out
}

// IndexRepo records the call, then blocks on the per-alias release channel
// (if one was primed) before returning. ctx is honoured: if it cancels while
// waiting on the block channel, recordAndWait returns ctx.Err() and IndexRepo
// surfaces it so runJob sees context.Canceled.
func (b *BlockingIndexer) IndexRepo(ctx context.Context, cfg config.RepoConfig, force bool) (*indexer.IndexResult, error) {
	if err := b.recordAndWait(ctx, IndexerCall{Alias: cfg.Alias, URL: cfg.URL, Paths: cfg.Paths, Force: force, Local: false}); err != nil {
		return nil, err
	}
	return b.takeResult(cfg.Alias), nil
}

// IndexLocalPath records the call, then blocks on the per-alias release
// channel (if one was primed) before returning. ctx is honoured the same way
// as IndexRepo.
func (b *BlockingIndexer) IndexLocalPath(ctx context.Context, alias, path string) (*indexer.IndexResult, error) {
	if err := b.recordAndWait(ctx, IndexerCall{Alias: alias, URL: path, Local: true}); err != nil {
		return nil, err
	}
	return b.takeResult(alias), nil
}

func (b *BlockingIndexer) recordAndWait(ctx context.Context, c IndexerCall) error {
	b.mu.Lock()
	b.calls = append(b.calls, c)
	ch := b.blocks[c.Alias]
	delete(b.blocks, c.Alias) // one-shot; subsequent calls do not block
	b.mu.Unlock()

	if ch != nil {
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (b *BlockingIndexer) takeResult(alias string) *indexer.IndexResult {
	b.mu.Lock()
	r, ok := b.results[alias]
	if ok {
		delete(b.results, alias)
	}
	b.mu.Unlock()
	if r != nil {
		return r
	}
	return &indexer.IndexResult{Repo: alias, DocsIndexed: 1, Duration: time.Millisecond}
}
