/*
  Copyright (c) 2026 Arenadata Softwer LLC.
  Licensed under the Apache License, Version 2.0 (the "License");
  you may not use this file except in compliance with the License.
  You may obtain a copy of the License at

      http://www.apache.org/licenses/LICENSE-2.0

  Unless required by applicable law or agreed to in writing, software
  distributed under the License is distributed on an "AS IS" BASIS,
  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
  See the License for the specific language governing permissions and
  limitations under the License.
*/

package layout

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/arenadata/oci-packer/internal/logger"
	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/reference"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/platforms"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	"github.com/moby/go-archive"
	"github.com/moby/go-archive/compression"
	"github.com/natefinch/atomic"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	MediaTypeTarLayout    = "application/vnd.oci.layout.blobs.tar"
	MediaTypeUnpackLayout = "application/vnd.oci.layout.blobs.unpack"
)

var log = logger.New("oci_layout")

type zstdReader struct {
	*zstd.Decoder
}

func (z zstdReader) Close() error {
	z.Decoder.Close()
	return nil
}

type Option func(*Layout)

func Unpack() Option {
	return func(c *Layout) {
		c.unpack = true
	}
}

type Layout struct {
	ref    reference.Reference
	unpack bool
}

func New(ref string, opts ...Option) (registry.Resolver, error) {
	parsedRef, err := reference.Parse(ref)
	if err != nil {
		return nil, err
	}

	if parsedRef.Scheme != reference.OciScheme {
		return nil, reference.ErrSchemeUnsupported
	}

	layoutFile := filepath.Join(parsedRef.Path, ocispecv1.ImageLayoutFile)
	if _, err = os.Stat(layoutFile); err != nil {
		layout := &Layout{ref: parsedRef}
		for _, opt := range opts {
			opt(layout)
		}
		if err = layout.new(); err != nil {
			return nil, err
		}

		return layout, nil
	}

	return Open(parsedRef, opts...)
}

func Open(ref reference.Reference, opts ...Option) (registry.Resolver, error) {
	layout := &Layout{ref: ref}
	for _, opt := range opts {
		opt(layout)
	}

	if err := layout.validate(); err != nil {
		return nil, err
	}

	index, err := layout.readIndex()
	if err != nil {
		return nil, err
	}

	layout.unpack = index.ArtifactType == MediaTypeUnpackLayout

	return layout, nil
}

func (l Layout) new() error {
	log.WithFields(map[string]any{"path": l.ref.Path, "unpack": l.unpack}).Debug("new OCI layout initialized")

	layoutFile := filepath.Join(l.ref.Path, ocispecv1.ImageLayoutFile)
	blobsDir := filepath.Join(l.ref.Path, ocispecv1.ImageBlobsDir)

	if err := os.MkdirAll(blobsDir, 0755); err != nil {
		return fmt.Errorf("failed to create blobs directory: %w", err)
	}

	ociLayout := ocispecv1.ImageLayout{Version: ocispecv1.ImageLayoutVersion}
	layoutData, err := json.Marshal(ociLayout)
	if err != nil {
		return fmt.Errorf("failed to marshal oci-layout: %w", err)
	}

	if err = os.WriteFile(layoutFile, layoutData, 0640); err != nil {
		return fmt.Errorf("failed to write oci-layout file: %w", err)
	}

	index := ocispecv1.Index{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispecv1.MediaTypeImageIndex,
		ArtifactType: MediaTypeTarLayout,
		Manifests:    []ocispecv1.Descriptor{},
	}

	if l.unpack {
		index.ArtifactType = MediaTypeUnpackLayout
	}

	return l.writeIndex(&index)
}

func (l Layout) validate() error {
	layoutFile := filepath.Join(l.ref.Path, ocispecv1.ImageLayoutFile)
	layoutJson, err := os.ReadFile(layoutFile)
	if err != nil {
		return fmt.Errorf("failed to read OCI layout file: %w", err)
	}

	var ociLayout ocispecv1.ImageLayout
	if err = json.Unmarshal(layoutJson, &ociLayout); err != nil {
		return fmt.Errorf("failed to unmarshal OCI layout: %w", err)
	}

	if ociLayout.Version != ocispecv1.ImageLayoutVersion {
		return fmt.Errorf("invalid OCI layout version: %q", ociLayout.Version)
	}
	return nil
}

