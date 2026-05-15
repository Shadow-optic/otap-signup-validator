// Package bitweave — conflict adapter for FLR integration
//
// Maps the FLR's Lease model to bitweave's integer attribute space.
//
// Field mapping:
//   Wavelength.ChannelNum  → channelNum (uint32)
//   Wavelength.Band        → awgID (uint16)
//   OperatorID             → tenantID (uint16, hashed)
//   Wavelength.ChannelNum  → awgPort (1:1 placeholder)
//   (all leases exclusive) → exclusive = true
package bitweave

import (
	"sync"

	"github.com/otap/flr/internal/models"
)

type ConflictEngine struct {
	mu    sync.RWMutex
	index *LeaseIndex
	slots map[string]int
}

func NewConflictEngine() *ConflictEngine {
	return &ConflictEngine{
		index: NewLeaseIndex(),
		slots: make(map[string]int),
	}
}

func (ce *ConflictEngine) IndexLease(lease *models.Lease) error {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	if _, ok := ce.slots[lease.ID]; ok {
		return nil
	}

	slot, err := ce.index.Insert(
		lease.ID,
		leaseChannelNum(lease),
		leaseAwgID(lease),
		leaseTenantID(lease),
		leaseAwgPort(lease),
		leaseExclusive(lease),
	)
	if err != nil {
		return err
	}
	ce.slots[lease.ID] = slot
	return nil
}

func (ce *ConflictEngine) RemoveLease(leaseID string) {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	slot, ok := ce.slots[leaseID]
	if !ok {
		return
	}
	ce.index.Remove(slot)
	delete(ce.slots, leaseID)
}

func (ce *ConflictEngine) CheckLeaseConflicts(proposed *models.Lease, renewLeaseID string) []Conflict {
	ce.mu.RLock()
	defer ce.mu.RUnlock()

	excludeSlot := -1
	if renewLeaseID != "" {
		if s, ok := ce.slots[renewLeaseID]; ok {
			excludeSlot = s
		}
	}

	return ce.index.CheckConflicts(
		leaseChannelNum(proposed),
		leaseAwgID(proposed),
		leaseTenantID(proposed),
		leaseAwgPort(proposed),
		leaseExclusive(proposed), // proposedExclusive
		excludeSlot,
	)
}

func (ce *ConflictEngine) RebuildFrom(leases []*models.Lease) error {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	ce.index = NewLeaseIndex()
	ce.slots = make(map[string]int)

	for _, l := range leases {
		slot, err := ce.index.Insert(
			l.ID,
			leaseChannelNum(l),
			leaseAwgID(l),
			leaseTenantID(l),
			leaseAwgPort(l),
			leaseExclusive(l),
		)
		if err != nil {
			return err
		}
		ce.slots[l.ID] = slot
	}
	return nil
}

func (ce *ConflictEngine) LeaseCount() int {
	ce.mu.RLock()
	defer ce.mu.RUnlock()
	return ce.index.Count
}

func leaseChannelNum(l *models.Lease) uint32 {
	if l.Wavelength == nil {
		return 0
	}
	return uint32(l.Wavelength.ChannelNum)
}

func leaseAwgID(l *models.Lease) uint16 {
	if l.Wavelength == nil {
		return 0
	}
	return uint16(l.Wavelength.Band)
}

func leaseAwgPort(l *models.Lease) uint16 {
	if l.Wavelength == nil {
		return 0
	}
	return uint16(l.Wavelength.ChannelNum)
}

func leaseTenantID(l *models.Lease) uint16 {
	if l.OperatorID == "" {
		return 0
	}
	h := uint16(0)
	for _, c := range l.OperatorID {
		h = h*31 + uint16(c)
	}
	return h
}

func leaseExclusive(_ *models.Lease) bool {
	return true
}
