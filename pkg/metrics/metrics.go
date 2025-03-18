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
	"time"

	"github.com/pkg/errors"
	"go.uber.org/zap"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var CQLRequests = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "gemini_cql_requests",
		Help: "How many CQL requests processed, partitioned by system and CQL query type aka 'method' (batch, delete, insert, update).",
	},
	[]string{"system", "method"},
)

func StartMetricsServer(ctx context.Context, bind string, logger *zap.Logger) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

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
