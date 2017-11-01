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
	MarkDone(pvName string)
}

var _ ProcTable = &ProcTableImpl{}

// ProcEntry represents an entry in the proc table
type ProcEntry struct {
	StartTime time.Time
}

// ProcTableImpl Implementation of BLockCleaner interface
type ProcTableImpl struct {
	mutex     sync.Mutex
	procTable map[string]ProcEntry
}

// NewProcTable returns a BlockCleaner
func NewProcTable() *ProcTableImpl {
	return &ProcTableImpl{procTable: make(map[string]ProcEntry)}
}

// IsRunning Check if cleanup process is still running
func (v *ProcTableImpl) IsRunning(pvName string) bool {
	v.mutex.Lock()
	defer v.mutex.Unlock()
	_, ok := v.procTable[pvName]
	return ok
}

// IsEmpty Check if any cleanup process is running
func (v *ProcTableImpl) IsEmpty() bool {
	v.mutex.Lock()
	defer v.mutex.Unlock()
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
	v.procTable[pvName] = ProcEntry{StartTime: time.Now()}
	return nil
}

// MarkDone Indicate the process is no longer running or being tracked.
func (v *ProcTableImpl) MarkDone(pvName string) {
	v.mutex.Lock()
	defer v.mutex.Unlock()
	delete(v.procTable, pvName)
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

// MarkDone Indicate the process is no longer running or being tracked.
func (f *FakeProcTableImpl) MarkDone(pvName string) {
	f.MarkDoneCount++
	f.realTable.MarkDone(pvName)
}
