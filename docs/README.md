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
| `oci-packer delete` (`rm`) | [delete.md](delete.md) | Delete an image from an OCI layout, garbage-collecting unshared blobs |
| `oci-packer list` (`ls`) | [list.md](list.md) | List images in an OCI layout, or the components of one Pack |

## Reference

- [Troubleshooting](troubleshooting.md) — symptoms, causes and fixes across all commands.

## Conventions

- **One scheme, `oci://`, for an image wherever it is**; the form says where:
  - a **registry**: `oci://<host>[:<port>]/<repo>:<tag>` — `oci://localhost/my/image:1.2.3`,
    `oci://localho.st/my/image:1.2.3`, `oci://docker.io/library/postgres:18-alpine`, `oci://localhost:5000/app:v1`;
  - a **local OCI layout**: `oci://<path>:<repo>:<tag>`, the path absolute or relative —
    `oci:///home/user/layout-dir:my/image:1.2.3`, `oci://layout-dir:my/image:1.2.3`, `oci://./layout-dir:my/image:1.2.3`.

  How the form is read: a path starting with `/`, `.` or `~` is a layout's. Otherwise the first segment (up to the
  first `/`) decides: `:<port>` — a registry; `:` followed by anything else — a layout (`layout-dir:my/image`); a dot,
  or `localhost` — a registry; anything else — a layout. A relative layout whose first segment looks like a host
  (`my.layouts/…`) is written with `./` in front. `cr://` is gone.
- **Global flags** (inherited by every command): `-v/--verbose`, `-l/--login`, `-p/--password`,
  `--plain-http`, `--insecure` (`--plain-http` and `--insecure` are mutually exclusive).
