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
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand/v2"
	"net/http"
	"net/http/pprof"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/scylladb/gemini/pkg/distributions"
	"github.com/scylladb/gemini/pkg/generators"
	"github.com/scylladb/gemini/pkg/generators/statements"
	"github.com/scylladb/gemini/pkg/jobs"
	"github.com/scylladb/gemini/pkg/metrics"
	"github.com/scylladb/gemini/pkg/realrandom"
	"github.com/scylladb/gemini/pkg/status"
	"github.com/scylladb/gemini/pkg/stmtlogger"
	"github.com/scylladb/gemini/pkg/stop"
	"github.com/scylladb/gemini/pkg/store"
	"github.com/scylladb/gemini/pkg/typedef"
	"github.com/scylladb/gemini/pkg/utils"
	"github.com/scylladb/gemini/pkg/workpool"
)

var (
	rootCmd = &cobra.Command{
		Use:              "gemini",
		Short:            "Gemini is an automatic random testing tool for Scylla.",
		RunE:             run,
		PersistentPreRun: preRun,
		SilenceUsage:     true,
	}

	versionInfo VersionInfo
)

func init() {
	setupFlags(rootCmd)
}

func preRun(cmd *cobra.Command, _ []string) {
	metrics.StartMetricsServer(cmd.Context(), metricsPort)

	if profilingPort != 0 {
		go func() {
			mux := http.NewServeMux()

			mux.HandleFunc("GET /debug/pprof/", pprof.Index)
			mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
			mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
			mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
			mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)

			log.Fatal(http.ListenAndServe("0.0.0.0:"+strconv.Itoa(profilingPort), mux))
		}()
	}

	var err error

	versionInfo, err = NewVersionInfo()
	if err != nil {
		panic(err)
	}

	cmd.Version = versionInfo.String()
}