func (l Layout) readIndex() (*ocispecv1.Index, error) {
	indexPath := filepath.Join(l.ref.Path, ocispecv1.ImageIndexFile)
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read index file: %w", err)
	}

	var index ocispecv1.Index
	if err = json.Unmarshal(indexData, &index); err != nil {
		return nil, fmt.Errorf("failed to parse index file: %w", err)
	}
	return &index, nil
}

func (l Layout) writeIndex(index *ocispecv1.Index) error {
	indexPath := filepath.Join(l.ref.Path, ocispecv1.ImageIndexFile)
	indexData, err := json.Marshal(index)
	if err != nil {
		return fmt.Errorf("failed to marshal index file: %w", err)
	}
	return atomic.WriteFile(indexPath, bytes.NewReader(indexData))
}

func (l Layout) Resolve(_ context.Context, ref reference.Reference) (ocispecv1.Descriptor, error) {
	index, err := l.readIndex()
	if err != nil {
		return ocispecv1.Descriptor{}, err
	}

	repoRef := l.ref.Merge(ref)
	for _, manifest := range index.Manifests {
		r := manifest.Annotations[ocispecv1.AnnotationRefName]
		if r == repoRef.Ref {
			return manifest, nil
		}
	}

	return ocispecv1.Descriptor{}, fmt.Errorf("no manifests found in index")
}

func (l Layout) Exists(ctx context.Context, ref reference.Reference) (bool, error) {
	desc, err := l.Resolve(ctx, ref)
	if err != nil {
		return false, nil
	}

	return l.exists(ctx, desc)
}

