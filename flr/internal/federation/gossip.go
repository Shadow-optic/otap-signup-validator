package federation

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// gossipLoop runs the periodic gossip protocol until the context is cancelled.
// It performs rounds of: sync with all peers, detect conflicts, push commitments.
func (m *Manager) gossipLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Do an initial round immediately
	if err := m.gossipRound(); err != nil {
		slog.Error("initial gossip round failed", "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			slog.Info("gossip loop stopped", "reason", ctx.Err())
			return
		case <-ticker.C:
			if err := m.gossipRound(); err != nil {
				slog.Error("gossip round failed", "error", err)
			}
		}
	}
}

// gossipRound performs one complete gossip round:
//  1. Sync with all registered peer operators
//  2. Detect cross-operator conflicts
//  3. Push our latest commitment to all peers
func (m *Manager) gossipRound() error {
	start := time.Now().UTC()

	// Get snapshot of active operators
	m.mu.RLock()
	ops := make([]*operatorRef, 0, len(m.operators))
	for id, op := range m.operators {
		if op.Status == 1 { // OperatorStatusActive
			ops = append(ops, &operatorRef{
				id:       id,
				enndoint: op.Endpoint,
			})
		}
	}
	m.mu.RUnlock()

	if len(ops) == 0 {
		return nil // No peers to gossip with
	}

	var syncErrors []error

	// Step 1: Sync with all peers concurrently
	syncResults := make(chan error, len(ops))
	for _, opRef := range ops {
		go func(opID string) {
			syncResults <- m.SyncWithOperator(opID)
		}(opRef.id)
	}

	for range ops {
		if err := <-syncResults; err != nil {
			syncErrors = append(syncErrors, err)
		}
	}

	if len(syncErrors) > 0 {
		slog.Warn("some sync operations failed",
			"failed", len(syncErrors),
			"total", len(ops),
		)
	}

	// Step 2: Detect conflicts across all peers
	conflicts, err := m.DetectConflicts()
	if err != nil {
		slog.Error("conflict detection failed", "error", err)
	} else if len(conflicts) > 0 {
		slog.Warn("cross-operator conflicts detected",
			"count", len(conflicts),
		)
		for _, poi := range conflicts {
			slog.Warn("conflict",
				"type", poi.Type.String(),
				"lease_a", poi.LeaseA.ID,
			)
		}
	}

	// Step 3: Push our latest commitment to all peers
	commitment, err := m.registry.GetLatestCommitment()
	if err != nil {
		slog.Error("get latest commitment failed", "error", err)
	} else if commitment != nil {
		if err := m.PushCommitmentToAll(commitment); err != nil {
			slog.Error("push commitment failed", "error", err)
		}
	}

	elapsed := time.Since(start)
	slog.Info("gossip round completed",
		"peers", len(ops),
		"sync_errors", len(syncErrors),
		"conflicts", len(conflicts),
		"duration_ms", elapsed.Milliseconds(),
	)

	if len(syncErrors) == len(ops) && len(ops) > 0 {
		return fmt.Errorf("all %d sync operations failed", len(ops))
	}

	return nil
}

// operatorRef is a lightweight reference used during gossip rounds.
type operatorRef struct {
	id       string
	enndoint string
}

// endpoint returns the operator's endpoint URL.
func (o *operatorRef) endpoint() string {
	return o.enndoint
}