//nolint:gocyclo
func run(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	stopFlag := stop.NewFlag("main")
	time.AfterFunc(warmup+duration+2*time.Second, func() {
		stopFlag.SetSoft(true)
	})
	go func() {
		<-ctx.Done()
		stopFlag.SetHard(true)
	}()

	logger := createLogger(level)

	versionJSON, err := cmd.PersistentFlags().GetBool("version-json")

	if versionFlag || versionJSON {
		if err != nil {
			log.Panicf("Failed to get version info as json flag: %v", err)
		}

		if versionJSON {
			var data []byte
			data, err = json.Marshal(versionInfo)
			if err != nil {
				log.Panicf("Failed to marshal version info: %v\n", err)
			}

			//nolint:forbidigo
			fmt.Println(string(data))
			return nil
		}

		//nolint:forbidigo
		fmt.Println(versionInfo.String())

		return nil
	}

	globalStatus := status.NewGlobalStatus(maxErrorsToStore)
	defer utils.IgnoreError(logger.Sync)

	for i := range len(testClusterHost) {
		testClusterHost[i] = strings.TrimSpace(testClusterHost[i])
	}

	for i := range len(oracleClusterHost) {
		oracleClusterHost[i] = strings.TrimSpace(oracleClusterHost[i])
	}

	if err = validateSeed(seed); err != nil {
		return errors.Wrapf(err, "failed to parse --seed argument")
	}
	if err = validateSeed(schemaSeed); err != nil {
		return errors.Wrapf(err, "failed to parse --schema-seed argument")
	}

	outFile, err := utils.CreateFile(outFileArg, true, os.Stdout)
	if err != nil {
		return err
	}

	pool := workpool.New(iOWorkerPool)
	metrics.GeminiInformation.WithLabelValues("io_thread_pool").Set(float64(iOWorkerPool))

	intSeed := seedFromString(seed)

	randSrc, distFunc, err := distributions.New(
		partitionKeyDistribution,
		partitionCount,
		intSeed,
		stdDistMean,
		oneStdDev,
	)
	if err != nil {
		return errors.Wrapf(
			err,
			"Failed to create distribution function: %s",
			partitionKeyDistribution,
		)
	}

	schema, schemaConfig, err := getSchema(intSeed, logger)
	if err != nil {
		return errors.Wrap(err, "failed to get schema")
	}

	gens := generators.New(
		schema,
		distFunc,
		intSeed,
		partitionCount,
		pkBufferReuseSize,
		logger,
		randSrc,
	)
	utils.AddFinalizer(func() {
		logger.Info("closing generators")

		if err = gens.Close(); err != nil {
			logger.Error("failed to close generators", zap.Error(err))
		} else {
			logger.Info("generators closed")
		}
	})

	storeConfig := store.Config{
		MaxRetriesMutate:        maxRetriesMutate,
		MaxRetriesMutateSleep:   maxRetriesMutateSleep,
		UseServerSideTimestamps: useServerSideTimestamps,
		OracleStatementFile:     oracleStatementLogFile,
		TestStatementFile:       testStatementLogFile,
		Compression:             stmtlogger.CompressionNone,
		TestClusterConfig: store.ScyllaClusterConfig{
			Name:                    stmtlogger.TypeTest,
			Hosts:                   testClusterHost,
			HostSelectionPolicy:     testClusterHostSelectionPolicy,
			Consistency:             consistency,
			RequestTimeout:          requestTimeout,
			ConnectTimeout:          connectTimeout,
			UseServerSideTimestamps: useServerSideTimestamps,
			Username:                testClusterUsername,
			Password:                testClusterPassword,
		},
	}

	if len(oracleClusterHost) > 0 {
		storeConfig.OracleClusterConfig = &store.ScyllaClusterConfig{
			Name:                    stmtlogger.TypeOracle,
			Hosts:                   oracleClusterHost,
			HostSelectionPolicy:     oracleClusterHostSelectionPolicy,
			Consistency:             consistency,
			RequestTimeout:          requestTimeout,
			ConnectTimeout:          connectTimeout,
			UseServerSideTimestamps: useServerSideTimestamps,
			Username:                oracleClusterUsername,
			Password:                oracleClusterPassword,
		}
	}

	schemaChanges, err := gens.Gens[schema.Tables[0].Name].Get(ctx)
	if err != nil {
		return errors.Wrapf(err, "failed to get schema changes for %s", schema.Tables[0].Name)
	}

	st, err := store.New(schemaChanges, pool, schema, storeConfig, logger.Named("store"), globalStatus.Errors)
	if err != nil {
		return err
	}

	utils.AddFinalizer(func() {
		logger.Info("closing store")

		if err = st.Close(); err != nil {
			logger.Error("failed to close store", zap.Error(err))
		} else {
			logger.Info("store closed")
		}
	})

	if dropSchema && mode != jobs.ReadMode {
		for _, stmt := range statements.GetDropKeyspace(schema) {
			logger.Debug(stmt)
			if err = st.Mutate(ctx, typedef.SimpleStmt(stmt, typedef.DropKeyspaceStatementType)); err != nil {
				return errors.Wrap(err, "unable to drop schema")
			}
		}
	}

	testKeyspace, oracleKeyspace := statements.GetCreateKeyspaces(schema)
	if err = st.Create(
		ctx,
		typedef.SimpleStmt(testKeyspace, typedef.CreateKeyspaceStatementType),
		typedef.SimpleStmt(oracleKeyspace, typedef.CreateKeyspaceStatementType)); err != nil {
		return errors.Wrap(err, "unable to create keyspace")
	}

	for _, stmt := range statements.GetCreateSchema(schema) {
		logger.Debug(stmt)
		if err = st.Mutate(ctx, typedef.SimpleStmt(stmt, typedef.CreateSchemaStatementType)); err != nil {
			return errors.Wrap(err, "unable to create schema")
		}
	}

	stop.StartOsSignalsTransmitter(logger, stopFlag)

	if warmup > 0 && !stopFlag.IsHardOrSoft() {
		warmupStopFlag := stopFlag.CreateChild("warmup")

		jobsList := jobs.ListFromMode(jobs.WarmupMode, warmup, concurrency)
		time.AfterFunc(warmup, func() {
			warmupStopFlag.SetHard(false)
		})
		if err = jobsList.Run(ctx, schema, schemaConfig, st, gens, globalStatus, logger, warmupStopFlag, randSrc); err != nil {
			logger.Error("warmup encountered an error", zap.Error(err))
		}
	}

	if !stopFlag.IsHardOrSoft() {
		work := stopFlag.CreateChild("work")

		jobsList := jobs.ListFromMode(mode, duration, concurrency)
		time.AfterFunc(duration, func() {
			work.SetHard(false)
		})
		if err = jobsList.Run(ctx, schema, schemaConfig, st, gens, globalStatus, logger, work, randSrc); err != nil {
			logger.Error("error detected", zap.Error(err))
			work.SetHard(true)
		}
	}

	globalStatus.PrintResult(outFile, schema, versionInfo.Gemini.Version, versionInfo)
	if globalStatus.HasErrors() {
		return errors.New("gemini encountered errors, exiting with non zero status")
	}

	logger.Info("test finished")
	return nil
}

