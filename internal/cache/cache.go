package cache

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/fullsend-ai/metrics-executor/internal/logging"
	"github.com/fullsend-ai/metrics-executor/internal/store"
)

type WorkflowCache struct {
	store     *store.MySQLStore
	mu        sync.RWMutex
	workflows map[string]*store.Workflow
	versions  map[string]versionInfo
	stop      chan struct{}
	done      chan struct{}
}

type versionInfo struct {
	version           int
	productionVersion int
	queryCount        int
}

func New(s *store.MySQLStore) *WorkflowCache {
	return &WorkflowCache{
		store:     s,
		workflows: make(map[string]*store.Workflow),
		versions:  make(map[string]versionInfo),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
}

func (c *WorkflowCache) GetWorkflowByName(_ context.Context, name string) (*store.Workflow, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.workflows[name], nil
}

func (c *WorkflowCache) Start(interval time.Duration) {
	c.Refresh(context.Background())
	go func() {
		defer close(c.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.Refresh(context.Background())
			case <-c.stop:
				return
			}
		}
	}()
}

func (c *WorkflowCache) Stop() {
	close(c.stop)
	<-c.done
}

func (c *WorkflowCache) Refresh(ctx context.Context) {
	logger := logging.FromContext(ctx)

	summaries, err := c.store.ListPublishedWorkflows(ctx)
	if err != nil {
		logger.ErrorContext(ctx, "cache refresh: failed to list workflows", "error", err)
		return
	}

	newWorkflows := make(map[string]*store.Workflow, len(summaries))
	newVersions := make(map[string]versionInfo, len(summaries))

	for _, s := range summaries {
		wf, err := c.store.GetWorkflowByName(ctx, s.Name)
		if err != nil {
			logger.ErrorContext(ctx, "cache refresh: failed to load workflow", "workflow", s.Name, "error", err)
			continue
		}
		if wf != nil {
			newWorkflows[s.Name] = wf
			newVersions[s.Name] = versionInfo{
				version:           s.Version,
				productionVersion: s.ProductionVersion,
				queryCount:        s.QueryCount,
			}
		}
	}

	c.mu.Lock()
	old := c.versions
	c.workflows = newWorkflows
	c.versions = newVersions
	c.mu.Unlock()

	for name, v := range newVersions {
		if _, existed := old[name]; !existed {
			logger.InfoContext(ctx, "workflow added", "workflow", name, "serving", servingLabel(v), "query_count", v.queryCount)
		} else if old[name] != v {
			logger.InfoContext(ctx, "workflow updated", "workflow", name, "serving", servingLabel(v), "query_count", v.queryCount)
		}
	}
	for name := range old {
		if _, exists := newVersions[name]; !exists {
			logger.InfoContext(ctx, "workflow removed", "workflow", name)
		}
	}
}

func servingLabel(v versionInfo) string {
	if v.productionVersion > 0 {
		return fmt.Sprintf("v%d", v.productionVersion)
	}
	return "latest"
}
