package cache

import (
	"context"
	"testing"

	"github.com/fullsend-ai/metrics-executor/internal/store"
)

func TestServingLabel_ProductionVersion(t *testing.T) {
	v := versionInfo{version: 1, productionVersion: 2, queryCount: 3}
	label := servingLabel(v)
	if label != "v2" {
		t.Errorf("expected 'v2', got '%s'", label)
	}
}

func TestServingLabel_Latest(t *testing.T) {
	v := versionInfo{version: 1, productionVersion: 0, queryCount: 3}
	label := servingLabel(v)
	if label != "latest" {
		t.Errorf("expected 'latest', got '%s'", label)
	}
}

func TestVersionInfoEquality(t *testing.T) {
	v1 := versionInfo{version: 1, productionVersion: 2, queryCount: 3}
	v2 := versionInfo{version: 1, productionVersion: 2, queryCount: 3}
	v3 := versionInfo{version: 2, productionVersion: 2, queryCount: 3}

	if v1 != v2 {
		t.Error("expected v1 == v2")
	}
	if v1 == v3 {
		t.Error("expected v1 != v3")
	}
}

func TestWorkflowCache_BasicOperations(t *testing.T) {
	// Test basic cache operations without database
	cache := &WorkflowCache{
		store:     nil, // Not used for basic tests
		workflows: make(map[string]*store.Workflow),
		versions:  make(map[string]versionInfo),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}

	// Add a workflow directly to the cache
	wf := &store.Workflow{
		ID:           1,
		Name:         "test-wf",
		InputScript:  "",
		OutputScript: "",
		Queries:      []store.Query{{Stage: 0, Label: "q1", SQLText: "SELECT 1"}},
	}
	cache.workflows["test-wf"] = wf
	cache.versions["test-wf"] = versionInfo{version: 1, productionVersion: 0, queryCount: 1}

	// Test GetWorkflowByName
	ctx := context.Background()
	result, err := cache.GetWorkflowByName(ctx, "test-wf")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Error("expected workflow to be returned")
	}
	if result.Name != "test-wf" {
		t.Errorf("expected 'test-wf', got '%s'", result.Name)
	}

	// Test non-existent workflow
	result, err = cache.GetWorkflowByName(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil for non-existent workflow")
	}
}
