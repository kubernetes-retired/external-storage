/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package deleter

import (
	"fmt"
	"sync"
	"time"
)

// ProcTable Interface for tracking running processes
type ProcTable interface {
	// CleanupBlockPV deletes block based PV
	IsRunning(pvName string) bool
	IsEmpty() bool
	MarkRunning(pvName string) error
	MarkFailed(pvName string) error
	MarkSucceeded(pvName string) error
	RemoveEntry(pvName string) (CleanupState, *time.Time, error)
	Stats() ProcTableStats
}

// ProcTableStats represents stats of ProcTable.
type ProcTableStats struct {
	Running   int
	Succeeded int
	Failed    int
}

var _ ProcTable = &ProcTableImpl{}

// ProcEntry represents an entry in the proc table
type ProcEntry struct {
	StartTime time.Time
	Status    CleanupState
}

// ProcTableImpl Implementation of BLockCleaner interface
type ProcTableImpl struct {
	mutex     sync.RWMutex
	procTable map[string]ProcEntry
	succeeded int
	failed    int
}

// NewProcTable returns a BlockCleaner
func NewProcTable() *ProcTableImpl {
	return &ProcTableImpl{procTable: make(map[string]ProcEntry)}
}

// IsRunning Check if cleanup process is still running
func (v *ProcTableImpl) IsRunning(pvName string) bool {
	v.mutex.RLock()
	defer v.mutex.RUnlock()

	if entry, ok := v.procTable[pvName]; !ok || entry.Status != CSRunning {
		return false
	}

	return true
}

// IsEmpty Check if any cleanup process is running
func (v *ProcTableImpl) IsEmpty() bool {
	v.mutex.RLock()
	defer v.mutex.RUnlock()
	return len(v.procTable) == 0
}

// MarkRunning Indicate that process is running.
func (v *ProcTableImpl) MarkRunning(pvName string) error {
	v.mutex.Lock()
	defer v.mutex.Unlock()
	_, ok := v.procTable[pvName]
	if ok {
		return fmt.Errorf("Failed to mark running of %q as it is already running, should never happen", pvName)
	}
	v.procTable[pvName] = ProcEntry{StartTime: time.Now(), Status: CSRunning}
	return nil
}

// MarkFailed Indicate the process has failed in its run.
func (v *ProcTableImpl) MarkFailed(pvName string) error {
	return v.markStatus(pvName, CSFailed)
}

// MarkSucceeded Indicate the process has succeeded in its run.
func (v *ProcTableImpl) MarkSucceeded(pvName string) error {
	return v.markStatus(pvName, CSSucceeded)
}

func (v *ProcTableImpl) markStatus(pvName string, status CleanupState) error {
	v.mutex.Lock()
	defer v.mutex.Unlock()
	defer func() {
		if status == CSSucceeded {
			v.succeeded++
		} else if status == CSFailed {
			v.failed++
		}
	}()
	entry, ok := v.procTable[pvName]
	if !ok {
		return fmt.Errorf("failed to mark status %d for pv %q as it is not present in proctable", status, pvName)
	}
	// Indicate that the process is done.
	entry.Status = status
	v.procTable[pvName] = entry
	return nil
}

// RemoveEntry Removes proctable entry and returns final state and start time of cleanup.
// Must only be called and cleanup that has ended, else error is returned.
func (v *ProcTableImpl) RemoveEntry(pvName string) (CleanupState, *time.Time, error) {
	v.mutex.Lock()
	defer v.mutex.Unlock()
	entry, ok := v.procTable[pvName]
	if !ok {
		return CSNotFound, nil, nil
	}
	if entry.Status == CSRunning {
		return CSUnknown, nil, fmt.Errorf("cannot remove proctable entry for %q when it is still running", pvName)
	}
	if entry.Status == CSUnknown {
		return CSUnknown, nil, fmt.Errorf("proctable entry for %q in unexpected unknown state", pvName)
	}
	delete(v.procTable, pvName)
	return entry.Status, &entry.StartTime, nil
}

// Stats returns stats of ProcTable.
func (v *ProcTableImpl) Stats() ProcTableStats {
	v.mutex.RLock()
	defer v.mutex.RUnlock()
	running := 0
	for _, entry := range v.procTable {
		if entry.Status == CSRunning {
			running++
		}
	}
	return ProcTableStats{
		Running:   running,
		Succeeded: v.succeeded,
		Failed:    v.failed,
	}
}

// FakeProcTableImpl creates a mock proc table that enables testing.
type FakeProcTableImpl struct {
	realTable ProcTable
	// IsRunningCount keeps count of number of times IsRunning() was called
	IsRunningCount int
	// MarkRunningCount keeps count of number of times MarkRunning() was called
	MarkRunningCount int
	// MarkDoneCount keeps count of number of times MarkDone() was called
	MarkDoneCount int
	// RemoveCount keeps count of number of times Remove() was called
	RemoveCount int
	// StatsCount keeps count of number of times Stats() was called
	StatsCount int
}

var _ ProcTable = &FakeProcTableImpl{}

// NewFakeProcTable returns a BlockCleaner
func NewFakeProcTable() *FakeProcTableImpl {
	return &FakeProcTableImpl{realTable: NewProcTable()}
}

// IsRunning Check if cleanup process is still running
func (f *FakeProcTableImpl) IsRunning(pvName string) bool {
	f.IsRunningCount++
	return f.realTable.IsRunning(pvName)
}

// IsEmpty Check if any cleanup process is running
func (f *FakeProcTableImpl) IsEmpty() bool {
	return f.realTable.IsEmpty()
}

// MarkRunning Indicate that process is running.
func (f *FakeProcTableImpl) MarkRunning(pvName string) error {
	f.MarkRunningCount++
	return f.realTable.MarkRunning(pvName)
}

// MarkFailed Indicate the process has failed.
func (f *FakeProcTableImpl) MarkFailed(pvName string) error {
	f.MarkDoneCount++
	return f.realTable.MarkFailed(pvName)
}

// MarkSucceeded Indicate the process has succeeded.
func (f *FakeProcTableImpl) MarkSucceeded(pvName string) error {
	f.MarkDoneCount++
	return f.realTable.MarkSucceeded(pvName)
}

// RemoveEntry removes the entry from the proc table.
func (f *FakeProcTableImpl) RemoveEntry(pvName string) (CleanupState, *time.Time, error) {
	f.RemoveCount++
	return f.realTable.RemoveEntry(pvName)
}

// Stats returns stats of ProcTable.
func (f *FakeProcTableImpl) Stats() ProcTableStats {
	f.StatsCount++
	return f.realTable.Stats()
}
