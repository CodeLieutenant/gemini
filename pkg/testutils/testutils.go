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

//go:build !testing

package testutils

import (
	"testing"
	"time"

	"github.com/gocql/gocql"
)

type ScyllaContainer struct {
	Oracle *gocql.Session
	Test   *gocql.Session

	OracleHosts []string
	TestHosts   []string
}

func Must[T any](T, error) T {
	panic("this function should not be used in production code, only for testing purposes")
}

func SingleScylla(tb testing.TB, timeouts ...time.Duration) *ScyllaContainer {
	panic("this function should not be used in production code, only for testing purposes")
}

func TestContainers(_ testing.TB, timeouts ...time.Duration) *ScyllaContainer {
	panic("this function should not be used in production code, only for testing purposes")
}
