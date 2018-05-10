/*
Copyright 2018 The Kubernetes Authors.

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

package metrics

import (
	"fmt"
	"math"

	esUtil "github.com/kubernetes-incubator/external-storage/lib/util"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	// LocalVolumeProvisionerSubsystem is prometheus subsystem name.
	LocalVolumeProvisionerSubsystem = "local_volume_provisioner"
	// APIServerRequestCreate represents metrics related to create resource request.
	APIServerRequestCreate = "create"
	// APIServerRequestDelete represents metrics related to delete resource request.
	APIServerRequestDelete = "delete"
	// DeleteTypeProcess represents metrics releated deletion in process.
	DeleteTypeProcess = "process"
	// DeleteTypeJob represents metrics releated deletion by job.
	DeleteTypeJob = "job"
)

var (
	// PersistentVolumeDiscoveryTotal is used to collect accumulated count of persistent volumes discoveried.
	PersistentVolumeDiscoveryTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: LocalVolumeProvisionerSubsystem,
			Name:      "persistentvolume_discovery_total",
			Help:      "Total number of persistent volumes discoveried. Broken down by persistent volume mode.",
		},
		[]string{"mode"},
	)
	// PersistentVolumeDiscoveryDurationSeconds is used to collect latency in seconds to discovery persistent volumes.
	PersistentVolumeDiscoveryDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem: LocalVolumeProvisionerSubsystem,
			Name:      "persistentvolume_discovery_duration_seconds",
			Help:      "Latency in seconds to discovery persistent volumes. Broken down by persistent volume mode.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"mode"},
	)
	// PersistentVolumeDeleteTotal is used to collect accumulated count of persistent volumes deleted.
	PersistentVolumeDeleteTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: LocalVolumeProvisionerSubsystem,
			Name:      "persistentvolume_delete_total",
			Help:      "Total number of persistent volumes deleteed. Broken down by persistent volume mode, delete type (process or job).",
		},
		[]string{"mode", "type"},
	)
	// PersistentVolumeDeleteFailedTotal is used to collect accumulated count of persistent volume delete failed attempts.
	PersistentVolumeDeleteFailedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: LocalVolumeProvisionerSubsystem,
			Name:      "persistentvolume_delete_failed_total",
			Help:      "Total number of persistent volume delete failed attempts. Broken down by persistent volume mode, delete type (process or job).",
		},
		[]string{"mode", "type"},
	)
	// PersistentVolumeDeleteDurationSeconds is used to collect latency in seconds to delete persistent volumes.
	PersistentVolumeDeleteDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem: LocalVolumeProvisionerSubsystem,
			Name:      "persistentvolume_delete_duration_seconds",
			Help:      "Latency in seconds to delete persistent volumes. Broken down by persistent volume mode, delete type (process or job), capacity and cleanup_command.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"mode", "type", "capacity", "cleanup_command"},
	)
	// APIServerRequestsTotal is used to collect accumulated count of apiserver requests.
	APIServerRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: LocalVolumeProvisionerSubsystem,
			Name:      "apiserver_requests_total",
			Help:      "Total number of apiserver requests. Broken down by method.",
		},
		[]string{"method"},
	)
	// APIServerRequestsFailedTotal is used to collect accumulated count of apiserver requests failed.
	APIServerRequestsFailedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: LocalVolumeProvisionerSubsystem,
			Name:      "apiserver_requests_failed_total",
			Help:      "Total number of apiserver requests failed. Broken down by method.",
		},
		[]string{"method"},
	)
	// APIServerRequestsDurationSeconds is used to collect latency in seconds of apiserver requests.
	APIServerRequestsDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem: LocalVolumeProvisionerSubsystem,
			Name:      "apiserver_requests_duration_seconds",
			Help:      "Latency in seconds of apiserver requests. Broken down by method.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"method"},
	)
)

// CapacityBreakDown breaks capacity down into every 500G, e.g.
//	[0]: 0G
//	(0, 500G]: 500G
//	(500G, 1000G]: 1000G
//	(1000G, 1500G]: 1500G
func CapacityBreakDown(capacityBytes int64) string {
	n := int64(math.Ceil(float64(capacityBytes) / float64(500*esUtil.GiB)))
	return fmt.Sprintf("%dG", n*500)
}
