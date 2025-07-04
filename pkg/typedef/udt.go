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
	"github.com/gocql/gocql"

	"github.com/scylladb/gemini/pkg/utils"
)

type UDTType struct {
	ComplexType string                `json:"complex_type"`
	ValueTypes  map[string]SimpleType `json:"value_types"`
	TypeName    string                `json:"type_name"`
	Frozen      bool                  `json:"frozen"`
}

func (t *UDTType) CQLType() gocql.TypeInfo {
	return goCQLTypeMap[gocql.TypeUDT]
}

func (t *UDTType) Name() string {
	return t.TypeName
}

func (t *UDTType) CQLDef() string {
	if t.Frozen {
		return "frozen<" + t.TypeName + ">"
	}
	return t.TypeName
}

func (t *UDTType) CQLHolder() string {
	return "?"
}

func (t *UDTType) Indexable() bool {
	for _, ty := range t.ValueTypes {
		if ty == TypeDuration {
			return false
		}
	}

	return true
}

func (t *UDTType) GenJSONValue(r utils.Random, p *PartitionRangeConfig) any {
	vals := make(map[string]any)
	for name, typ := range t.ValueTypes {
		vals[name] = typ.GenJSONValue(r, p)
	}
	return vals
}

func (t *UDTType) GenValue(r utils.Random, p *PartitionRangeConfig) []any {
	vals := make(map[string]any)
	for name, typ := range t.ValueTypes {
		vals[name] = typ.GenValue(r, p)[0]
	}
	return []any{vals}
}

func (t *UDTType) LenValue() int {
	return 1
}

// ValueVariationsNumber returns number of bytes generated value holds
func (t *UDTType) ValueVariationsNumber(p *PartitionRangeConfig) float64 {
	out := float64(1)
	for _, tp := range t.ValueTypes {
		out *= tp.ValueVariationsNumber(p)
	}
	return out
}
