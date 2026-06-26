# `pack` command (default)

The root command builds an OCI artifact from a declarative **pack file** and pushes it to a
container registry.

```
oci-packer -f <pack-file> [flags] <cr://registry/repo:tag>
```

`<reference>` is the push destination and must use the `cr://` scheme.

## What it does

```
pack file (YAML) → validate → per-item handler → manifest or index → push → registry
    file://    → file as a layer
    dir://     → each file in the directory as a layer
    http(s):// → download, then add as a layer
    cr://      → mount layers from another repository (index mode)
```

A flat **manifest** is produced unless any item has a `platform` field or a registry
(`cr://`) source, in which case a multi-platform **index** is produced.

## Flags

| Flag | Description |
|------|-------------|
| `-f, --file` | Path to the pack file (**required**) |
| `--tmp-dir` | Directory for HTTP downloads (default: system temp) |
| `-l, --login` | Registry username |
| `-p, --password` | Registry password |
| `--plain-http` | Allow plaintext HTTP to the registry (no TLS) |
| `--insecure` | Allow TLS without certificate verification |
| `-v, --verbose` | Verbose (debug) logging |

`--plain-http` and `--insecure` are mutually exclusive.

## Pack file

The pack file declares the artifact type, annotations, an optional config descriptor and the
list of items to package. The full format (fields, source-scheme table, multi-platform
examples) is documented in the project README:
[Pack file format](../README.md#формат-pack-файла).

Minimal example:

```yaml
type: application/vnd.example.artifact
annotations:
  org.opencontainers.image.title: "My artifact"
items:
  - from: file://config.tmpl
    type: application/vnd.example.template
  - from: dir://data/
```

## Examples

```bash
# Pack and push
oci-packer -f artifact.yaml cr://registry.example.com/myartifact:v1.0

# With a custom temp directory for HTTP downloads
oci-packer -f artifact.yaml cr://registry.example.com/myartifact:v1.0 --tmp-dir /tmp/packer

# Against an insecure (plain HTTP) registry, with credentials
oci-packer -f artifact.yaml cr://localhost:5000/myartifact:v1.0 \
  --plain-http -l user -p pass
```

## See also

- [Documentation hub](README.md)
- [Troubleshooting](troubleshooting.md)
- [copy](copy.md) · [proxy](proxy.md) · [mount](mount.md)
