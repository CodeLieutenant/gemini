// Copyright 2025 ScyllaDB
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

package statements

import (
	"context"
	"fmt"
	"slices"

	"github.com/scylladb/gocqlx/v3/qb"

	"github.com/scylladb/gemini/pkg/typedef"
	"github.com/scylladb/gemini/pkg/utils"
)

func (g *Generator) Select(ctx context.Context) (*typedef.Stmt, error) {
	switch n := g.random.IntN(SelectStatementsCount); n {
	case SelectSinglePartitionQuery:
		return g.genSinglePartitionQuery(ctx)
	case SelectMultiplePartitionQuery:
		return g.genMultiplePartitionQuery(ctx)
	case SelectClusteringRangeQuery:
		return g.genClusteringRangeQuery(ctx)
	case SelectMultiplePartitionClusteringRangeQuery:
		return g.genMultiplePartitionClusteringRangeQuery(ctx)
	case SelectSingleIndexQuery:
		// Reducing the probability to hit these since they often take a long time to run
		if len(g.table.Indexes) > 0 && g.random.IntN(SelectSingleIndexQuery+2) == 0 {
			return g.genSingleIndexQuery(), nil
		}

		return g.genSinglePartitionQuery(ctx)
	default:
		panic(fmt.Sprintf("unexpected case in GenCheckStmt, random value: %d", n))
	}
}

func (g *Generator) genSinglePartitionQuery(ctx context.Context) (*typedef.Stmt, error) {
	g.table.RLock()
	defer g.table.RUnlock()

	pks, builder, err := g.getSinglePartitionKeys(ctx)
	if err != nil {
		return nil, err
	}

	query, _ := builder.ToCql()

	return &typedef.Stmt{
		QueryType:     typedef.SelectStatementType,
		Query:         query,
		PartitionKeys: pks,
		Values:        pks.Values.ToCQLValues(g.table.PartitionKeys),
	}, nil
}

func (g *Generator) getMultiplePartitionKeys(initial int) int {
	l := g.table.PartitionKeys.Len()
	if l == 0 {
		panic("table has no partition keys")
	}

	return utils.RandInt2(g.random, 1, TotalCartesianProductCount(float64(initial), float64(l)))
}

func (g *Generator) getMultipleClusteringKeys(initial int) int {
	l := g.table.ClusteringKeys.Len()
	if l == 0 {
		return 0
	}

	return utils.RandInt2(g.random, 1, TotalCartesianProductCount(float64(initial), float64(l)))
}

func (g *Generator) getIndex(initial int) int {
	l := len(g.table.Indexes)
	return min(initial, l) + g.random.IntN(l)
}

func (g *Generator) getSinglePartitionKeys(ctx context.Context) (typedef.PartitionKeys, *qb.SelectBuilder, error) {
	partitionKeys, err := g.generator.GetOld(ctx)
	if err != nil {
		return typedef.PartitionKeys{}, nil, err
	}

	builder := qb.Select(g.keyspaceAndTable)
	for _, pk := range g.table.PartitionKeys {
		builder = builder.Where(qb.Eq(pk.Name))
	}

	return partitionKeys, builder, nil
}

func (g *Generator) buildMultiPartitionsKey(ctx context.Context) (*typedef.Values, *qb.SelectBuilder, error) {
	builder := qb.Select(g.keyspaceAndTable)

	numQueryPKs := g.getMultiplePartitionKeys(2)
	pks := typedef.NewValues(g.table.PartitionKeys.Len())

	maybeReturn := make([]typedef.PartitionKeys, 0, numQueryPKs)

	for range numQueryPKs {
		pk, err := g.generator.GetOld(ctx)
		if err != nil {
			if len(maybeReturn) > 0 {
				g.generator.GiveOlds(ctx, maybeReturn...)
			}
			return nil, nil, err
		}

		pks.Merge(pk.Values)
		maybeReturn = append(maybeReturn, pk)
	}

	for _, pk := range g.table.PartitionKeys {
		builder.Where(qb.InTuple(pk.Name, numQueryPKs))
	}

	return pks, builder, nil
}

func (g *Generator) buildClusteringRange(builder *qb.SelectBuilder, values []any) []any {
	if len(g.table.ClusteringKeys) == 0 {
		return values
	}

	values = slices.Grow(values, g.table.ClusteringKeys.LenValues())
	maxClusteringKeys := g.getMultipleClusteringKeys(1)

	for _, ck := range g.table.ClusteringKeys[:maxClusteringKeys-1] {
		builder.Where(qb.Eq(ck.Name))
		values = append(values, ck.Type.GenValue(g.random, g.partitionConfig)...)
	}

	ck := g.table.ClusteringKeys[maxClusteringKeys-1]
	builder.Where(qb.Gt(ck.Name)).Where(qb.Lt(ck.Name))
	values = append(values, ck.Type.GenValue(g.random, g.partitionConfig)...)
	values = append(values, ck.Type.GenValue(g.random, g.partitionConfig)...)

	return values
}

func (g *Generator) genMultiplePartitionQuery(ctx context.Context) (*typedef.Stmt, error) {
	pks, builder, err := g.buildMultiPartitionsKey(ctx)
	if err != nil {
		return nil, err
	}

	query, _ := builder.ToCql()

	return &typedef.Stmt{
		PartitionKeys: typedef.PartitionKeys{Values: pks},
		Values:        pks.ToCQLValues(g.table.PartitionKeys),
		QueryType:     typedef.SelectMultiPartitionType,
		Query:         query,
	}, nil
}

func (g *Generator) genClusteringRangeQuery(ctx context.Context) (*typedef.Stmt, error) {
	pks, builder, err := g.getSinglePartitionKeys(ctx)
	if err != nil {
		return nil, err
	}

	values := g.buildClusteringRange(builder, pks.Values.ToCQLValues(g.table.PartitionKeys))

	query, _ := builder.ToCql()

	return &typedef.Stmt{
		PartitionKeys: pks,
		Values:        values,
		QueryType:     typedef.SelectRangeStatementType,
		Query:         query,
	}, nil
}

func (g *Generator) genMultiplePartitionClusteringRangeQuery(ctx context.Context) (*typedef.Stmt, error) {
	pks, builder, err := g.buildMultiPartitionsKey(ctx)
	if err != nil {
		return nil, err
	}

	values := g.buildClusteringRange(builder, pks.ToCQLValues(g.table.PartitionKeys))

	query, _ := builder.ToCql()

	return &typedef.Stmt{
		QueryType:     typedef.SelectMultiPartitionRangeStatementType,
		PartitionKeys: typedef.PartitionKeys{Values: pks},
		Values:        values,
		Query:         query,
	}, nil
}

func (g *Generator) genSingleIndexQuery() *typedef.Stmt {
	idxCount := g.getIndex(0)
	values := make([]any, 0, idxCount)
	builder := qb.Select(g.keyspaceAndTable).AllowFiltering()

	g.table.RLock()
	defer g.table.RUnlock()

	for _, idx := range g.table.Indexes[:idxCount] {
		builder = builder.Where(qb.Eq(idx.ColumnName))
		values = append(values, idx.Column.Type.GenValue(g.random, g.partitionConfig)...)
	}

	query, _ := builder.ToCql()

	return &typedef.Stmt{
		PartitionKeys: typedef.PartitionKeys{},
		Values:        values,
		QueryType:     typedef.SelectByIndexStatementType,
		Query:         query,
	}
}
