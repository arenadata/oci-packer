# `mount` and `umount`

> ⚠️ Linux only, and root — the mount is made in the host's mount namespace, which takes
> `CAP_SYS_ADMIN` there. overlayfs itself no longer needs real root; see
> [Limitations](#limitations-and-pitfalls) for what that does and does not buy you.

## What it is for

`oci-packer mount` lets you use the contents of an OCI image as an ordinary filesystem directory — **without starting a
container and without unpacking the image into a single large tree**.

The layers of an unpacked OCI Layout are stacked on top of each other through the kernel's **overlayfs** in read-only
mode. This gives you:

- **Instant access** to the image contents as a mounted rootfs — no data is copied into a separate store. The OCI Layout
  itself remains the backing store.
- **Space savings**: multiple mounts of the same image share the same layer files.
- **Immutability**: the rootfs is read-only, so the source image cannot be corrupted by accident. Writes are isolated to
  tmpfs or to explicitly declared bind directories.

Typical use cases:

- Inspecting image contents (`ls /mnt/app`, reading configs and binaries).
- Using an image rootfs as a read-only base for an application whose writable data lives in separate volumes — the same
  model Kubernetes uses (read-only rootfs + volume mounts).
- Mounting an image persistently at boot via systemd.

## How it works

```
oci-packer copy --unpack cr://registry/example/service:v1 oci://./layout:example/service:v1
        │
        ▼  image layers are unpacked into blobs/sha256/<hex>/ directories
   ┌─────────────────────────────────────────────────────┐
   │ OCI Layout (unpack mode) — may hold many images      │
   │   example/service:v1   → image manifest              │
   │   example/worker:v2    → image manifest              │
   │   blobs/sha256/aaa…/   ← layer 1 (bottom)            │
   │   blobs/sha256/bbb…/   ← layer 2                     │
   │   blobs/sha256/ccc…/   ← layer 3 (top)               │
   └─────────────────────────────────────────────────────┘
        │
        ▼  oci-packer mount oci://./layout:example/service:v1 /mnt/app
   mount -t overlay overlay -o lowerdir=ccc:bbb:aaa,ro /mnt/app
   mount -t tmpfs   tmpfs    /mnt/app/tmp
   mount -t tmpfs   tmpfs    /mnt/app/run
   mount -t tmpfs   tmpfs    /mnt/app/var/tmp
   mount --bind     /srv/data /mnt/app/var/lib/app   (if --bind is given)
```

1. **Image selection.** A layout can hold many images (and non-image artifacts), so `src`
   must name a single image inside the layout — `oci://<layout-dir>:<repo>:<tag>`. The reference is matched against the
   image's `org.opencontainers.image.ref.name`. If it resolves to an OCI Index (multi-platform image), the manifest
   matching the host platform is selected automatically (`containerd/platforms`). Non-image artifacts are rejected.
2. **Layer collection.** The layer list is taken from the manifest; each layer maps to an unpacked directory in the
   layout. The layer order (bottom-to-top) is reversed into overlayfs order (`lowerdir` is listed top-to-bottom).
3. **Read-only overlay.** Without `upperdir`/`workdir`, overlayfs mounts read-only, and
   `MS_RDONLY` goes in with the syscall that creates the mount rather than a later remount — a mount that is briefly
   live without its flags is a window an image's setuid binary can be executed through.
4. **tmpfs for volatile paths.** `/tmp`, `/run`, `/var/tmp` are mounted as writable tmpfs so applications writing to
   those directories don't hit `EROFS`.
5. **Bind directories.** Each `--bind <host>:<path>` is mounted over the rootfs as a writable point (the Kubernetes
   model).

### Single-layer images

overlayfs refuses a single `lowerdir` with no `upperdir` (*"at least 2 lowerdir are needed while upperdir
nonexistent"*), and single-layer images — busybox, alpine, most minimal bases — are the common case rather than an edge
one. Such a mount gets an **empty directory as its bottom layer**, so the real layer still wins every path:

```
mount -t overlay overlay -o lowerdir=/mnt/app.empty-lower:aaa…,ro /mnt/app
                                     └─ created next to the mount point, removed by umount
```

The empty layer exists so that both cases are one atomic mount. The obvious alternative — bind- mounting the lone layer
directory — cannot carry per-mount flags: the kernel ignores
`MS_NOSUID`/`MS_NODEV` on the call that creates a bind and honours them only on a following
`MS_REMOUNT|MS_BIND`, leaving the same window described above. `oci-packer umount` removes the
`<dst>.empty-lower` directory after unmounting; it is only ever empty, so nothing can be lost with it.

## Source reference format

A single OCI Layout directory can contain many images and other OCI artifacts. The `src`
reference therefore has three parts:

```
oci://<layout-dir>:<repository>:<tag>
       ▲            ▲            ▲
       │            │            └─ image tag (or @sha256:… digest)
       │            └─ repository name of the image inside the layout
       └─ path to the OCI Layout directory (relative or absolute)
```

Examples:

| Reference                           | Layout dir    | Image inside layout    |
|-------------------------------------|---------------|------------------------|
| `oci://./layout:example/service:v1` | `./layout`    | `example/service:v1`   |
| `oci:///srv/images:team/app:2.3`    | `/srv/images` | `team/app:2.3`         |
| `oci://./layout:nginx@sha256:abc…`  | `./layout`    | `nginx` at that digest |

Everything up to the first `:` is the layout directory; the remainder is the image reference within that layout, matched
against the manifest's `org.opencontainers.image.ref.name`
annotation (the same value `oci-packer copy` writes as the destination tag).

## Source requirements

- **Must be an image.** Only container images can be mounted — their layers describe a filesystem. Arbitrary OCI
  artifacts (custom configs, data blobs produced by `oci-packer
  pack`) are rejected:

  ```
  reference is not a container image (config media type "…"); only images can be mounted
  ```

- **Must be in unpack mode** (`oci-packer copy --unpack ...`). In this mode layers are stored as unpacked directories
  that overlayfs uses directly. A regular (tar) layout cannot be mounted — you will get:

  ```
  layout is not in unpack mode; re-copy with --unpack
  ```

- **File ownership follows whoever unpacked it.** `copy --unpack` restores each entry's owner from the layer tar when it
  runs as root, and flattens ownership to the invoking user when it does not — an unprivileged process holds no
  `CAP_CHOWN`, so the alternative is failing on the first root-owned file. Device nodes in a layer are skipped in that
  mode for the same reason (`CAP_MKNOD`). Since `mount` itself runs as root, a layout unpacked rootless mounts fine but
  presents every file as owned by the user who unpacked it — which matters for applications that check ownership of
  their own data directory.

## `mount` — usage

```
oci-packer mount [flags] <src> <dst>
```

| Argument / flag   | Example                             | Description                                                                   |
|-------------------|-------------------------------------|-------------------------------------------------------------------------------|
| `<src>`           | `oci://./layout:example/service:v1` | Image inside an unpack-mode OCI Layout (`oci://<dir>:<repo>:<tag>`)           |
| `<dst>`           | `/mnt/app`                          | Mount point (merged rootfs)                                                   |
| `--bind`          | `--bind /srv/data:/var/lib/app`     | Bind-mount a writable host directory; repeatable                              |
| `--no-auto-tmpfs` |                                     | Do not mount tmpfs automatically                                              |
| `--tmpfs-size`    | `--tmpfs-size /tmp:512m,/run:64m`   | Per-path tmpfs size; `<path>:<size>`, repeatable and/or comma-separated       |
| `--verify`        |                                     | Verify layer digests before mounting (tar-mode layouts only, see Limitations) |
| `--persistent`    |                                     | Generate systemd.mount units instead of mounting immediately                  |
| `--unit-dir`      | `--unit-dir /etc/systemd/system`    | Directory for unit files (default `/run/systemd/system`)                      |
| `--enable`        |                                     | Run `systemctl enable --now` after writing units (requires `--persistent`)    |

### Examples

```bash
# Basic read-only rootfs mount
sudo oci-packer mount oci://./layout:example/service:v1 /mnt/app

# Read-only rootfs + writable application data
sudo oci-packer mount oci://./layout:example/service:v1 /mnt/app \
  --bind /srv/app-data:/var/lib/app \
  --bind /srv/app-logs:/var/log/app

# Cap tmpfs sizes
sudo oci-packer mount oci://./layout:example/service:v1 /mnt/app \
  --tmpfs-size /tmp:512m --tmpfs-size /run:64m

# No auto-tmpfs (the application does not write to volatile paths)
sudo oci-packer mount oci://./layout:example/service:v1 /mnt/app --no-auto-tmpfs
```

## Persistent mounts via systemd

With `--persistent` the command does not mount anything itself; instead it generates systemd.mount units: one for the
overlay and one per tmpfs and bind mount. Names are derived from the mount-point path (`systemd-escape --path`):
`/mnt/app` → `mnt-app.mount`.

Dependencies are wired with `After=` and `BindsTo=` on the overlay unit — systemd tears down the tmpfs/bind mounts when
the overlay stops and respects ordering.

```bash
# Write units and enable them right away (survives reboot)
sudo oci-packer mount oci://./layout:example/service:v1 /mnt/app \
  --bind /srv/data:/var/lib/app \
  --persistent --unit-dir /etc/systemd/system --enable
```

With `--enable` the command runs `systemctl daemon-reload` and
`systemctl enable --now <all units>`. Without `--enable` it only prints the `systemctl start`
command for you to run manually.

> For persistence across reboots use `--unit-dir /etc/systemd/system`. The default
> `/run/systemd/system` is cleared at boot. The OCI Layout must remain available at the same
> paths after reboot.

## `umount` — usage

```
oci-packer umount [--lazy] <dst>
```

Reads `/proc/mounts`, finds every mount point under `<dst>` and unmounts them in order of descending path depth —
tmpfs/bind first, then the overlay (otherwise the overlay would return `EBUSY`). A failure on one mount does not abort
the rest; the command exits non-zero if any mount could not be removed.

| Flag     | Description                                                   |
|----------|---------------------------------------------------------------|
| `--lazy` | Lazy unmount (`MNT_DETACH`) — detaches even busy mount points |

```bash
sudo oci-packer umount /mnt/app
sudo oci-packer umount --lazy /mnt/app   # if something is holding the mount open
```

For `--persistent` mounts, use systemd instead:
`systemctl disable --now mnt-app.mount`.

## Limitations and pitfalls

- **Root only, Linux only — but the root is about *where* the mount lands.** Putting a mount in
  the host's mount namespace takes `CAP_SYS_ADMIN` there, which is real root; without it the
  command stops at `operation not permitted`. Since kernel 5.11 an unprivileged user *can* mount
  overlayfs — inside a user namespace they created, where they hold a namespaced
  `CAP_SYS_ADMIN` — and `unshare -Urm oci-packer mount …` does work. It is not a rootless mode
  for this command, though: such a mount is visible only inside that namespace and dies with it,
  which is the opposite of what `mount` exists to do. A rootless *runtime* uses exactly that
  property on purpose, assembling the rootfs inside the namespace its workload will run in.
  fuse-overlayfs is not used on any path. On non-Linux platforms the commands return an error.
- **The layout is the only backing store.** `lowerdir` references directories inside the OCI Layout by absolute path. If
  the layout is moved, renamed or deleted, the mounted filesystem starts returning I/O errors. Unlike containerd/Docker,
  layer data is never copied out.
- **Layer-count limit.** The overlayfs option string is bounded by the kernel page size (~4096 bytes). Images with many
  layers hit this limit — the command returns a clear error *before* the kernel call, suggesting you squash the layers
  with `oci-packer copy`.
- **Filesystem compatibility.** overlayfs cannot use NFS, FUSE, CIFS, FAT/exFAT, or another overlayfs without `index=on`
  as a lowerdir. If the layout lives on such a filesystem the mount fails with `EINVAL`.
- **Read-only breaks some applications.** tmpfs covers only `/tmp`, `/run`, `/var/tmp`. Non-standard writable paths must
  be declared manually with `--bind` — there is no automatic detection from the image configuration.
- **The rootfs is mounted without `nosuid`/`nodev`.** The command mounts image content read-only, but does not otherwise
  restrict it: a setuid binary in a layer is setuid on the host, and a device node in a layer is a device node. That is
  the right default for a tool whose purpose is inspecting an image as root, and the wrong one for exposing a tenant's
  image to anything else. Callers using the `overlay` package directly can pass those flags through
  `MountOptions.Flags`, where they land on the creating syscall.
- **`--verify` does not work in unpack mode.** Digest verification compares the layer bytes against the digest recorded
  in the manifest. In unpack mode the original compressed layer bytes are not retained (only the already-unpacked
  directories), so the recorded digest cannot be reproduced and `--verify` returns an error. Because `mount` only works
  with an unpack-mode layout, **integrity should be verified at `copy` time, before unpacking**
  (tar-mode layout). The `VerifyLayers` method works correctly for tar mode specifically.
- **No namespaces.** The command performs filesystem mounting only: it does not create network/pid namespaces, cgroups,
  etc. It is not a container runtime (runc/crun) replacement, but a tool for accessing image contents.
- **A mounted layer is safe from `delete`, but only while it is mounted.** `oci-packer delete`
  reads `/proc/mounts` and refuses to garbage-collect a blob directory that is in use as an overlayfs lower dir. The
  check is a point-in-time one: it protects a live mount, not a layout you are about to mount.

## Troubleshooting

Mount-specific problems — the host `/run` / systemd breakage, `not in unpack mode`,
`not a container image`, platform mismatch, `lowerdir` limit, `umount` `EBUSY` — are collected in the
central [Troubleshooting hub](troubleshooting.md#mount--umount).

## Comparison with a container runtime

|               | `oci-packer mount`                    | runc / Docker                     |
|---------------|---------------------------------------|-----------------------------------|
| What it does  | overlay-mounts the image rootfs       | fully runs a container            |
| Isolation     | filesystem only                       | filesystem + namespaces + cgroups |
| Writes        | tmpfs + bind                          | writable upperdir by default      |
| Backing store | OCI Layout (in place)                 | copy into a storage driver        |
| Purpose       | image content access, read-only bases | running processes                 |

To *run* what an unpack-mode layout holds rather than inspect it, the layer directories this command mounts are equally
the input of a runtime — the division of labour oci-packer is built for is that it delivers and unpacks images while
something else assembles the rootfs and execs into it.

## See also

- [Documentation hub](README.md)
- [Troubleshooting](troubleshooting.md)
- [copy](copy.md) — produce an `--unpack` layout · [pack](pack.md) · [proxy](proxy.md)
