# sysc-lock Model Effects Implementation Plan

> **For Claude: REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.**

**Goal:** Make the approved shared model effects selectable and renderable in sysc-lock, then prepare and publish sysc-lock 0.1.2 and update its AUR package.

**Architecture:** `internal/config.Effects` already reads the catalog from `sysc-terminal/renderer`; keep that ownership and pin sysc-terminal to the reviewed main commit plus sysc-Go's reviewed model-shading commit. Add one CPU-path regression test that checks the approved IDs through the config catalog and draws a frame for each model. Release after review and merge, then update the AUR source package to the published tag.

**Tech Stack:** Go 1.26 modules, GitHub Actions release workflow, Arch `makepkg` and AUR Git.

---

### Task 1: Prove model availability through sysc-lock's catalog and CPU renderer

**Files:**
- Modify: `internal/lockd/effect_backend_test.go`
- Create: `docs/plans/2026-10-23-sysc-lock-model-effects.md`

**Steps:** Add a test for `l2a1-sar`, the 16 approved statues and monuments, and four fighter jets. For each ID, require membership in `config.Effects()`, successful config validation, successful CPU backend construction, and a successful frame draw. Run `go test ./internal/lockd -run TestApprovedModelEffectsRenderOnCPU -count=1` and confirm the old module pins fail on a missing model.

### Task 2: Pin the reviewed shared effect catalog and model renderer

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`

Pin sysc-terminal to `v0.2.2-0.20261022013002-57dd64b663ae` and sysc-Go to `v1.0.9-0.20261022080948-51b43b6e52a7`. Re-run the focused regression, package tests, `go test ./...`, `go vet ./...`, and `go build ./...`. Confirm `sysc-lock --describe` lists the approved IDs and keeps `ak-47-rama` and `beams` excluded.

### Task 3: Prepare sysc-lock 0.1.2

**Files:**
- Modify: `cmd/sysc-lock/main.go`
- Modify: `cmd/sysc-lock/release_test.go`

Set the binary version and release contract to `0.1.2`. Review the complete diff and merge the reviewed change into `master`.

### Task 4: Publish and update AUR

Create and push tag `v0.1.2`; verify the release workflow and downloadable assets. In a clean AUR checkout, update `PKGBUILD` and `.SRCINFO` to the `v0.1.2` source archive, verify its checksum, build and inspect the package, then push the package update to AUR.
