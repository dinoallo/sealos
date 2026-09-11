# Source Cache Optimization

## Summary

Optimize the source cache layer used by `sealos distribution install` and `sealos build` to support granular invalidation, offline resilience, cache integrity checks, and an explicit cache management interface.

## Motivation

The current source cache implementation creates a monolithic Git checkout per repository URL+ref and lacks validation before building. Key failure modes observed in real testing:

1. Deleting a single package's source directory (e.g., `packages/kubernetes/v1.28.15`) produces `chdir: no such file or directory` because the whole checkout is shared and no pre-build validation runs.

2. When the Git source cache (`~/.cache/sealos/sources/<per-package-hash>`) is incomplete (directory exists but `.git` is missing), the error message says "remove it and retry" but provides no automated recovery.

3. Offline mode uses the same cache path as online mode; partial cache corruption makes `--offline` unusable.

4. No command exists to inspect or clean individual cache entries.

5. OCI image cache (`build_cache`) has no correlation with source cache; stale source code still uses cached OCI layers.

6. Repository cache staleness is time-based (24h TTL) rather than content-based, producing unnecessary clones in CI-like workflows.

## Design

### 1. Cache Layout

Introduce a layered cache under `$XDG_CACHE_HOME/sealos/` (default `~/.cache/sealos/`):

```
~/.cache/sealos/
  repositories/            # (unchanged) per-repo bare-like checkouts
    <sha256(url + ref)[:8]>/
      .git/
      distributions/
      packages/
  builds/                  # NEW: per-package build artifacts
    <sha256(package ref + source path/url/ref)>/
      source/              # shallow clone or bind mount of source
      result/              # OCI image tarball or manifest digest
  state/                   # NEW: cache metadata
    repository.json        # per-repo sync metadata
    integrity.json         # per-package cache integrity records
```

Key change: separate _source checkout_ (shared read-many from repo cache) from _build context_ (per-package isolated working tree). The repo cache is kept as a full Git checkout that can be shared across all packages; individual builds use `git worktree add` rather than per-package git clones.

### 2. Integrity Checks Before Build

Before invoking `sealos build`, validate that all source files declared by the package's `source.file` actually exist.

**Current flow** (simplified):

```
load package manifest
→ resolve sources
→ select first usable source
→ buildPlanForSource → returns BuildCommand{Name: "sealos build", Dir: <source>}
→ execute BuildCommand
→ chdir fails with opaque error
```

**Proposed flow:**

```
load package manifest
→ resolve sources
→ validate selected source:
    - if SourceType=local: check filepath exists, is dir, contains .git or required build files
    - if SourceType=git: check sourceCacheReady, validate checkout HEAD matches expected ref
→ if validation fails:
    - online mode: re-sync repository, re-checkout source
    - offline mode: fall back to next source candidate (if available) or fail with actionable error
→ buildPlanForSource
→ execute BuildCommand
```

### 3. Granular Cache Invalidation

Add three invalidation levels:

| Scope | Trigger | Action |
|---|---|---|
| Single package | `sealos repo cache clean kubernetes@v1.28.15` | Remove that package's build context and mark its source for re-checkout |
| Single distribution | `sealos repo cache clean platform@v0.1.0` | Remove all packages referenced by the distribution |
| All | `sealos repo cache clean --all` | Full re-sync of repository cache and all build contexts |

**Cache key for per-package build context:**

```
cache_key = SHA256(package.Name + "@" + package.Version + "\x00" +
                   source.URL + "\x00" + source.Ref + "\x00" +
                   source.Context + "\x00" + source.File)
```

This ensures that any change to the source configuration produces a new cache entry. Old entries can be pruned by `sealos repo cache clean` or automatically when disk usage exceeds a configurable threshold.

### 4. Per-Package Source Checkout Using `git worktree`

Instead of cloning per-package Git sources independently, use `git worktree add` from the shared repository cache:

```
# repo cache already exists at ~/.cache/sealos/repositories/<hash>/
# for a package with source.type=git, source.url=<repo-url>, source.ref=<ref>:
git -C <repo-cache> fetch origin <ref>
git -C <repo-cache> worktree add --detach <build-context-dir> FETCH_HEAD
```

Benefits:
- Single fetch per remote URL+ref, regardless of how many packages reference it.
- Worktrees share object storage; no redundant data transfer.
- Each worktree is disposable; deleting it doesn't affect other packages.
- Worktree creation is O(1) after fetch.
- `git worktree prune` can clean orphaned worktrees.

**Migration path:**
- Phase 1: add `git worktree add` as the default for Git sources, keep existing per-package clone as a fallback for environments where `git worktree add` is unavailable (old Git versions).
- Phase 2: remove per-package clone path.

### 5. Content-Based Staleness Check

Replace the 24-hour TTL with a remote ref comparison:

```
HEAD commit at last sync → git ls-remote origin <ref> → compare SHAs
```

For `--offline` mode, add a `RepositoryMaxAge` fallback of 7 days (instead of breaking immediately) with a warning.

