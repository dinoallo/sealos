package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	"github.com/labring/sealos/pkg/forge"
)

type forgeCommandOptions struct {
	File              string
	SourceOverlayPath string
	BinaryOverlayPath string
	AllowJITBuild     bool
	Output            string
}

func newForgeCmd() *cobra.Command {
	opts := &forgeCommandOptions{}

	cmd := &cobra.Command{
		Use:   "forge",
		Short: "Plan and resolve ClusterForge components from source and binary overlays",
	}

	cmd.PersistentFlags().StringVarP(&opts.File, "file", "f", "ClusterForge.yaml", "path to ClusterForge manifest")
	cmd.PersistentFlags().StringVar(&opts.SourceOverlayPath, "source-overlay", "", "path to the source overlay root")
	cmd.PersistentFlags().StringVar(&opts.BinaryOverlayPath, "binary-overlay", "", "path to the binary overlay root")
	cmd.PersistentFlags().BoolVar(&opts.AllowJITBuild, "allow-jit", false, "allow source overlay fallback when binary cache misses")
	cmd.PersistentFlags().StringVarP(&opts.Output, "output", "o", "yaml", "output format: yaml or json")

	cmd.AddCommand(newForgePlanCmd(opts))
	cmd.AddCommand(newForgeApplyCmd(opts))
	cmd.AddCommand(newForgeRunCmd(opts))
	return cmd
}

func newForgePlanCmd(opts *forgeCommandOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "plan",
		Short: "Resolve ClusterForge components and print the execution plan",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := buildForgePlan(opts)
			if err != nil {
				return err
			}
			return printForgeOutput(plan, opts.Output)
		},
	}
}

func newForgeApplyCmd(opts *forgeCommandOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "apply",
		Short: "Resolve a ClusterForge manifest and print the actionable plan",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := buildForgePlan(opts)
			if err != nil {
				return err
			}
			return printForgeOutput(plan, opts.Output)
		},
	}
}

func newForgeRunCmd(opts *forgeCommandOptions) *cobra.Command {
	var image string
	var traits []string
	var version string
	var overlay string

	cmd := &cobra.Command{
		Use:   "run IMAGE",
		Short: "Resolve a single component image against binary and source overlays",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			image = args[0]
			cluster := &forge.ClusterForge{
				TypeMeta: forge.TypeMeta{
					APIVersion: forge.DefaultAPIVersion,
					Kind:       forge.ClusterForgeKind,
				},
				Spec: forge.ClusterForgeSpec{
					Base: forge.ClusterForgeBase{Image: image},
					Components: []forge.ComponentSpec{
						{
							Name:    image,
							Version: version,
							Traits:  traits,
							Overlay: overlay,
						},
					},
					Strategy: forge.ClusterForgeStrategy{AllowJITBuild: opts.AllowJITBuild},
				},
			}
			plan, err := forge.BuildPlan(cluster, forge.Options{
				SourceOverlayPath: opts.SourceOverlayPath,
				BinaryOverlayPath: opts.BinaryOverlayPath,
				AllowJITBuild:     opts.AllowJITBuild,
			})
			if err != nil {
				return err
			}
			return printForgeOutput(plan, opts.Output)
		},
	}

	cmd.Flags().StringSliceVar(&traits, "traits", nil, "component traits, for example --traits +ha,+cgroupv2")
	cmd.Flags().StringVar(&version, "version", "", "component version used to locate the Forgefile")
	cmd.Flags().StringVar(&overlay, "overlay", "", "explicit overlay name or path")
	return cmd
}

func buildForgePlan(opts *forgeCommandOptions) (*forge.Plan, error) {
	cluster, err := forge.LoadClusterForge(opts.File)
	if err != nil {
		return nil, err
	}
	return forge.BuildPlan(cluster, forge.Options{
		SourceOverlayPath: opts.SourceOverlayPath,
		BinaryOverlayPath: opts.BinaryOverlayPath,
		AllowJITBuild:     opts.AllowJITBuild,
	})
}

func printForgeOutput(v interface{}, output string) error {
	switch output {
	case "yaml":
		data, err := yaml.Marshal(v)
		if err != nil {
			return fmt.Errorf("marshal yaml: %w", err)
		}
		_, err = os.Stdout.Write(data)
		return err
	case "json":
		data, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal json: %w", err)
		}
		data = append(data, '\n')
		_, err = os.Stdout.Write(data)
		return err
	default:
		return fmt.Errorf("--output must be 'yaml' or 'json'")
	}
}
