# `delete`

Delete an image (or artifact) from an OCI layout and garbage-collect the blobs
that only it referenced. Aliases: `rm`, `remove`.

## What it is for

A single OCI layout directory can hold many images that **share layers**. Simply
removing an entry from `index.json` would leave its blobs on disk forever, while
deleting all of an image's blobs blindly would corrupt any other image built on
the same layers.

`oci-packer delete` does the safe thing:

- drops the selected image's entry from the layout index, and
- removes every blob reachable **only** from that image (its manifest, config
  and layers), while **keeping** any blob still referenced by another image in
  the layout.

## Reference format

The reference selects one image inside the layout — the same three-part form the
other layout commands use:

```
oci://<layout-dir>:<repository>:<tag>
       ▲            ▲            ▲
       │            │            └─ image tag (or @sha256:… digest)
       │            └─ repository name of the image inside the layout
       └─ path to the OCI Layout directory (relative or absolute)
```

Everything up to the first `:` is the layout directory; the remainder is matched
against the manifest's `org.opencontainers.image.ref.name` annotation (the value
`oci-packer copy`/`pack` writes as the destination tag).

## Usage

```
oci-packer delete <oci://layout:repo:tag>
oci-packer rm     <oci://layout:repo:tag>
```

| Argument | Example                              | Description                                     |
|----------|--------------------------------------|-------------------------------------------------|
| `<ref>`  | `oci://./layout:example/service:v1`  | Image inside an OCI layout to remove             |

### Examples

```bash
# Remove one image; shared layers used by other images are kept
oci-packer delete oci://./layout:example/service:v1

# Same, via the short alias
oci-packer rm oci://./layout:example/worker:v2
```

## Behaviour

- **Shared layers are retained.** A blob is removed only when no surviving index
  entry references it. Deleting one of two images that share a base layer keeps
  that base layer for the other image.
- **Multiple tags of the same manifest.** If the same manifest is tagged twice,
  deleting one tag only drops that entry; the blobs stay because the other tag
  still references them.
- **Multi-platform images.** Deleting a tag that points to an OCI Index removes
  the index blob and recurses into every child manifest, collecting their
  configs and layers for garbage collection.
- **Consistency.** The trimmed index is written *before* blobs are removed, so an
  interrupted delete never leaves the index pointing at a missing blob (at worst
  a few unreferenced blobs linger, harmlessly, on disk).

## Mounted images (unpack mode, Linux)

In an unpack-mode layout the layer directories can be
[`mount`](mount.md)ed via overlayfs. Deleting a layer directory out from under a
live mount would break it, so `delete` refuses to remove a layer that is
currently in use as an overlayfs `lowerdir`:

```
cannot delete "example/service:v1": 1 layer(s) are currently mounted
(sha256:…); unmount them first with 'oci-packer umount'
```

Unmount first, then delete:

```bash
sudo oci-packer umount /mnt/app
oci-packer delete oci://./layout:example/service:v1
```

The check reads `/proc/mounts`, so it only applies on Linux (mounting is
Linux-only). A layer that is shared with another image is retained regardless
and therefore never triggers this error.

## See also

- [Documentation hub](README.md)
- [copy](copy.md) — populate a layout · [mount / umount](mount.md) · [pack](pack.md)
