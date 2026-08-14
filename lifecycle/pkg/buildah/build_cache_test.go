// Copyright © 2026 Sealos.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package buildah

import (
	"reflect"
	"testing"

	"github.com/spf13/cobra"
)

func TestBuildCacheRemoveImagesOptions(t *testing.T) {
	tests := []struct {
		name    string
		opts    buildCacheCleanOptions
		filters []string
		force   bool
	}{
		{
			name:    "default removes dangling intermediate images",
			filters: []string{"readonly=false", "dangling=true", "intermediate=true"},
		},
		{
			name:    "all removes every unused image",
			opts:    buildCacheCleanOptions{all: true},
			filters: []string{"readonly=false"},
		},
		{
			name:    "force is propagated",
			opts:    buildCacheCleanOptions{force: true},
			filters: []string{"readonly=false", "dangling=true", "intermediate=true"},
			force:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildCacheRemoveImagesOptions(tt.opts)
			if !reflect.DeepEqual(got.Filters, tt.filters) {
				t.Fatalf("filters = %v, want %v", got.Filters, tt.filters)
			}
			if got.Force != tt.force {
				t.Fatalf("force = %v, want %v", got.Force, tt.force)
			}
		})
	}
}

func TestNewBuildCacheCommand(t *testing.T) {
	root := &cobra.Command{Use: "sealos"}
	RegisterRootCommand(root)

	command := newBuildCacheCommand()
	clean, _, err := command.Find([]string{"clean"})
	if err != nil {
		t.Fatalf("find clean command: %v", err)
	}
	if clean == nil {
		t.Fatal("clean command is not registered")
	}
	if clean.HasSubCommands() {
		t.Fatal("clean command should be a leaf command")
	}
	if clean.Flags().Lookup("all") == nil || clean.Flags().Lookup("force") == nil {
		t.Fatal("clean command is missing cache cleanup flags")
	}
}
