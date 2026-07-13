# `list`

Inspect the contents of an OCI layout. What is shown depends on the reference:
name the layout directory to list its images, or add a `:repo:tag` to drill into
one Pack's components. Alias: `ls`.

```
oci-packer list <oci://layout-dir>                 # list images in the layout
oci-packer list <oci://layout-dir:repository:tag>  # components of one Pack
```

The two modes are distinguished the same way the reference parser splits a
reference: if anything follows the layout directory (a `:repo:tag` or
`@digest`), that one Pack is inspected; otherwise the whole layout is listed.

## Listing a layout

With a bare layout reference, `list` prints one row per index entry — a tagged
image, index or artifact.

| Column          | Meaning                                                              |
|-----------------|---------------------------------------------------------------------|
| `REF`           | `org.opencontainers.image.ref.name` (the tag), or `-` if untagged   |
| `KIND`          | `image`, `index`, or the raw media type for other artifacts          |
| `DIGEST`        | Manifest digest, `sha256:` + first 12 hex chars                      |
| `PLATFORM`      | `os/arch` when the index entry pins a platform, else `-`            |
| `ARTIFACT TYPE` | `artifactType` of the entry (the Pack type), else `-`               |
| `SIZE`          | Size of the manifest/index blob                                      |

```bash
oci-packer list oci://./layout
```

```
REF         KIND   DIGEST               PLATFORM  ARTIFACT TYPE                 SIZE
app:v1      image  sha256:6c856e0ab21a  -         -                             541B
img:latest  index  sha256:ea855b12d254  -         application/vnd.example.pack  537B
```

## Listing a Pack's components

With a `:repo:tag` reference, `list` prints the components the selected Pack is
built from — its config blob and layers.

| Column       | Meaning                                                         |
|--------------|-----------------------------------------------------------------|
| `ROLE`       | `config` or `layer`                                            |
| `TITLE`      | `org.opencontainers.image.title` (the file name), else `-`    |
| `DIGEST`     | Blob digest, `sha256:` + first 12 hex chars                    |
| `MEDIA TYPE` | Media type of the component blob                               |
| `SIZE`       | Size of the component blob                                     |

Each layer's title is the packed file name (`oci-packer pack`/`copy` set the
`org.opencontainers.image.title` annotation). If the reference resolves to a
multi-platform **index**, the components of every platform variant are listed
under their own `Manifest` header.

```bash
oci-packer list oci://./layout:img:latest
```

```
Pack img:latest
  digest:       sha256:ea855b12d2541e446ef64a694f9a452834277220b0cb469e6652814d78f8302e
  kind:         index
  artifactType: application/vnd.example.pack

Manifest sha256:dcb224fcbbf5  platform=linux/amd64  artifactType=-
ROLE    TITLE                DIGEST               MEDIA TYPE                                   SIZE
config  -                    sha256:9d99a75171ae  application/vnd.oci.image.config.v1+json     37B
layer   rootfs-amd64.tar.gz  sha256:7ac6384843c7  application/vnd.oci.image.layer.v1.tar+gzip  9B

Manifest sha256:91be29f84341  platform=linux/arm64  artifactType=-
ROLE    TITLE                DIGEST               MEDIA TYPE                                   SIZE
config  -                    sha256:d6f56bc20064  application/vnd.oci.image.config.v1+json     37B
layer   rootfs-arm64.tar.gz  sha256:7bd3d418d504  application/vnd.oci.image.layer.v1.tar+gzip  9B
```

## See also

- [Documentation hub](README.md)
- [copy](copy.md) — populate a layout · [delete](delete.md) — remove an image ·
  [mount / umount](mount.md) · [pack](pack.md)
