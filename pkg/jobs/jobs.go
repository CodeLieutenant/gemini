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

package jobs

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/pkg/errors"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/scylladb/gemini/pkg/generators"
	"github.com/scylladb/gemini/pkg/status"
	"github.com/scylladb/gemini/pkg/stop"
	"github.com/scylladb/gemini/pkg/store"
	"github.com/scylladb/gemini/pkg/typedef"
)

const (
	WriteMode  = "write"
	ReadMode   = "read"
	MixedMode  = "mixed"
	WarmupMode = "warmup"
)

type List struct {
	name     string
	modes    []string
	duration time.Duration
	workers  uint64
}

var ErrNoStatement = errors.New("no statement generated")

type Worker interface {
	Name() string
	Do(context.Context) error
}

func ListFromMode(mode string, duration time.Duration, workers uint64) List {
	var modes []string
	switch mode {
	case MixedMode:
		modes = []string{WriteMode, ReadMode}
	case WriteMode:
		modes = []string{WriteMode}
	case ReadMode:
		modes = []string{ReadMode}
	case WarmupMode:
		modes = []string{WarmupMode}
	}

	return List{
		modes:    modes,
		duration: duration,
		workers:  workers,
	}
}

func (l List) Run(
	base context.Context,
	schema *typedef.Schema,
	schemaConfig typedef.SchemaConfig,
	s store.Store,
	gens *generators.Generators,
	globalStatus *status.GlobalStatus,
	logger *zap.Logger,
	stopFlag *stop.Flag,
	src *rand.ChaCha8,
) error {
	logger = logger.Named(l.name)
	ctx, cancel := context.WithDeadline(base, time.Now().Add(l.duration))
	g, gCtx := errgroup.WithContext(ctx)

	logger.Info("start jobs")
	defer func() {
		logger.Info("stop jobs")
		cancel()
	}()

	for _, table := range schema.Tables {
		generator := gens.Get(table)

		for _, mode := range l.modes {
			switch mode {
			case WriteMode, WarmupMode:
				for range l.workers {
					newSrc := [32]byte{}
					_, _ = src.Read(newSrc[:])

					warmup := mode == WarmupMode
					g.Go(func() error {
						return NewMutation(
							schema,
							schemaConfig,
							table,
							generator,
							globalStatus,
							stopFlag,
							s,
							!warmup,
							newSrc,
						).Do(gCtx)
					})
				}
			case ReadMode:
				for range l.workers {
					newSrc := [32]byte{}
					_, _ = src.Read(newSrc[:])

					g.Go(func() error {
						return NewValidation(
							schema.Keyspace.Name,
							table,
							schemaConfig,
							generator,
							globalStatus,
							stopFlag,
							s,
							newSrc,
						).Do(gCtx)
					})
				}
			}
		}
	}

	return g.Wait()
}

//nolint
// mutationJob continuously applies mutations against the database
// for as long as the pump is active.
//func mutationJob(ctx context.Context, stmtGen *statements.Generator, globalStatus *status.GlobalStatus, logger *zap.Logger, stopFlag *stop.Flag) error {
//	for !stopFlag.IsHardOrSoft() {
//		metrics.ExecutionTime("mutation_job", func() {
//			if schemaCfg.CQLFeature == typedef.CQL_FEATURE_ALL && r.IntN(1000000)%100000 == 0 {
//				_ = ddl(ctx, globalStatus, logger)
//				return
//			}
//
//			_ = mutation(ctx, globalStatus, true, logger)
//		})
//
//		if globalStatus.HasErrors() {
//			stopFlag.SetSoft(true)
//			return nil
//		}
//	}
//
//	return nil
//}
