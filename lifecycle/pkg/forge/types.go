package forge

import "github.com/opencontainers/go-digest"

const (
	DefaultAPIVersion = "forge.sealos.io/v1alpha1"
	ClusterForgeKind  = "ClusterForge"
	TraitsAnnotation  = "forge.sealos.io/traits"
)

type TypeMeta struct {
	APIVersion string `json:"apiVersion" yaml:"apiVersion"`
	Kind       string `json:"kind" yaml:"kind"`
}

type Forgefile struct {
	TypeMeta       `json:",inline" yaml:",inline"`
	Metadata       Metadata             `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Package        PackageSpec          `json:"package" yaml:"package"`
	Base           BaseSpec             `json:"base,omitempty" yaml:"base,omitempty"`
	Sources        []SourceSpec         `json:"sources,omitempty" yaml:"sources,omitempty"`
	Traits         map[string]TraitSpec `json:"traits,omitempty" yaml:"traits,omitempty"`
	Patches        PatchSet             `json:"patches,omitempty" yaml:"patches,omitempty"`
	Templates      []TemplateSpec       `json:"templates,omitempty" yaml:"templates,omitempty"`
	Copies         []CopySpec           `json:"copies,omitempty" yaml:"copies,omitempty"`
	Scripts        []ScriptSpec         `json:"scripts,omitempty" yaml:"scripts,omitempty"`
	Dependencies   []DependencySpec     `json:"dependencies,omitempty" yaml:"dependencies,omitempty"`
	BinaryOverlays []BinaryOverlayRef   `json:"binaryOverlays,omitempty" yaml:"binaryOverlays,omitempty"`
}

type Metadata struct {
	Name string `json:"name,omitempty" yaml:"name,omitempty"`
}

type PackageSpec struct {
	Name     string `json:"name" yaml:"name"`
	Version  string `json:"version" yaml:"version"`
	Category string `json:"category,omitempty" yaml:"category,omitempty"`
}

type BaseSpec struct {
	Image string `json:"image,omitempty" yaml:"image,omitempty"`
}

type SourceSpec struct {
	Name string `json:"name" yaml:"name"`
	Ref  string `json:"ref,omitempty" yaml:"ref,omitempty"`
}

type TraitSpec struct {
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Default     bool              `json:"default,omitempty" yaml:"default,omitempty"`
	Requires    []string          `json:"requires,omitempty" yaml:"requires,omitempty"`
	Parameters  map[string]string `json:"parameters,omitempty" yaml:"parameters,omitempty"`
}

type PatchSet struct {
	TOML      []PatchRef `json:"toml_patch,omitempty" yaml:"toml_patch,omitempty"`
	JSON      []PatchRef `json:"json_patch,omitempty" yaml:"json_patch,omitempty"`
	Kustomize []PatchRef `json:"kustomize_patch,omitempty" yaml:"kustomize_patch,omitempty"`
}

type PatchRef struct {
	Path   string   `json:"path" yaml:"path"`
	Traits []string `json:"traits,omitempty" yaml:"traits,omitempty"`
}

type TemplateSpec struct {
	Path   string            `json:"path" yaml:"path"`
	Output string            `json:"output,omitempty" yaml:"output,omitempty"`
	Traits []string          `json:"traits,omitempty" yaml:"traits,omitempty"`
	Values map[string]string `json:"values,omitempty" yaml:"values,omitempty"`
}

type CopySpec struct {
	From   string   `json:"from" yaml:"from"`
	To     string   `json:"to" yaml:"to"`
	Backup bool     `json:"backup,omitempty" yaml:"backup,omitempty"`
	Traits []string `json:"traits,omitempty" yaml:"traits,omitempty"`
}

type ScriptSpec struct {
	Path   string   `json:"path" yaml:"path"`
	Stage  string   `json:"stage,omitempty" yaml:"stage,omitempty"`
	Traits []string `json:"traits,omitempty" yaml:"traits,omitempty"`
}

type DependencySpec struct {
	Name     string   `json:"name" yaml:"name"`
	Version  string   `json:"version,omitempty" yaml:"version,omitempty"`
	Traits   []string `json:"traits,omitempty" yaml:"traits,omitempty"`
	Overlay  string   `json:"overlay,omitempty" yaml:"overlay,omitempty"`
	Optional bool     `json:"optional,omitempty" yaml:"optional,omitempty"`
}

type BinaryOverlayRef struct {
	Name       string           `json:"name" yaml:"name"`
	Repository string           `json:"repository,omitempty" yaml:"repository,omitempty"`
	Manifests  []BinaryManifest `json:"manifests,omitempty" yaml:"manifests,omitempty"`
}

type BinaryManifest struct {
	Digest      digest.Digest     `json:"digest" yaml:"digest"`
	Annotations map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

type ClusterForge struct {
	TypeMeta `json:",inline" yaml:",inline"`
	Metadata Metadata         `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Spec     ClusterForgeSpec `json:"spec" yaml:"spec"`
}

