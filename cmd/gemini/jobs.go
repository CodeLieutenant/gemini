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

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/scylladb/gemini/pkg/joberror"
	"github.com/scylladb/gemini/pkg/status"

	"go.uber.org/zap"
	"golang.org/x/exp/rand"
	"golang.org/x/sync/errgroup"

	"github.com/scylladb/gemini"
	"github.com/scylladb/gemini/store"
)

// MutationJob continuously applies mutations against the database
// for as long as the pump is active.
func MutationJob(
	ctx context.Context,
	pump <-chan heartBeat,
	schema *gemini.Schema,
	schemaCfg gemini.SchemaConfig,
	table *gemini.Table,
	s store.Store,
	r *rand.Rand,
	p *gemini.PartitionRangeConfig,
	g *gemini.Generator,
	globalStatus *status.GlobalStatus,
	_ string,
	_ time.Duration,
	logger *zap.Logger,
) error {
	schemaConfig := &schemaCfg
	logger = logger.Named("mutation_job")
	logger.Info("starting mutation loop")
	defer func() {
		logger.Info("ending mutation loop")
	}()
	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			logger.Debug("mutation job terminated")
			return ctx.Err()
		case hb := <-pump:
			hb.await()
			ind := r.Intn(1000000)
			if ind%100000 == 0 {
				if err := ddl(ctx, schema, schemaConfig, table, s, r, p, globalStatus, logger); err != nil {
					return err
				}
			} else {
				if err := mutation(ctx, schema, schemaConfig, table, s, r, p, g, globalStatus, true, logger); err != nil {
					return err
				}
			}
			if failFast && globalStatus.HasErrors() {
				return errors.New("encounter error, failsafe is set")
			}
		}
	}
}

// ValidationJob continuously applies validations against the database
// for as long as the pump is active.
func ValidationJob(
	ctx context.Context,
	pump <-chan heartBeat,
	schema *gemini.Schema,
	schemaCfg gemini.SchemaConfig,
	table *gemini.Table,
	s store.Store,
	r *rand.Rand,
	p *gemini.PartitionRangeConfig,
	g *gemini.Generator,
	globalStatus *status.GlobalStatus,
	_ string,
	_ time.Duration,
	logger *zap.Logger,
) error {
	schemaConfig := &schemaCfg
	logger = logger.Named("validation_job")
	logger.Info("starting validation loop")
	defer func() {
		logger.Info("ending validation loop")
	}()

	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case hb := <-pump:
			hb.await()
			stmt := schema.GenCheckStmt(table, g, r, p)
			if stmt == nil {
				logger.Info("Validation. No statement generated from GenCheckStmt.")
				continue
			}

			if err := validation(ctx, schemaConfig, table, s, stmt, g, globalStatus, logger); err != nil {
				globalStatus.AddReadError(&joberror.JobError{
					Timestamp: time.Now(),
					Message:   "Validation failed: " + err.Error(),
					Query:     stmt.PrettyCQL(),
				})
			} else {
				globalStatus.ReadOps.Add(1)
			}

			if failFast && globalStatus.HasErrors() {
				return errors.New("encounter error, failsafe is set")
			}
		}
	}
}

// WarmupJob continuously applies mutations against the database
// for as long as the pump is active or the supplied duration expires.
func WarmupJob(
	ctx context.Context,
	_ <-chan heartBeat,
	schema *gemini.Schema,
	schemaCfg gemini.SchemaConfig,
	table *gemini.Table,
	s store.Store,
	r *rand.Rand,
	p *gemini.PartitionRangeConfig,
	g *gemini.Generator,
	globalStatus *status.GlobalStatus,
	_ string,
	warmup time.Duration,
	logger *zap.Logger,
) error {
	schemaConfig := &schemaCfg
	warmupCtx, cancel := context.WithTimeout(ctx, warmup)
	defer cancel()
	logger = logger.Named("warmup")
	logger.Info("starting warmup loop")
	defer func() {
		logger.Info("ending warmup loop")
	}()
	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			logger.Debug("warmup job terminated")
			return ctx.Err()
		case <-warmupCtx.Done():
			logger.Debug("warmup job finished")
			return nil
		default:
			// Do we care about errors during warmup?
			_ = mutation(warmupCtx, schema, schemaConfig, table, s, r, p, g, globalStatus, false, logger)
			if failFast && globalStatus.HasErrors() {
				return errors.New("encounter error, failsafe is set")
			}
		}
	}
}

