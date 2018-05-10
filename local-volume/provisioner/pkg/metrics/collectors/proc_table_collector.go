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

package collectors

import (
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/deleter"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	procTableRunning = prometheus.NewDesc(
		prometheus.BuildFQName("", metrics.LocalVolumeProvisionerSubsystem, "proctable_running"),
		"Number of running operations in proctable.",
		[]string{}, nil,
	)
	procTableSucceeded = prometheus.NewDesc(
		prometheus.BuildFQName("", metrics.LocalVolumeProvisionerSubsystem, "proctable_succeeded"),
		"Number of succeeded operations in proctable.",
		[]string{}, nil,
	)
	procTableFailed = prometheus.NewDesc(
		prometheus.BuildFQName("", metrics.LocalVolumeProvisionerSubsystem, "proctable_failed"),
		"Number of failed operations in proctable.",
		[]string{}, nil,
	)
)

type procTableCollector struct {
	procTable deleter.ProcTable
}

// NewProcTableCollector creates a process table
func NewProcTableCollector(procTable deleter.ProcTable) prometheus.Collector {
	return &procTableCollector{procTable: procTable}
}

// Describe implements the prometheus.Collector interface.
func (collector *procTableCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- procTableRunning
	ch <- procTableSucceeded
	ch <- procTableFailed
}

// Collect implements the prometheus.Collector interface.
func (collector *procTableCollector) Collect(ch chan<- prometheus.Metric) {
	stats := collector.procTable.Stats()
	addGauge := func(desc *prometheus.Desc, v float64, lv ...string) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, v, lv...)
	}
	addGauge(procTableRunning, float64(stats.Running))
	addGauge(procTableSucceeded, float64(stats.Succeeded))
	addGauge(procTableFailed, float64(stats.Failed))
}
