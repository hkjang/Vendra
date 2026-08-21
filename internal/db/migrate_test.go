package db

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
)

// TestConcurrentMigrateIsSerialised starts several pools at once, the way a
// rolling deploy starts several replicas. Without the advisory lock they race
// inside the catalog and fail with duplicate type/relation errors.
//
// It needs an EMPTY database, so it is opt-in:
//
//	VENDRA_TEST_MIGRATE_DSN=postgres://... go test ./internal/db/
func TestConcurrentMigrateIsSerialised(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("VENDRA_TEST_MIGRATE_DSN"))
	if dsn == "" {
		t.Skip("set VENDRA_TEST_MIGRATE_DSN to an empty database to run migration tests")
	}
	const replicas = 6
	errs := make([]error, replicas)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pool, err := Open(context.Background(), dsn)
			if err != nil {
				errs[i] = err
				return
			}
			pool.Close()
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("replica %d failed to migrate: %v", i, err)
		}
	}
}
