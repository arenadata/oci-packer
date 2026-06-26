# oci-packer documentation

Documentation hub for the `oci-packer` CLI. Start at the
[project README](../README.md) for an overview and installation.

## Commands

| Command | Doc | Summary |
|---------|-----|---------|
| `oci-packer <ref>` (pack) | [pack.md](pack.md) | Build an OCI artifact from a declarative pack file and push it to a registry |
| `oci-packer copy` | [copy.md](copy.md) | Copy images/artifacts between a remote registry and an OCI layout (optionally one platform, optionally unpacked) |
| `oci-packer proxy` | [proxy.md](proxy.md) | Serve OCI artifacts over plain HTTP (Beta) |
| `oci-packer mount` / `umount` | [mount.md](mount.md) | Mount an image from an unpacked OCI layout read-only via overlayfs (Linux only) |

## Reference

- [Troubleshooting](troubleshooting.md) — symptoms, causes and fixes across all commands.

## Conventions

- **Registry references** use the `cr://` scheme: `cr://registry.example.com/repo/name:tag`.
- **OCI layout references** use the `oci://` scheme: `oci://<layout-dir>:<repo>/<name>:tag`.
- **Global flags** (inherited by every command): `-v/--verbose`, `-l/--login`, `-p/--password`,
  `--plain-http`, `--insecure` (`--plain-http` and `--insecure` are mutually exclusive).