func (l Layout) exists(_ context.Context, desc ocispecv1.Descriptor) (bool, error) {
	blobPath := l.getBlobPath(desc.Digest)
	st, err := os.Stat(blobPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if l.unpack && st.IsDir() {
		return true, nil
	}
	return st.Size() == desc.Size, nil
}

func (l Layout) FetchReference(ctx context.Context, ref reference.Reference) (ocispecv1.Descriptor, io.ReadCloser, error) {
	desc, err := l.Resolve(ctx, ref)
	if err != nil {
		return ocispecv1.Descriptor{}, nil, err
	}

	ref.Ref = desc.Digest.String()
	r, err := l.Fetch(ctx, ref)
	if err != nil {
		return ocispecv1.Descriptor{}, nil, err
	}

	return desc, r, nil
}

func (l Layout) Fetch(_ context.Context, ref reference.Reference) (io.ReadCloser, error) {
	dgst, err := digest.Parse(ref.Ref)
	if err != nil {
		return nil, err
	}

	blobPath := l.getBlobPath(dgst)

	if l.unpack {
		desc := ref.Descriptor()
		switch desc.MediaType {
		case ocispecv1.MediaTypeImageLayer:
			return archive.Tar(blobPath, compression.None)
		case ocispecv1.MediaTypeImageLayerGzip, images.MediaTypeDockerSchema2LayerGzip:
			r, err := archive.Tar(blobPath, compression.None)
			if err != nil {
				return nil, err
			}
			return gzip.NewReader(r)

		case ocispecv1.MediaTypeImageLayerZstd, images.MediaTypeDockerSchema2LayerZstd:
			r, err := archive.Tar(blobPath, compression.None)
			if err != nil {
				return nil, err
			}
			zstdr, err := zstd.NewReader(r)
			if err != nil {
				return nil, fmt.Errorf("creating zstd reader failed: %w", err)
			}
			return zstdReader{zstdr}, nil
		}
	}

	file, err := os.Open(blobPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open blob '%s': %w", dgst, err)
	}
	return file, nil
}

func (l Layout) Push(ctx context.Context, desc ocispecv1.Descriptor, r io.Reader) error {
	log.WithFields(map[string]any{
		"digest":     desc.Digest,
		"media_type": desc.MediaType,
		"size":       desc.Size,
		"unpack":     l.unpack,
	}).Debug("pushing to layout")

	if ok, err := l.exists(ctx, desc); err != nil {
		return err
	} else if ok {
		return registry.ErrAlreadyExists
	}

	switch desc.MediaType {
	case ocispecv1.MediaTypeImageLayer,
		ocispecv1.MediaTypeImageLayerGzip, images.MediaTypeDockerSchema2LayerGzip,
		ocispecv1.MediaTypeImageLayerZstd, images.MediaTypeDockerSchema2LayerZstd:
		// Filesystem layer. In tar mode it is stored verbatim so its digest is
		// preserved; only unpack mode decompresses and extracts it into a
		// directory.
		if !l.unpack {
			return l.writeBlob(desc, r)
		}
		return l.unpackLayer(desc, r)

	default:
		return l.writeBlob(desc, r)
	}
}

// unpackLayer decompresses a layer per its media type and extracts it into a
// directory under the blobs path (unpack mode only).
func (l Layout) unpackLayer(desc ocispecv1.Descriptor, r io.Reader) (err error) {
	switch desc.MediaType {
	case ocispecv1.MediaTypeImageLayerGzip, images.MediaTypeDockerSchema2LayerGzip:
		gz, err := gzip.NewReader(r)
		if err != nil {
			return fmt.Errorf("creating gzip reader failed: %w", err)
		}
		defer func() { _ = gz.Close() }()
		r = gz

	case ocispecv1.MediaTypeImageLayerZstd, images.MediaTypeDockerSchema2LayerZstd:
		zr, err := zstd.NewReader(r)
		if err != nil {
			return fmt.Errorf("creating zstd reader failed: %w", err)
		}
		defer zr.Close()
		r = zr
	}

	destDir := l.getBlobPath(desc.Digest)
	// exists() accepts any directory here as a finished layer, so a partial
	// extraction — an aborted copy, a sibling layer failing and cancelling this
	// one — must not survive to be mistaken for the real thing next time.
	defer func() {
		if err != nil {
			_ = os.RemoveAll(destDir)
		}
	}()

	// Create the layer directory ourselves. Left to archive.Unpack it is made by
	// MkdirAllAndChownNew, which chowns it to 0:0 and so fails for an
	// unprivileged user even when every file in the layer belongs to them.
	if err = os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create layer directory: %w", err)
	}

	return archive.Unpack(r, destDir, tarOptions(os.Geteuid()))
}

var rootlessUnpackOnce sync.Once

// tarOptions returns the extraction options for the given effective uid. Root —
// real or userns-mapped — restores each entry's owner from its tar header, as
// before. An unprivileged user holds no CAP_CHOWN or CAP_MKNOD, so restoring
// ownership would fail with EPERM on the first root-owned entry: instead every
// entry lands owned by the invoking user, and device nodes are skipped rather
// than failing on mknod (InUserNS is go-archive's switch for exactly that).
func tarOptions(euid int) *archive.TarOptions {
	opts := &archive.TarOptions{WhiteoutFormat: -1}
	if euid != 0 {
		opts.NoLchown = true
		opts.InUserNS = true
		rootlessUnpackOnce.Do(func() {
			log.WithField("euid", euid).Warn("unpacking rootless: tar ownership is not restored, all files belong to the invoking user")
		})
	}
	return opts
}

