package forge

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/opencontainers/go-digest"
)

func BuildPlan(cluster *ClusterForge, opts Options) (*Plan, error) {
	if cluster == nil {
		return nil, fmt.Errorf("cluster forge config is required")
	}

	plan := &Plan{
		Cluster:       cluster,
		SourceOverlay: opts.SourceOverlayPath,
		BinaryOverlay: opts.BinaryOverlayPath,
		Components:    make([]ComponentPlan, 0, len(cluster.Spec.Components)),
	}

	for _, component := range cluster.Spec.Components {
		componentPlan, err := buildComponentPlan(component, opts, cluster.Spec.Strategy.AllowJITBuild || opts.AllowJITBuild)
		if err != nil {
			return nil, err
		}
		plan.Components = append(plan.Components, componentPlan)
	}

	return plan, nil
}

func buildComponentPlan(component ComponentSpec, opts Options, allowJIT bool) (ComponentPlan, error) {
	expression, normalized, err := NormalizeTraitsExpression(component.Traits)
	if err != nil {
		return ComponentPlan{}, fmt.Errorf("normalize traits for %s: %w", component.Name, err)
	}

	selectedOverlay := component.Overlay
	if selectedOverlay == "" {
		if opts.SourceOverlayPath != "" {
			selectedOverlay = opts.SourceOverlayPath
		} else {
			selectedOverlay = opts.BinaryOverlayPath
		}
	}

	result := ResolutionResult{}
	if opts.BinaryOverlayPath != "" {
		dgst, ok, err := lookupBinaryManifest(opts.BinaryOverlayPath, component.Name, component.Version, expression)
		if err != nil {
			return ComponentPlan{}, err
		}
		if ok {
			result.Mode = "binary-cache-hit"
			result.MatchedDigest = digest.Digest(dgst)
			return ComponentPlan{
				Name:             component.Name,
				Version:          component.Version,
				NormalizedTraits: normalized,
				TraitsExpression: expression,
				SelectedOverlay:  selectedOverlay,
				Resolution:       result,
			}, nil
		}
	}

	if opts.SourceOverlayPath == "" {
		return ComponentPlan{}, fmt.Errorf("binary overlay miss for %s and no source overlay configured", component.Name)
	}
	if !allowJIT {
		return ComponentPlan{}, fmt.Errorf("binary overlay miss for %s and JIT build is disabled", component.Name)
	}

	forgefilePath, err := FindForgefile(opts.SourceOverlayPath, component.Name, component.Version)
	if err != nil {
		return ComponentPlan{}, err
	}
	forgefile, err := LoadForgefile(forgefilePath)
	if err != nil {
		return ComponentPlan{}, err
	}

	result.Mode = "source-jit"
	result.ForgefilePath = forgefilePath
	result.ForgefileSummary = summarizeForgefile(forgefile, normalized)
	return ComponentPlan{
		Name:             component.Name,
		Version:          component.Version,
		NormalizedTraits: normalized,
		TraitsExpression: expression,
		SelectedOverlay:  selectedOverlay,
		Resolution:       result,
	}, nil
}

func lookupBinaryManifest(root, component, version, traits string) (string, bool, error) {
	indexPath, err := findBinaryIndex(root, component, version)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}

	forgefile, err := LoadForgefile(indexPath)
	if err != nil {
		return "", false, err
	}

	for _, overlay := range forgefile.BinaryOverlays {
		for _, manifest := range overlay.Manifests {
			if manifest.Annotations[TraitsAnnotation] == traits {
				return manifest.Digest.String(), true, nil
			}
		}
	}
	return "", false, nil
}

func findBinaryIndex(root, component, version string) (string, error) {
	componentPath := filepath.FromSlash(component)
	candidates := []string{}
	if version != "" {
		candidates = append(candidates,
			filepath.Join(root, "apps", componentPath, version, "Forgefile"),
			filepath.Join(root, componentPath, version, "Forgefile"),
		)
	}
	candidates = append(candidates,
		filepath.Join(root, "apps", componentPath, "Forgefile"),
		filepath.Join(root, componentPath, "Forgefile"),
	)
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		} else if err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", os.ErrNotExist
}

func summarizeForgefile(forgefile *Forgefile, traits []string) *ForgefileSummary {
	summary := &ForgefileSummary{
		PackageName:          forgefile.Package.Name,
		PackageVersion:       forgefile.Package.Version,
		BaseImage:            forgefile.Base.Image,
		EnabledPatchCount:    countEnabledPatches(forgefile.Patches, traits),
		EnabledTemplateCount: countEnabledTemplates(forgefile.Templates, traits),
		EnabledCopyCount:     countEnabledCopies(forgefile.Copies, traits),
		EnabledScriptCount:   countEnabledScripts(forgefile.Scripts, traits),
	}
	for _, dep := range forgefile.Dependencies {
		if matchesTraitFilter(traits, dep.Traits) {
			name := dep.Name
			if dep.Version != "" {
				name = name + ":" + dep.Version
			}
			summary.Dependencies = append(summary.Dependencies, name)
		}
	}
	return summary
}

func countEnabledPatches(patches PatchSet, traits []string) int {
	total := 0
	for _, item := range patches.TOML {
		if matchesTraitFilter(traits, item.Traits) {
			total++
		}
	}
	for _, item := range patches.JSON {
		if matchesTraitFilter(traits, item.Traits) {
			total++
		}
	}
	for _, item := range patches.Kustomize {
		if matchesTraitFilter(traits, item.Traits) {
			total++
		}
	}
	return total
}

func countEnabledTemplates(items []TemplateSpec, traits []string) int {
	total := 0
	for _, item := range items {
		if matchesTraitFilter(traits, item.Traits) {
			total++
		}
	}
	return total
}

func countEnabledCopies(items []CopySpec, traits []string) int {
	total := 0
	for _, item := range items {
		if matchesTraitFilter(traits, item.Traits) {
			total++
		}
	}
	return total
}

func countEnabledScripts(items []ScriptSpec, traits []string) int {
	total := 0
	for _, item := range items {
		if matchesTraitFilter(traits, item.Traits) {
			total++
		}
	}
	return total
}
