# ymlutils refactor design

**Date:** 2026-04-24

## Goal

Replace manual string-splitting YAML parsing in `internal/ymlutils` with `gopkg.in/yaml.v3`, fix known bugs, and introduce a pure-core / OS-wrapper split so unit tests can operate on in-memory content without touching disk.

## Bugs fixed

| Location | Bug |
|---|---|
| `ExtractStringValueFromYamlForGivenKey` | Splits value on `:` — breaks for values containing colons (e.g. image URLs) |
| `ExtractGvkFromYml` | Same split-on-colon bug on the `apiVersion` line |
| `AddSuffixToNameInManifests` | Fragile state-machine line parser — can misfire if `name:` appears outside `metadata` context |
| `UpdateChartVersion` | Text replacement would corrupt duplicate `version:` keys |

## Part 1 — Rewrite with yaml.v3

All parsing replaced with `gopkg.in/yaml.v3` (already a direct dependency).

### extractor.go

- `ExtractGvkFromYml(content string)` — already pure (no OS access). Unmarshal each `---`-separated document into `struct{ APIVersion, Kind string }`, then `schema.ParseGroupVersion`.
- `ExtractStringValueFromYamlForGivenKey(filePath, key string)` — reads file, delegates to pure inner `extractStringValue(data []byte, key string) (string, error)` which unmarshals into `map[string]interface{}` and returns `obj[key].(string)`.
- `GatherChartGvks` and `CopyManifestsFromYamlsIntoOneYaml` — no logic change; call rewritten helpers.

### renamer.go

- `AddSuffixToNameInManifests(dir, suffix string)` — walks dir, delegates each file to pure `addSuffixToNameInContent(data []byte, suffix string) ([]byte, error)` which unmarshals into `map[string]interface{}`, navigates `metadata.name` and `spec.group`, appends suffix, marshals back.
- `UpdateChartVersion(chartPath, newVersion string)` — reads `Chart.yaml`, delegates to pure `updateChartVersionInContent(data []byte, version string) ([]byte, error)` which unmarshals into `map[string]interface{}`, sets `version`, marshals back.

## Part 2 — IO abstraction layer

### Single-file functions (clean split)

Expose unexported pure-core functions that operate on `[]byte`:

| Public function | Pure inner | Testable with inline YAML? |
|---|---|---|
| `ExtractStringValueFromYamlForGivenKey` | `extractStringValue([]byte, string)` | Yes |
| `AddSuffixToNameInManifests` | `addSuffixToNameInContent([]byte, string)` | Yes |
| `UpdateChartVersion` | `updateChartVersionInContent([]byte, string)` | Yes |

### Directory-walking functions (fs.FS abstraction)

`GatherChartGvks` and `CopyManifestsFromYamlsIntoOneYaml` walk a directory tree. The abstraction is `fs.FS` (Go stdlib since 1.16):

- Public signatures accept an `fs.FS` instead of a path string.
- Production callers pass `os.DirFS(path)`.
- Unit tests pass `fstest.MapFS` with inline YAML content — no disk access.

Call-site change is one line each (wrap existing path with `os.DirFS`).

`ExtractGvkFromYml` is already pure — no change needed.

## Testing strategy

- New `internal/ymlutils/ymlutils_test.go` with unit tests for all pure-core functions using inline YAML strings and `fstest.MapFS`.
- Existing integration tests in `controllers/` unchanged — they exercise the public OS-level API end-to-end.

## Branch

Rename current branch `flaky-investigation` → `refactor-ymlutils-yaml-v3`.

## Files changed

- `internal/ymlutils/extractor.go` — rewrite
- `internal/ymlutils/renamer.go` — rewrite
- `internal/ymlutils/ymlutils_test.go` — new unit tests
- Call sites: `controllers/btpoperator_controller.go`, `controllers/btpoperator_controller_updating_test.go`, `controllers/utils_test.go` — update `GatherChartGvks` and `CopyManifestsFromYamlsIntoOneYaml` signatures to pass `os.DirFS`
