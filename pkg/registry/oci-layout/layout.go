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

	"github.com/arenadata/oci-packer/internal/logger"
	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/reference"

	"github.com/containerd/containerd/v2/core/images"
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
	case ocispecv1.MediaTypeImageLayer:
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

	default:
		return l.writeBlob(desc, r)
	}

	if l.unpack {
		destDir := l.getBlobPath(desc.Digest)
		return archive.Unpack(r, destDir, &archive.TarOptions{WhiteoutFormat: -1})
	}

	return l.writeBlob(desc, r)
}

func (l Layout) writeBlob(desc ocispecv1.Descriptor, r io.Reader) error {
	log.WithField("digest", desc.Digest).Debug("writing blob")

	blobDir := l.getBlobDirectory(desc.Digest)
	if err := os.MkdirAll(blobDir, 0755); err != nil {
		return fmt.Errorf("failed to create blob directory: %w", err)
	}

	blobPath := l.getBlobPath(desc.Digest)
	file, err := os.OpenFile(blobPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0640)
	if err != nil {
		return fmt.Errorf("failed to create blob file '%s': %w", desc.Digest, err)
	}
	defer func() { _ = file.Close() }()

	// Write blob content and verify digest
	digester := digest.Canonical.Digester()
	if _, err := io.Copy(io.MultiWriter(file, digester.Hash()), r); err != nil {
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
