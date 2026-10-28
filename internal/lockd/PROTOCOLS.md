# Wayland protocol bindings

## wp_presentation

`presentationtime/presentation_time.go` is generated from the stable
presentation-time protocol in wayland-protocols 1.49. Its XML SHA-256 is
`dffac93bcb2bb1d8c385e72b8a8c2c0d4d79a336866322f3ba886dce2b27b1e2`.
Regenerate from this directory with the pinned runtime module's scanner:

```sh
go run github.com/Nomadcxx/sysc-wayland/cmd/sysc-wayland-scanner@v0.3.3 -pkg presentationtime -o presentationtime/presentation_time.go -i ../../protocols/presentation-time.xml
```

The binding is optional at runtime. When advertised, its v2 `refresh` estimate
quantizes effect deadlines, including the fastest-rate estimate used for
variable-refresh outputs. A zero estimate falls back to the configured timer
cadence.

## Scale protocols

These bindings copy sysc-terminal commit
`4e522749ac8be37808e96bd0997824d04dd81f2a` without changing generated code.
The generated headers retain the upstream MIT notices.

Sources: wayland-protocols tag 1.49, staging/fractional-scale and
stable/viewporter. The pinned terminal module includes provenance comments;
its checked-in XML SHA-256 values are:

- fractional-scale-v1.xml: `3de083b4fc80b50e177e3f42ac5231ae6ee58bb568591581ffe58f567a0c6033`
- viewporter.xml: `92e4a659ad61ec43545cdac1643d102266cce820669aa797dfb1390601d5f622`

Regenerate from this directory with the original scanner version:

```sh
source_dir=$(go list -m -f '{{.Dir}}' github.com/Nomadcxx/sysc-terminal)
go run github.com/Nomadcxx/sysc-wayland/cmd/sysc-wayland-scanner@v0.3.1 -pkg fractionalscale -o fractionalscale/fractional_scale.go -i "$source_dir/protocols/fractional-scale-v1.xml"
go run github.com/Nomadcxx/sysc-wayland/cmd/sysc-wayland-scanner@v0.3.1 -pkg viewporter -o viewporter/viewporter.go -i "$source_dir/protocols/viewporter.xml"
```

Review the generated diff and run the lockd checks. Keep sysc-lock's existing
Wayland runtime pin when regenerating.

## zwlr-screencopy-unstable-v1

`protocols/wlr-screencopy-unstable-v1.xml` is copied verbatim from the
sysc-shell repository (SHA-256
`131b8f9b4aad0c8a9cf705e90d2a1511a5ca0c477637fd3400cf1cc4fa963fb8`). The
binding is version 3, used only to grab one pre-lock picture per output with
`overlay_cursor` set to 0; a compositor without the global simply keeps the
plain fallback. Regenerate with:

```sh
go run github.com/Nomadcxx/sysc-wayland/cmd/sysc-wayland-scanner@v0.3.1 -pkg screencopy -o screencopy/screencopy.go -i ../../protocols/wlr-screencopy-unstable-v1.xml
```
