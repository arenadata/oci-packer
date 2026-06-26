# Troubleshooting

Symptoms, causes and fixes across all `oci-packer` commands. See the
[documentation hub](README.md) for per-command references.

- [General](#general)
- [pack](#pack)
- [copy](#copy)
- [proxy](#proxy)
- [mount / umount](#mount--umount)

---

## General

### `scheme required` / `scheme unsupported`

A registry reference was passed without the `cr://` scheme, or with an unsupported one.
Registry references must be `cr://host/repo:tag`; OCI layout references must be
`oci://<dir>:<repo>:<tag>`.

```bash
# wrong
oci-packer -f a.yaml registry.example.com/app:v1
# right
oci-packer -f a.yaml cr://registry.example.com/app:v1
```

### Authentication / `401 Unauthorized`

Supply registry credentials:

```bash
oci-packer copy -l user -p pass cr://registry.example.com/app:v1 oci://./layout:app:v1
```

### TLS / transport errors (`http: server gave HTTP response to HTTPS client`, `x509: certificate signed by unknown authority`)

These are transport problems, not authentication. Choose the matching flag for how the
registry is served:

```bash
oci-packer ... --plain-http      # registry speaks plain HTTP (no TLS)
oci-packer ... --insecure        # HTTPS, but skip TLS certificate verification (self-signed)
```

`--plain-http` and `--insecure` are mutually exclusive.

---

## pack

### `pack file must contain at least one item`

The pack file has an empty or missing `items:` list. Add at least one item.

### `'<src>' does not exist` / `is a directory` / `is not a directory`

An item's `from:` points to a path that does not match its scheme. Check `file://` points to a
file and `dir://` to a directory, and that the path is correct relative to the working
directory.

### `unsupported source type`

An item uses a scheme the builder does not handle. Supported item sources: `file://`,
`dir://`, `http://`, `https://`, and `cr://` (registry, for index/mount mode).

---

## copy

### `no manifest in index matches the requested platform`

`--platform` did not match any manifest in the source index. Verify the value
(`os/arch[/variant]`, e.g. `linux/amd64`, `linux/arm64/v8`) and that the image actually
publishes it. Omit `--platform` to copy the full multi-platform index.

### Destination layout is an index, but I wanted a single image

Use `--platform` to select one platform — the destination then gets a single image manifest
instead of an index.

---

## proxy

### Upstream errors / empty responses

The proxy forwards to the upstream registry; check credentials and transport flags
(`-l/-p`, `--plain-http`, `--insecure`) and that the upstream reference is reachable. The
proxy is Beta; mapping and caching behaviour may change.

---

## mount / umount

### After `mount`, `systemctl`/`reboot` report "System has not been booted with systemd"

**Symptom**

```
# ./oci-packer mount oci://packer:postgres:18 /mnt
# reboot
System has not been booted with systemd as init system (PID 1). Can't operate.
Failed to connect to bus: Host is down
```

`ps -p 1 -o comm=` still shows `systemd`, yet `systemctl` thinks systemd is not running.

**What happened**

`systemctl` decides whether systemd booted the host by checking `/run/systemd/system/`.
A tmpfs was mounted over the host's real `/run`, shadowing that directory.

The cause is an absolute symlink inside the image. On Debian/Ubuntu-based images `/var/run`
is a symlink to `/run`. When the kernel resolves a mount target like `/mnt/var/run`, it
follows that symlink; because it is **absolute** (`/run`), resolution restarts from the
namespace root — the host's `/run`, not `/mnt/run`. A tmpfs aimed at `/mnt/var/run` therefore
lands on the host `/run`. (A relative symlink such as `../run` would have stayed inside `/mnt`.)

**Diagnose**

```bash
cat /proc/mounts | grep -E ' /run | /mnt'   # a stray "tmpfs /run tmpfs ..." line is the smoking gun
ls -ld /run/systemd/system                  # "No such file or directory" confirms /run is shadowed
```

**Recover**

```bash
umount /run            # remove the stray tmpfs (umount -l /run if it is busy)
./oci-packer umount /mnt
ls -ld /run/systemd/system   # should reappear
systemctl is-system-running
```

If `systemctl` is still unusable and you must reboot, use a path that bypasses it:

```bash
reboot -f                     # forced reboot(2), skips systemd
# or, as a last resort: echo b > /proc/sysrq-trigger
```

**Why current versions are safe**

This is fixed: `/var/run` was removed from the auto-tmpfs set (it is always a symlink to
`/run`), auto-tmpfs is now resolved *after* the overlay is mounted so symlinks are visible,
and every tmpfs/bind target is checked with `EnsureWithin` — any target that resolves outside
the mount point (`/mnt`) is skipped (tmpfs) or rejected (bind). Rebuild from a current source
tree (`go build ./cmd/oci-packer`) to get the guard.

### `layout is not in unpack mode; re-copy with --unpack`

The layout stores layers as compressed blobs; `mount` needs unpacked directories. Re-create it
in unpack mode — either from the registry, or by repacking the existing layout into a new one
(no registry round-trip):

```bash
# from the registry
oci-packer copy --unpack cr://registry/example/service:v1 oci://./layout:example/service:v1

# or repack an existing tar-mode layout into a new unpacked one
oci-packer copy --unpack oci://./layout:example/service:v1 oci://./unpacked:example/service:v1
```

A layout's mode is fixed at creation, so the destination must be a **new** layout.

### `reference is not a container image (config media type "…")`

The reference points to a non-image OCI artifact (e.g. something produced by `oci-packer
pack`). Only container images have an overlay-mountable filesystem. Point `mount` at an image
reference: `oci://<layout-dir>:<repo>:<tag>`.

### `no manifest in index matches host platform linux/amd64`

The layout holds a multi-platform index without a manifest for the host architecture. Copy the
matching platform (or the full index) before mounting:

```bash
oci-packer copy --platform linux/amd64 cr://registry/app:v1 oci://./layout:app:v1
```

### `lowerdir option string … exceeds kernel limit`

The image has too many layers for a single overlay mount (the option string is bounded by the
kernel page size). Squash the layers, e.g. by re-copying, then mount the result.

### `umount` fails with `EBUSY`

A process still has the mount open. Find and stop it (`fuser -m /mnt`, `lsof /mnt`), or detach
lazily:

```bash
./oci-packer umount --lazy /mnt
```

### `operation not permitted` on mount

`mount`/`umount` require root (`CAP_SYS_ADMIN`) and Linux. Run with `sudo` on a Linux host.
