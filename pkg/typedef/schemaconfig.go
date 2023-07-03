// Copyright 2019 ScyllaDB
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package typedef

import (
	"time"

	"github.com/scylladb/gemini/pkg/replication"
	"github.com/scylladb/gemini/pkg/tableopts"
)

type SchemaConfig struct {
	ReplicationStrategy              *replication.Replication
	OracleReplicationStrategy        *replication.Replication
	TableOptions                     []tableopts.Option
	MaxTables                        int
	MaxPartitionKeys                 int
	MinPartitionKeys                 int
	MaxClusteringKeys                int
	MinClusteringKeys                int
	MaxColumns                       int
	MinColumns                       int
	MaxUDTParts                      int
	MaxTupleParts                    int
	MaxBlobLength                    int
	MaxStringLength                  int
	MinBlobLength                    int
	MinStringLength                  int
	UseCounters                      bool
	UseLWT                           bool
	CQLFeature                       CQLFeature
	AsyncObjectStabilizationAttempts int
	AsyncObjectStabilizationDelay    time.Duration
}

func (sc *SchemaConfig) Valid() error {
	if sc.MaxPartitionKeys <= sc.MinPartitionKeys {
		return ErrSchemaConfigInvalidRangePK
	}
	if sc.MaxClusteringKeys <= sc.MinClusteringKeys {
		return ErrSchemaConfigInvalidRangeCK
	}
	if sc.MaxColumns <= sc.MinColumns {
		return ErrSchemaConfigInvalidRangeCols
	}
	return nil
}

func (sc *SchemaConfig) GetMaxTables() int {
	return sc.MaxTables
}

func (sc *SchemaConfig) GetMaxPartitionKeys() int {
	return sc.MaxPartitionKeys
}

func (sc *SchemaConfig) GetMinPartitionKeys() int {
	return sc.MinPartitionKeys
}

func (sc *SchemaConfig) GetMaxClusteringKeys() int {
	return sc.MaxClusteringKeys
}

func (sc *SchemaConfig) GetMinClusteringKeys() int {
	return sc.MinClusteringKeys
}

func (sc *SchemaConfig) GetMaxColumns() int {
	return sc.MaxColumns
}

func (sc *SchemaConfig) GetMinColumns() int {
	return sc.MinColumns
}

func (sc *SchemaConfig) GetPartitionRangeConfig() PartitionRangeConfig {
	return PartitionRangeConfig{
		MaxBlobLength:   sc.MaxBlobLength,
		MinBlobLength:   sc.MinBlobLength,
		MaxStringLength: sc.MaxStringLength,
		MinStringLength: sc.MinStringLength,
		UseLWT:          sc.UseLWT,
	}
}
