# Fixed offline operational kit loader

`Load(directory, pinnedManifestSHA) (*Kit, error)` verifies the first ordinary initial-Flood kit only. It does not execute Python, extract files, contact a target, install prerequisites, create inputs, or claim a scenario result.

The descriptor loader is implemented for Linux and macOS. Other platforms (including Windows) keep the CLI buildable but return `ErrUnsupportedPlatform` before filesystem access; Windows installation/run support is not claimed by this slice. Canonical JSON remains platform-independent.

The caller supplies a separately trusted lowercase manifest SHA256. The loader binds exactly ten flat regular files through a retained directory descriptor and `openat`/`O_NOFOLLOW`/`O_NONBLOCK` reads. Each read is capped at1MiB; the ten-file snapshot is capped at10MiB by construction. Root trailing slash/dot syntax is normalized before the no-follow open. Parent directory resolution follows normal OS path semantics; this is not a claim of exclusive ownership over the filesystem or a hostile writer's ancestors. After opening the directory, leaf lookups stay relative to that original descriptor. Only copied validated bytes are returned; later disk changes cannot change a loaded Kit.

The fixed manifest, eight artifact hashes/sizes, both sidecars, provenance path/hash syntax, exact fifteen-input scenario and budget, and full authored rule and Mapping are checked. Their qualified canonical v1 digests deliberately reject altered descriptions, omitted or extra fields, alternative profile programs and other patterns. The effective profile hash is the actual qualified Python compiler result for that fixed Mapping, not a Go implementation of the compiler. Validator source hashes are retained provenance, not proof the target is running those sources.

Inner project parsing allows only the three original directories, one Mapping file and pack.yml, with exact inventory and manifest-to-file binding. Gzip expansion is capped at4MiB, member reads at1MiB, and trailing compressed/nonzero tar content is refused. Rule ZIP parsing permits exactly one regular stored YAML member of at most256KiB with the exact rule filename, valid CRC and sizes. The single local ZIP filename, central directory and end record must agree, with no hidden prefix/trailer or unlisted records. Nothing is extracted. YAML aliases, anchors, duplicate keys, merge keys, nonstring keys, unsupported scalar types, multiple documents and nesting beyond32 are refused. JSON uses the same depth bound, valid UTF-8 and paired Unicode escapes, duplicate-key refusal and integers within ±(2^53−1); floats/exponents are refused.

Canonical JSON is UTF-8 sorted-key compact JSON plus LF, matching Python `ensure_ascii=False`. The private codec explicitly handles actual U+2028/U+2029 independently of literal backslash-u sequences; it never rewrites encoded strings blindly. `CanonicalJSON` is available for this kit's semantic readbacks and does not change the generic OSCAR codec. Backend semantic hashes omit the final LF, whereas the kit's Mapping/rule hashes include it.

## API

- `Bindings()` returns value-only identifiers and hashes: pack/version, pinned manifest, project archive/manifest/content, profile Mapping/effective hash, full authored rule, normalized Mapping state/row key/source file.
- `ManifestJSON()`, `ScenarioJSON()`, `ProfileJSON()`, `RuleJSON()`, `MappingStateJSON()` return detached canonical bytes including LF.
- `Artifact(name)` returns a copied file from the fixed snapshot; unknown paths are not read.
- `Files()` returns copied eight-artifact bindings.
- `Scenario()` returns a detached value with four fixed groups and budgets; `InputCount()` is15.

The normalized Mapping state and pack content/row-key hashes mirror the actual target installer `Bundle` contract. Tests include literal results from executing that genuine Python Bundle over the fixture. HTTP readers must still validate original installation evidence and live complete readbacks; these values grant no target authority or readiness.

## Genuine fixture and verification

`testdata/kit/` is an unchanged byte copy of the root-built artifact at backend excluded `.superpowers/sdd/2026-10-17-operational-reliability/test-pack-final/kit/`, reviewed installer checkpoint CorrTest12115fd. Manifest SHA256:

`44381fc9bdf13c1f04ea1244bc992b88ec23eedb45169163651d70a7dd01ca59`

All ten fixture files are required to exercise the real outer hash inventory. Included installer/builder files are inert artifact bytes; Go tests never execute them. The first successful fixture is not a synthetic tar/JSON model. Mutation tests operate only on independent temporary copies and rebind the outer pin where necessary to reach inner validation.

Host commands (cached toolchain, downloads disabled):

```
GOPROXY=off GOTOOLCHAIN=local go test ./internal/testpack -count=1
GOPROXY=off GOTOOLCHAIN=local go test -race ./internal/testpack -count=1
GOPROXY=off GOTOOLCHAIN=local go vet ./internal/testpack
```

No installed service, Linux container, CLI composition or end-to-end pattern proof is supplied by this package. The later run retains all external target ownership, controlled destination, live mode/readiness, exact source identity, audit observation and DIRTY cleanup prerequisites.