type ClusterForgeSpec struct {
	Base       ClusterForgeBase     `json:"base" yaml:"base"`
	Components []ComponentSpec      `json:"components,omitempty" yaml:"components,omitempty"`
	Strategy   ClusterForgeStrategy `json:"strategy,omitempty" yaml:"strategy,omitempty"`
}

type ClusterForgeBase struct {
	Image string `json:"image" yaml:"image"`
}

type ComponentSpec struct {
	Name    string   `json:"name" yaml:"name"`
	Version string   `json:"version,omitempty" yaml:"version,omitempty"`
	Traits  []string `json:"traits,omitempty" yaml:"traits,omitempty"`
	Overlay string   `json:"overlay,omitempty" yaml:"overlay,omitempty"`
}

type ClusterForgeStrategy struct {
	AllowJITBuild bool `json:"allowJITBuild,omitempty" yaml:"allowJITBuild,omitempty"`
}

type Options struct {
	SourceOverlayPath string
	BinaryOverlayPath string
	AllowJITBuild     bool
}

type Plan struct {
	Cluster       *ClusterForge   `json:"cluster,omitempty" yaml:"cluster,omitempty"`
	SourceOverlay string          `json:"sourceOverlay,omitempty" yaml:"sourceOverlay,omitempty"`
	BinaryOverlay string          `json:"binaryOverlay,omitempty" yaml:"binaryOverlay,omitempty"`
	Components    []ComponentPlan `json:"components" yaml:"components"`
}

type ComponentPlan struct {
	Name             string           `json:"name" yaml:"name"`
	Version          string           `json:"version,omitempty" yaml:"version,omitempty"`
	NormalizedTraits []string         `json:"normalizedTraits,omitempty" yaml:"normalizedTraits,omitempty"`
	TraitsExpression string           `json:"traitsExpression,omitempty" yaml:"traitsExpression,omitempty"`
	SelectedOverlay  string           `json:"selectedOverlay,omitempty" yaml:"selectedOverlay,omitempty"`
	Resolution       ResolutionResult `json:"resolution" yaml:"resolution"`
}

type ResolutionResult struct {
	Mode             string            `json:"mode" yaml:"mode"`
	MatchedDigest    digest.Digest     `json:"matchedDigest,omitempty" yaml:"matchedDigest,omitempty"`
	ForgefilePath    string            `json:"forgefilePath,omitempty" yaml:"forgefilePath,omitempty"`
	ForgefileSummary *ForgefileSummary `json:"forgefileSummary,omitempty" yaml:"forgefileSummary,omitempty"`
}

type ForgefileSummary struct {
	PackageName          string   `json:"packageName,omitempty" yaml:"packageName,omitempty"`
	PackageVersion       string   `json:"packageVersion,omitempty" yaml:"packageVersion,omitempty"`
	BaseImage            string   `json:"baseImage,omitempty" yaml:"baseImage,omitempty"`
	Dependencies         []string `json:"dependencies,omitempty" yaml:"dependencies,omitempty"`
	EnabledPatchCount    int      `json:"enabledPatchCount,omitempty" yaml:"enabledPatchCount,omitempty"`
	EnabledTemplateCount int      `json:"enabledTemplateCount,omitempty" yaml:"enabledTemplateCount,omitempty"`
	EnabledCopyCount     int      `json:"enabledCopyCount,omitempty" yaml:"enabledCopyCount,omitempty"`
	EnabledScriptCount   int      `json:"enabledScriptCount,omitempty" yaml:"enabledScriptCount,omitempty"`
}