func job(
	ctx context.Context,
	f testJob,
	actors uint64,
	schema *gemini.Schema,
	schemaConfig gemini.SchemaConfig,
	s store.Store,
	pump *Pump,
	generators []*gemini.Generator,
	globalStatus *status.GlobalStatus,
	logger *zap.Logger,
) error {
	g, gCtx := errgroup.WithContext(ctx)
	partitionRangeConfig := gemini.PartitionRangeConfig{
		MaxBlobLength:   schemaConfig.MaxBlobLength,
		MinBlobLength:   schemaConfig.MinBlobLength,
		MaxStringLength: schemaConfig.MaxStringLength,
		MinStringLength: schemaConfig.MinStringLength,
		UseLWT:          schemaConfig.UseLWT,
	}

	for j := range schema.Tables {
		gen := generators[j]
		table := schema.Tables[j]
		for i := 0; i < int(actors); i++ {
			r := rand.New(rand.NewSource(seed))
			g.Go(func() error {
				return f(gCtx, pump.ch, schema, schemaConfig, table, s, r, &partitionRangeConfig, gen, globalStatus, mode, warmup, logger)
			})
		}
	}
	return g.Wait()
}

func ddl(
	ctx context.Context,
	schema *gemini.Schema,
	sc *gemini.SchemaConfig,
	table *gemini.Table,
	s store.Store,
	r *rand.Rand,
	p *gemini.PartitionRangeConfig,
	globalStatus *status.GlobalStatus,
	logger *zap.Logger,
) error {
	if sc.CQLFeature != gemini.CQL_FEATURE_ALL {
		logger.Debug("ddl statements disabled")
		return nil
	}
	table.Lock()
	defer table.Unlock()
	if len(table.MaterializedViews) > 0 {
		// Scylla does not allow changing the DDL of a table with materialized views.
		return nil
	}
	ddlStmts, postStmtHook, err := schema.GenDDLStmt(table, r, p, sc)
	if err != nil {
		logger.Error("Failed! Mutation statement generation failed", zap.Error(err))
		globalStatus.WriteErrors.Add(1)
		return err
	}
	if ddlStmts == nil {
		if w := logger.Check(zap.DebugLevel, "no statement generated"); w != nil {
			w.Write(zap.String("job", "ddl"))
		}
		return nil
	}
	defer postStmtHook()
	defer func() {
		if verbose {
			jsonSchema, _ := json.MarshalIndent(schema, "", "    ")
			fmt.Printf("Schema: %v\n", string(jsonSchema))
		}
	}()
	for _, ddlStmt := range ddlStmts {
		ddlQuery := ddlStmt.Query
		if w := logger.Check(zap.DebugLevel, "ddl statement"); w != nil {
			w.Write(zap.String("pretty_cql", ddlStmt.PrettyCQL()))
		}
		if err = s.Mutate(ctx, ddlQuery); err != nil {
			globalStatus.AddWriteError(&joberror.JobError{
				Timestamp: time.Now(),
				Message:   "DDL failed: " + err.Error(),
				Query:     ddlStmt.PrettyCQL(),
			})
		} else {
			globalStatus.WriteOps.Add(1)
		}
	}
	return nil
}

