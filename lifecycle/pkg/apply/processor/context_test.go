/*
Copyright 2026 sealos.

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

package processor

import (
	"context"
	"reflect"
	"testing"
)

func TestContextValuesUseIndependentKeys(t *testing.T) {
	ctx := WithCommands(context.Background(), []string{"echo ready"})
	ctx = WithEnvs(ctx, map[string]string{"registryDomain": "sealos.hub"})
	ctx = WithAllowExistingRuntime(ctx, true)

	if got, want := GetCommands(ctx), []string{"echo ready"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("GetCommands() = %#v, want %#v", got, want)
	}
	if got, want := GetEnvs(ctx), map[string]string{"registryDomain": "sealos.hub"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("GetEnvs() = %#v, want %#v", got, want)
	}
	if !AllowExistingRuntime(ctx) {
		t.Fatal("AllowExistingRuntime() = false, want true")
	}
}