**Repository Config changes:**

```go
type RepositoryConfig struct {
    // existing fields...
    MaxAge     time.Duration `json:"maxAge,omitempty" yaml:"maxAge,omitempty"` // user-configurable; 0 means default
    AutoClean  bool          `json:"autoClean,omitempty" yaml:"autoClean,omitempty"` // clean orphaned worktrees after sync
}
```

### 6. Cache Management CLI

Extend the `sealos repo` command group:

```
sealos repo cache status          # list cache entries, sizes, staleness
sealos repo cache clean [ref...]  # remove build cache for specific packages
sealos repo cache clean --all     # full rebuild
sealos repo cache clean --stale   # remove stale entries only
sealos repo cache verify          # check integrity of all cached sources
```

`sealos repo cache verify` should:
1. For each package build context, check that the source files exist.
2. For Git-based sources with remote access, verify that HEAD matches the remote ref.
3. Report missing, corrupt, or outdated entries.
4. Exit non-zero if any issues are found.

### 7. OCI Image Cache Correlation

When a package source changes, the previously built OCI image should be invalidated. Approach:

**Option A (recommended):** Tag built images with a content-addressable suffix derived from the source cache key:

```
ghcr.io/dinoallo/sealos-platform/kubernetes:v1.28.15-<cache-key-prefix>
```

Before building, check if an image with this tag already exists. If so, skip the build. When source changes, the cache key changes, and a new image is built with a different tag. The old image is pruned by `build-cache clean`.

**Option B (simpler):** Store the source cache key in the OCI image label:

```dockerfile
LABEL sealos.io/source-cache-key="<sha256>"
```

During install, if the running image's label matches the current source cache key, skip the build. This avoids changing the image tag scheme but requires the installer to inspect image labels.

Recommend Option A because it doesn't require inspecting image metadata and works naturally with existing `sealos build` caching.

### 8. `--offline` Resilience

Current behavior: `openCachedRepository` fails immediately if the repository cache is missing or invalid.

Proposed behavior:

| Cache State | Online Mode | Offline Mode |
|---|---|---|
| Valid, recent | Use cache | Use cache |
| Valid, stale | Sync then use | Use cache with warning |
| Missing/Invalid | Clone/sync | Return actionable error: `repository cache not found; run without --offline first` |
| Partial (package source missing) | Sync repo, re-checkout | Fail with specific missing package path |

### 9. Error Messages

Replace opaque `chdir: no such file or directory` errors with clear, actionable messages:

| Condition | New Error Message |
|---|---|
| Source checkout directory missing | `package source for <ref> is missing from the repository cache; run without --offline or re-sync with 'sealos repo sync'` |
| Source checkout incomplete | `package source cache for <ref> is incomplete; run 'sealos repo cache clean <ref>' and retry` |
| Build context missing in offline mode | `build context for <ref> is not cached; run without --offline first, or use --package-mode=remote` |

### 10. Implementation Plan

#### Phase 1 (minimal, high-impact)

- Add pre-build path validation in `BuildPlanWithOptions`.
- Improve error messages for missing/incomplete source checkout.
- Add `sealos repo cache status` and `sealos repo cache clean [ref...]` commands.
- Add content-based staleness check for repository sync.

#### Phase 2 (structural)

- Replace per-package Git clone with `git worktree add` from shared repository checkout.
- Add OCI image cache correlation using content-addressable tags.
- Add `sealos repo cache verify` command.

#### Phase 3 (polish)

- Automatic cache cleanup based on disk usage threshold.
- Concurrent build safety (locking for shared repository checkout).
- Telemetry for cache hit/miss rates.

## Backward Compatibility

- Phase 1: fully backward compatible. Existing cache directories continue to work.
- Phase 2: `git worktree add` requires Git >= 2.5.0 (2015). Older Git versions fall back to per-package clone.
- Phase 3: `sealos repo cache` commands are additive; existing `build_cache clean` continues to work.
- The new content-addressable image tags in Option A change the tag scheme; the old tags must continue to be checked as a fallback during migration.

## Open Questions

1. Should `sealos repo cache clean --all` also trigger `buildah build-cache clean` for orphaned OCI images, or leave that to a separate command?
2. Should the per-package cache key include platform (`--platform`) or architecture information?
3. For `git worktree add`, should we pin to a specific commit or follow the ref (allowing automatic updates on re-sync)?
4. Should the cache integrity file be a JSON log (append-only) or a SQLite database (queryable)?

## References

- `lifecycle/pkg/distribution/repository.go` — repository sync, cache path resolution, open/validate
- `lifecycle/pkg/distribution/package.go` — source resolution, build plan, `sourceCacheReady`, `BuildPlanWithOptions`
- `lifecycle/pkg/cloud/install/install.go` — `Config.SourceCache`, `Config.SourceRoot`, package resolution during install
- `lifecycle/pkg/buildah/build_cache.go` — build cache clean/prune
- `lifecycle/cmd/sealos/cmd/repository.go` — repository CLI commands
