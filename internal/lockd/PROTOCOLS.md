# Scale protocol bindings

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
