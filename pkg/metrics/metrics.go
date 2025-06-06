// Copyright 2019 ScyllaDB
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package metrics

import (
	"context"
	"net"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

var registerer = prometheus.NewRegistry()

var (
	executionTime = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "execution_time",
			Help:    "Time taken to execute a task.",
			Buckets: []float64{0.001, 0.01, 0.1, 0.5, 1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000, 20000, 30000},
		},
		[]string{"task"},
	)

	CQLRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cql_requests",
			Help: "How many CQL requests processed, partitioned by system and CQL query type aka 'method' (batch, delete, insert, update).",
		},
		[]string{"system", "method"},
	)

	CQLQueryTimeouts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cql_query_timeouts",
		},
		[]string{"cluster", "query_type"},
	)

	GoCQLConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cql_connections",
		},
		[]string{"cluster", "host"},
	)

	GoCQLConnectionsErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cql_connections_errors",
		},
		[]string{"cluster", "host", "error"},
	)

	GoCQLConnectTime = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cql_connect_time",
			Help:    "Time taken to establish a connection to the CQL server.",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10},
		},
		[]string{"cluster", "host"},
	)

	GoCQLQueries = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cql_queries",
		},
		[]string{"cluster", "host", "query_type"},
	)

	GoCQLQueryTime = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cql_query_time",
			Help:    "Time taken to execute a CQL query.",
			Buckets: []float64{0.001, 0.01, 0.1, 0.5, 1, 2, 5, 10, 20, 50, 100},
		},
		[]string{"cluster", "host", "query"},
	)

	GoCQLQueryErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cql_query_errors",
			Help: "Number of CQL query errors.",
		},
		[]string{"cluster", "host", "query", "error"},
	)

	GoCQLBatchQueries = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cql_batched_queries",
		},
		[]string{"cluster", "host", "query_type"},
	)

	GoCQLBatches = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cql_batches",
		},
		[]string{"cluster", "host"},
	)
	GeneratorEmittedValues = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "generated_emitted_values",
		},
		[]string{"table"},
	)

	GeneratorDroppedValues = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "generated_dropped_values",
		},
		[]string{"table", "type"},
	)

	GeneratorFilledPartitions = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "generated_filled_partitions",
		},
		[]string{"table"},
	)

	GeneratorPartitionSize = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "generated_partition_size",
		},
		[]string{"table"},
	)

	StalePartitions = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "stale_partitions",
		},
		[]string{"table"},
	)

	GeneratorBufferSize = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "generated_buffer_size",
		},
		[]string{"table"},
	)

	MemoryMetrics = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "memory_footprint",
		},
		[]string{"type", "context"},
	)

	FileSizeMetrics = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "file_size_bytes",
		},
		[]string{"file"},
	)
)

func init() {
	r := prometheus.WrapRegistererWithPrefix("gemini_", registerer)

	r.MustRegister(channelMetrics, executionTime)

	r.MustRegister(
		CQLRequests,
		CQLQueryTimeouts,
		GoCQLConnections,
		GoCQLConnectionsErrors,
		GoCQLConnectTime,
		GoCQLQueries,
		GoCQLQueryTime,
		GoCQLQueryErrors,
		GoCQLBatchQueries,
		GoCQLBatches,
		GeneratorEmittedValues,
		GeneratorFilledPartitions,
		GeneratorPartitionSize,
		GeneratorDroppedValues,
		GeneratorBufferSize,
		StalePartitions,
		MemoryMetrics,
		FileSizeMetrics,
	)

	r.MustRegister(
		collectors.NewGoCollector(
			collectors.WithGoCollectorRuntimeMetrics(),
		),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{
			PidFn: func() (int, error) {
				return os.Getpid(), nil
			},
		}),
		collectors.NewBuildInfoCollector(),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "go_goroutines_count",
			Help: "Number of goroutines currently active.",
		}, func() float64 {
			return float64(runtime.NumGoroutine())
		}),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "go_gc_total_count",
			Help: "Total number of garbage collections.",
		}, func() float64 {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			return float64(m.NumGC)
		}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "go_last_gc_duration_microseconds",
			Help: "Duration of the last GC cycle.",
		}, func() float64 {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			return float64(m.PauseNs[(m.NumGC+255)%256]) / 1e6
		}),
	)
}

func StartMetricsServer(ctx context.Context, bind string, logger *zap.Logger) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.InstrumentMetricHandler(
		registerer, promhttp.HandlerFor(registerer, promhttp.HandlerOpts{
			EnableOpenMetrics: true,
			Registry:          registerer,
			OfferedCompressions: []promhttp.Compression{
				promhttp.Zstd,
				promhttp.Gzip,
				promhttp.Identity,
			},
		}),
	))

	server := &http.Server{
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
		WriteTimeout: 1 * time.Minute,
		Handler:      mux,
		Addr:         bind,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Failed to start Metrics server", zap.String("bind", bind), zap.Error(err))
		}
	}()
}

type RunningTime struct {
	start time.Time
	task  string
}

func ExecutionTimeStart(task string) RunningTime {
	return RunningTime{
		start: time.Now(),
		task:  task,
	}
}

func (r RunningTime) Record() {
	executionTime.
		WithLabelValues(r.task).
		Observe(float64(time.Since(r.start).Microseconds()))
}

func ExecutionTime(task string, callback func()) {
	start := time.Now()

	callback()
	executionTime.
		WithLabelValues(task).
		Observe(float64(time.Since(start).Nanoseconds()) / 1e3)
}