func (l Layout) writeBlob(desc ocispecv1.Descriptor, r io.Reader) (err error) {
	log.WithField("digest", desc.Digest).Debug("writing blob")

	blobDir := l.getBlobDirectory(desc.Digest)
	if err = os.MkdirAll(blobDir, 0755); err != nil {
		return fmt.Errorf("failed to create blob directory: %w", err)
	}

	blobPath := l.getBlobPath(desc.Digest)
	file, err := os.OpenFile(blobPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0640)
	if err != nil {
		return fmt.Errorf("failed to create blob file '%s': %w", desc.Digest, err)
	}

	// Drop the truncated remains of a failed write. Both defers are registered
	// after the file exists, and run last-first, so the handle is closed before
	// the file goes away.
	defer func() {
		if err != nil {
			_ = os.Remove(blobPath)
		}
	}()
	defer func() { _ = file.Close() }()

	// Write blob content and verify digest
	digester := digest.Canonical.Digester()
	if _, err = io.Copy(io.MultiWriter(file, digester.Hash()), r); err != nil {
		return fmt.Errorf("failed to write blob: %w", err)
	}

	// Verify the digest matches
	calculatedDigest := digester.Digest()
	if calculatedDigest != desc.Digest {
		return fmt.Errorf("digest mismatch: expected %s, got %s", desc.Digest, calculatedDigest)
	}

	return nil
}

func (l Layout) SetTag(_ context.Context, desc ocispecv1.Descriptor) error {
	log.WithFields(map[string]any{"ref": l.ref, "digest": desc.Digest}).Debug("setting tag in layout")

	index, err := l.readIndex()
	if err != nil {
		return err
	}

	if desc.Annotations == nil {
		desc.Annotations = make(map[string]string)
	}
	desc.Annotations[ocispecv1.AnnotationRefName] = l.ref.Ref

	var found bool
	for i, manifest := range index.Manifests {
		if manifest.Digest == desc.Digest {
			index.Manifests[i] = desc
			found = true
			break
		}
	}

	if !found {
		index.Manifests = append(index.Manifests, desc)
	}

	return l.writeIndex(index)
}

func (l Layout) getBlobDirectory(dgst digest.Digest) string {
	algo := dgst.Algorithm().String()
	return filepath.Join(l.ref.Path, ocispecv1.ImageBlobsDir, algo)
}

func (l Layout) getBlobPath(dgst digest.Digest) string {
	algo := dgst.Algorithm().String()
	hex := dgst.Hex()
	return filepath.Join(l.ref.Path, ocispecv1.ImageBlobsDir, algo, hex)
}

func (l Layout) MountFrom(ctx context.Context, ref reference.Reference) (ocispecv1.Descriptor, error) {
	desc, err := l.Resolve(ctx, ref)
	if err != nil {
		return ocispecv1.Descriptor{}, err
	}
	return desc, nil
}

// readJSONBlob opens the blob identified by dgst and decodes it into v.
func (l Layout) readJSONBlob(dgst digest.Digest, v any) error {
	f, err := os.Open(l.getBlobPath(dgst))
	if err != nil {
		return fmt.Errorf("failed to open blob '%s': %w", dgst, err)
	}
	defer func() { _ = f.Close() }()

	if err = json.NewDecoder(f).Decode(v); err != nil {
		return fmt.Errorf("failed to decode blob '%s': %w", dgst, err)
	}
	return nil
}

// readManifestBlob decodes the manifest blob referenced by desc. The caller
// must have already resolved any index to a concrete manifest descriptor.
func (l Layout) readManifestBlob(desc ocispecv1.Descriptor) (ocispecv1.Manifest, error) {
	switch desc.MediaType {
	case ocispecv1.MediaTypeImageIndex, images.MediaTypeDockerSchema2ManifestList:
		return ocispecv1.Manifest{}, fmt.Errorf("descriptor '%s' is an index, not a manifest", desc.Digest)
	}

	var manifest ocispecv1.Manifest
	if err := l.readJSONBlob(desc.Digest, &manifest); err != nil {
		return ocispecv1.Manifest{}, err
	}
	return manifest, nil
}

