//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// signalingEconomicsRepository reports each persisted sample so the lifecycle
// test can observe the background sampler without sleeping.
type signalingEconomicsRepository struct {
	economicsRepositoryStub
	sampled chan struct{}
}

func (r *signalingEconomicsRepository) UpsertSample(_ context.Context, _ AccountEconomicsSample) error {
	select {
	case r.sampled <- struct{}{}:
	default:
	}
	return nil
}

func TestAccountEconomicsService_StopEndsBackgroundSampler(t *testing.T) {
	repo := &signalingEconomicsRepository{sampled: make(chan struct{}, 1)}
	svc := NewAccountEconomicsService(&economicsAccountReaderStub{}, repo, NewAccountCostLossService(&economicsCostLossRepositoryStub{}))

	svc.Start()
	select {
	case <-repo.sampled:
	case <-time.After(5 * time.Second):
		t.Fatal("the sampler did not record its initial sample")
	}

	svc.Stop()
	svc.Stop() // cleanup may run more than once; Stop must stay idempotent.

	select {
	case <-svc.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the sampler goroutine kept running after Stop")
	}
}

func TestAccountEconomicsService_StopWithoutStartIsSafe(t *testing.T) {
	svc := NewAccountEconomicsService(nil, nil, nil)
	require.NotPanics(t, svc.Stop)

	var nilService *AccountEconomicsService
	require.NotPanics(t, nilService.Stop)
}