const (
	stdDistMean = math.MaxUint64 / 2
	oneStdDev   = 0.341 * math.MaxUint64
)

func createLogger(level string) *zap.Logger {
	lvl := zap.NewAtomicLevel()
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl.SetLevel(zap.InfoLevel)
	}

	file, err := utils.CreateFile("gemini.log", false, os.Stdout)
	if err != nil {
		log.Fatalf("failed to create log file: %v", err)
	}

	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.EncodeTime = zapcore.RFC3339NanoTimeEncoder
	encoderCfg.EncodeDuration = zapcore.StringDurationEncoder
	encoderCfg.EncodeLevel = zapcore.LowercaseLevelEncoder
	encoderCfg.EncodeCaller = nil

	logger := zap.New(zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderCfg),
		zapcore.NewMultiWriteSyncer(zapcore.Lock(file.(zapcore.WriteSyncer)), zapcore.Lock(os.Stdout)),
		lvl,
	))
	return logger
}

func getCQLFeature(feature string) typedef.CQLFeature {
	switch strings.ToLower(feature) {
	case "all":
		return typedef.CQL_FEATURE_ALL
	case "normal":
		return typedef.CQL_FEATURE_NORMAL
	default:
		return typedef.CQL_FEATURE_BASIC
	}
}

func printSetup(seed, schemaSeed uint64) {
	tw := new(tabwriter.Writer)
	tw.Init(os.Stdout, 0, 8, 2, '\t', tabwriter.AlignRight)
	_, _ = fmt.Fprintf(tw, "Seed:\t%d\n", seed)
	_, _ = fmt.Fprintf(tw, "Schema seed:\t%d\n", schemaSeed)
	_, _ = fmt.Fprintf(tw, "Maximum duration:\t%s\n", duration)
	_, _ = fmt.Fprintf(tw, "Warmup duration:\t%s\n", warmup)
	_, _ = fmt.Fprintf(tw, "Concurrency:\t%d\n", concurrency)
	_, _ = fmt.Fprintf(tw, "Test cluster:\t%s\n", testClusterHost)
	_, _ = fmt.Fprintf(tw, "Oracle cluster:\t%s\n", oracleClusterHost)
	if outFileArg == "" {
		_, _ = fmt.Fprintf(tw, "Output file:\t%s\n", "<stdout>")
	} else {
		_, _ = fmt.Fprintf(tw, "Output file:\t%s\n", outFileArg)
	}
	_ = tw.Flush()
}

func RealRandom() uint64 {
	return rand.New(realrandom.Source).Uint64()
}

func validateSeed(seed string) error {
	if seed == "random" {
		return nil
	}
	_, err := strconv.ParseUint(seed, 10, 64)
	return err
}

func seedFromString(seed string) uint64 {
	if seed == "random" {
		return RealRandom()
	}
	val, _ := strconv.ParseUint(seed, 10, 64)
	return val
}