// resolveManifest resolves ref to a single image manifest. If ref points to an
// OCI Index (multi-platform), the manifest matching the host platform is
// selected via containerd/platforms.
func (l Layout) resolveManifest(ctx context.Context, ref reference.Reference) (ocispecv1.Manifest, error) {
	desc, err := l.Resolve(ctx, ref)
	if err != nil {
		return ocispecv1.Manifest{}, err
	}

	switch desc.MediaType {
	case ocispecv1.MediaTypeImageIndex, images.MediaTypeDockerSchema2ManifestList:
		var index ocispecv1.Index
		if err = l.readJSONBlob(desc.Digest, &index); err != nil {
			return ocispecv1.Manifest{}, err
		}

		match := platforms.Only(platforms.DefaultSpec())
		for _, m := range index.Manifests {
			if m.Platform != nil && match.Match(*m.Platform) {
				return l.readManifestBlob(m)
			}
		}
		return ocispecv1.Manifest{}, fmt.Errorf("no manifest in index matches host platform %s",
			platforms.Format(platforms.DefaultSpec()))
	}

	return l.readManifestBlob(desc)
}

// isImageConfig reports whether mt identifies an OCI/Docker container image
// config. Only manifests with such a config describe a filesystem and can be
// overlay-mounted; arbitrary OCI artifacts cannot.
func isImageConfig(mt string) bool {
	switch mt {
	case ocispecv1.MediaTypeImageConfig, images.MediaTypeDockerSchema2Config:
		return true
	}
	return false
}

// LayerDirs returns absolute paths to the unpacked layer directories of ref in
// bottom-to-top order (as recorded in the manifest). The reference must select
// a single container image inside the layout (e.g. oci://./layout:repo/name:tag);
// non-image artifacts are rejected. The layout must be in unpack mode. If ref
// resolves to an OCI Index, the manifest matching the host platform is selected
// automatically.
func (l Layout) LayerDirs(ctx context.Context, ref reference.Reference) ([]string, error) {
	manifest, err := l.resolveManifest(ctx, ref)
	if err != nil {
		return nil, err
	}

	if !isImageConfig(manifest.Config.MediaType) {
		return nil, fmt.Errorf("reference is not a container image (config media type %q); "+
			"only images can be mounted", manifest.Config.MediaType)
	}

	if !l.unpack {
		return nil, fmt.Errorf("layout is not in unpack mode; re-copy with --unpack")
	}

	dirs := make([]string, 0, len(manifest.Layers))
	for i, layer := range manifest.Layers {
		dir := l.getBlobPath(layer.Digest)
		st, err := os.Stat(dir)
		if err != nil {
			return nil, fmt.Errorf("layer[%d] '%s': %w", i, layer.Digest, err)
		}
		if !st.IsDir() {
			return nil, fmt.Errorf("layer[%d] '%s' is not an unpacked directory", i, layer.Digest)
		}
		dirs = append(dirs, dir)
	}
	return dirs, nil
}

// VerifyLayers recomputes the digest of each layer blob and compares it to the
// digest recorded in the manifest, returning an error on the first mismatch.
//
// Verification is only reliable in tar mode (the original compressed layer
// bytes are stored as-is). In unpack mode the original bytes are discarded,
// so the recorded digest cannot be reproduced and VerifyLayers returns an
// error directing the caller to verify before unpacking.
func (l Layout) VerifyLayers(ctx context.Context, ref reference.Reference) error {
	if l.unpack {
		return fmt.Errorf("layer verification is not supported on unpack-mode layouts: " +
			"the original compressed layer bytes are not retained; verify before unpacking " +
			"(copy without --unpack, then 'oci-packer mount --verify')")
	}

	log := logger.New("verify_layers")
	manifest, err := l.resolveManifest(ctx, ref)
	if err != nil {
		return err
	}

	for i, layer := range manifest.Layers {
		f, err := os.Open(l.getBlobPath(layer.Digest))
		if err != nil {
			return fmt.Errorf("layer[%d] '%s': %w", i, layer.Digest, err)
		}

		actual, err := digest.FromReader(f)
		_ = f.Close()
		if err != nil {
			return fmt.Errorf("layer[%d] '%s': %w", i, layer.Digest, err)
		}

		if actual != layer.Digest {
			return fmt.Errorf("layer[%d] digest mismatch: expected %s, got %s", i, layer.Digest, actual)
		}
		log.WithField("digest", layer.Digest).Debug("layer verified")
	}
	return nil
}
