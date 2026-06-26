# `copy` command

Copy images and artifacts between remote registries and local OCI layouts.

```
oci-packer copy [flags] <src> <dst>
```

Each endpoint is determined by its scheme; **any combination is allowed**, including
registry→layout, layout→registry, registry→registry and layout→layout.

| Endpoint | Scheme | Example |
|----------|--------|---------|
| Remote registry | `cr://` | `cr://registry.example.com/repo/name:tag` |
| OCI layout | `oci://` | `oci://./layout:repo/name:tag` (`oci://<dir>:<repo>:<tag>`) |

## Flags

| Flag | Description |
|------|-------------|
| `--unpack` | Write an OCI layout whose layers are unpacked into directories (required for `mount`) |
| `--platform` | Copy only one platform from a multi-platform image, e.g. `linux/amd64` |
| `-l, --login` / `-p, --password` | Registry credentials |
| `--plain-http` | Allow plaintext HTTP to the registry |
| `--insecure` | Allow TLS without certificate verification |
| `-v, --verbose` | Verbose logging |

`--plain-http` and `--insecure` are mutually exclusive.

## Platform selection

Without `--platform`, a multi-platform image is copied in full (the index and every child
manifest). With `--platform os/arch[/variant]`, the matching child manifest is selected from
the index and a **single-platform image** is written to the destination. The platform string
comes from the flag, not from the host, so you can copy `linux/arm64` from a `windows/amd64`
machine.

## Examples

```bash
# Registry → OCI layout
oci-packer copy \
  cr://registry.example.com/example/service:v1 \
  oci://./layout:example/service:v1

# Registry → unpacked OCI layout (mountable), single platform
oci-packer copy --unpack --platform linux/amd64 \
  cr://registry.example.com/example/service:v1 \
  oci://./layout:example/service:v1

# OCI layout → registry
oci-packer copy \
  oci://./layout:example/service:v1 \
  cr://registry.example.com/example/service:v1

# OCI layout → unpacked OCI layout (repack an existing tar-mode layout for mount)
oci-packer copy --unpack \
  oci://./layout:example/service:v1 \
  oci://./unpacked:example/service:v1
```

## Unpack and repack

`--unpack` describes how the **destination** layout stores layers (unpacked directories vs.
compressed blobs); the source is read in whatever mode it already is. The destination layout
must be **new** — a layout's mode is fixed at creation.

This makes `copy` the way to repack a tar-mode layout into a mountable one without going back
to the registry:

```bash
oci-packer copy --unpack oci://./tar-layout:app:v1 oci://./unpacked:app:v1
oci-packer mount oci://./unpacked:app:v1 /mnt    # now mountable
```

## See also

- [Documentation hub](README.md)
- [Troubleshooting](troubleshooting.md)
- [mount](mount.md) — consumes an `--unpack` layout
- [pack](pack.md) · [proxy](proxy.md)
