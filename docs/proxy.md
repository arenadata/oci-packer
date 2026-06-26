# `proxy` command (Beta)

Run an HTTP server that exposes OCI artifacts from a registry over plain HTTP, so plain HTTP
clients (curl, browsers, package tools) can fetch blobs without an OCI client.

```
oci-packer proxy [flags] <cr://registry>
```

The single argument is the upstream registry reference (`cr://` scheme).

## Flags

| Flag | Description |
|------|-------------|
| `--addr` | Listen address (default `:8080`) |
| `--unpack` | Serve from an OCI layout with unpacked layers |
| `-l, --login` / `-p, --password` | Upstream registry credentials |
| `--plain-http` | Use plaintext HTTP to the upstream registry |
| `--insecure` | Allow TLS without certificate verification upstream |
| `-v, --verbose` | Verbose logging |

## Example

```bash
oci-packer proxy --addr :8080 cr://registry.example.com
```

> Beta: the request-to-artifact mapping and caching behaviour may change.

## See also

- [Documentation hub](README.md)
- [Troubleshooting](troubleshooting.md)
- [pack](pack.md) · [copy](copy.md) · [mount](mount.md)
