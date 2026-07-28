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
| `-j, --parallel` | Number of layers to transfer at a time (default `4`; `1` copies them one by one) |
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

## Parallel transfers

Layers, configs and manifests are transferred concurrently. `-j` is the ceiling on how much of the
copy is in flight at once — reading a manifest to walk it counts against it just as moving a layer
does, so a wide multi-platform index never opens more connections than you asked for. Since a copy
spends its time waiting on the network or the disk rather than on the CPU, raising `-j` is what
makes a large image land faster; lower it when a registry rate-limits you or the machine is short
on bandwidth.

```bash
oci-packer copy -j 16 cr://registry.example.com/example/service:v1 oci://./layout:example/service:v1
oci-packer copy -j 1  cr://registry.example.com/example/service:v1 oci://./layout:example/service:v1  # one at a time
```

Three guarantees hold whatever `-j` is set to:

- **A manifest or index is only written once every blob it references is in place**, so the
  destination never points at content that has not arrived yet.
- **Each digest is copied once per copy.** A layer shared by several manifests of a
  multi-platform index is fetched and written a single time, not once per manifest.
- **Manifests are checked against their digest as they are read.** A source that answers with
  content that does not hash to what was asked for is refused rather than followed.

If a transfer fails, the copy stops: the remaining transfers are cancelled, the partially written
blob or half-extracted layer directory is removed, and the destination tag is left untouched.
Blobs that did finish stay in the layout, so re-running the copy resumes rather than starting over.

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