func mutation(
	ctx context.Context,
	schema *gemini.Schema,
	_ *gemini.SchemaConfig,
	table *gemini.Table,
	s store.Store,
	r *rand.Rand,
	p *gemini.PartitionRangeConfig,
	g *gemini.Generator,
	globalStatus *status.GlobalStatus,
	deletes bool,
	logger *zap.Logger,
) error {
	mutateStmt, err := schema.GenMutateStmt(table, g, r, p, deletes)
	if err != nil {
		logger.Error("Failed! Mutation statement generation failed", zap.Error(err))
		globalStatus.WriteErrors.Add(1)
		return err
	}
	if mutateStmt == nil {
		if w := logger.Check(zap.DebugLevel, "no statement generated"); w != nil {
			w.Write(zap.String("job", "mutation"))
		}
		return err
	}
	mutateQuery := mutateStmt.Query
	token := mutateStmt.Token
	mutateValues := mutateStmt.Values
	defer func() {
		v := make(gemini.Value, len(table.PartitionKeys))
		copy(v, mutateValues)
		g.GiveOld(gemini.ValueWithToken{Token: token, Value: v})
	}()
	if w := logger.Check(zap.DebugLevel, "mutation statement"); w != nil {
		w.Write(zap.String("pretty_cql", mutateStmt.PrettyCQL()))
	}
	if err = s.Mutate(ctx, mutateQuery, mutateValues...); err != nil {
		globalStatus.AddWriteError(&joberror.JobError{
			Timestamp: time.Now(),
			Message:   "Mutation failed: " + err.Error(),
			Query:     mutateStmt.PrettyCQL(),
		})
	} else {
		globalStatus.WriteOps.Add(1)
	}
	return nil
}

func validation(
	ctx context.Context,
	sc *gemini.SchemaConfig,
	table *gemini.Table,
	s store.Store,
	stmt *gemini.Stmt,
	g *gemini.Generator,
	_ *status.GlobalStatus,
	logger *zap.Logger,
) error {
	checkQuery := stmt.Query
	token := stmt.Token
	checkValues := stmt.Values
	defer func() {
		// Signal done with this pk...
		g.GiveOld(gemini.ValueWithToken{Token: token})
	}()
	if w := logger.Check(zap.DebugLevel, "validation statement"); w != nil {
		w.Write(zap.String("pretty_cql", stmt.PrettyCQL()))
	}

	maxAttempts := 1
	delay := 10 * time.Millisecond
	if stmt.QueryType.PossibleAsyncOperation() {
		maxAttempts = sc.AsyncObjectStabilizationAttempts
		if maxAttempts < 1 {
			maxAttempts = 1
		}
		delay = sc.AsyncObjectStabilizationDelay
	}

	var lastErr, err error
	attempt := 1
	for {
		lastErr = err
		err = s.Check(ctx, table, checkQuery, checkValues...)
		if err == nil {
			if attempt > 1 {
				logger.Info(fmt.Sprintf("Validation successfully completed on %d attempt.", attempt))
			}
			return nil
		}
		if attempt == maxAttempts {
			break
		}
		if unWrapErr(err).Error() == unWrapErr(lastErr).Error() {
			logger.Info(fmt.Sprintf("Retring failed validation. %d attempt from %d attempts. Error same as at attempt before. ", attempt, maxAttempts))
		} else {
			logger.Info(fmt.Sprintf("Retring failed validation. %d attempt from %d attempts. Error: %s", attempt, maxAttempts, err))
		}

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			logger.Info(fmt.Sprintf("Retring failed validation stoped by done context. %d attempt from %d attempts. Error: %s", attempt, maxAttempts, err))
			return nil
		}
		attempt++
	}

	if attempt > 1 {
		logger.Info(fmt.Sprintf("Retring failed validation stoped by reach of max attempts %d. Error: %s", maxAttempts, err))
	} else {
		logger.Info(fmt.Sprintf("Validation failed. Error: %s", err))
	}

	return err
}

func unWrapErr(err error) error {
	nextErr := err
	for nextErr != nil {
		err = nextErr
		nextErr = errors.Unwrap(err)
	}
	return err
}
