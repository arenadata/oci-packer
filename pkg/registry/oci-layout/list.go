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
	"context"

	"github.com/arenadata/oci-packer/pkg/registry/reference"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// ImageInfo summarizes one entry of the layout index — a tagged image, index or
// artifact stored in the layout.
type ImageInfo struct {
	Ref          string // org.opencontainers.image.ref.name annotation ("" if untagged)
	Digest       digest.Digest
	Kind         string // "image", "index" or the raw media type for other artifacts
	MediaType    string
	ArtifactType string
	Platform     string // "os/arch" if the index entry pins a platform
	Size         int64
}

// Component is one addressable part of a Pack manifest: its config or a layer.
type Component struct {
	Role         string // "config" or "layer"
	Title        string // org.opencontainers.image.title annotation (usually the file name)
	Digest       digest.Digest
	MediaType    string
	ArtifactType string
	Size         int64
	Annotations  map[string]string
}

// PackManifest holds the components of a single manifest. A Pack that is an OCI
// Index yields one PackManifest per platform variant.
type PackManifest struct {
	Digest       digest.Digest
	MediaType    string
	ArtifactType string
	Platform     string
	Annotations  map[string]string
	Components   []Component
}

// PackInfo describes a Pack (one index entry) and the components it is built
// from.
type PackInfo struct {
	Ref          string
	Digest       digest.Digest
	Kind         string
	ArtifactType string
	Annotations  map[string]string
	Manifests    []PackManifest
}

// List returns a summary of every entry in the layout index.
func (l Layout) List() ([]ImageInfo, error) {
	index, err := l.readIndex()
	if err != nil {
		return nil, err
	}

	out := make([]ImageInfo, 0, len(index.Manifests))
	for _, m := range index.Manifests {
		info := ImageInfo{
			Ref:          m.Annotations[ocispecv1.AnnotationRefName],
			Digest:       m.Digest,
			Kind:         kindOf(m.MediaType),
			MediaType:    m.MediaType,
			ArtifactType: m.ArtifactType,
			Size:         m.Size,
		}
		if m.Platform != nil {
			info.Platform = platforms.Format(*m.Platform)
		}
		out = append(out, info)
	}
	return out, nil
}

// PackComponents resolves ref to a Pack in the layout and returns its components
// (config and layers). If ref points to an OCI Index, every child manifest is
// reported as a separate PackManifest so all platform variants are shown.
func (l Layout) PackComponents(ctx context.Context, ref reference.Reference) (PackInfo, error) {
	desc, err := l.Resolve(ctx, ref)
	if err != nil {
		return PackInfo{}, err
	}

	info := PackInfo{
		Ref:          desc.Annotations[ocispecv1.AnnotationRefName],
		Digest:       desc.Digest,
		Kind:         kindOf(desc.MediaType),
		ArtifactType: desc.ArtifactType,
		Annotations:  desc.Annotations,
	}

	switch desc.MediaType {
	case ocispecv1.MediaTypeImageIndex, images.MediaTypeDockerSchema2ManifestList:
		var idx ocispecv1.Index
		if err = l.readJSONBlob(desc.Digest, &idx); err != nil {
			return PackInfo{}, err
		}
		for _, m := range idx.Manifests {
			pm, err := l.packManifest(m)
			if err != nil {
				return PackInfo{}, err
			}
			info.Manifests = append(info.Manifests, pm)
		}

	default:
		pm, err := l.packManifest(desc)
		if err != nil {
			return PackInfo{}, err
		}
		info.Manifests = append(info.Manifests, pm)
	}

	return info, nil
}

// packManifest reads a single image manifest and turns its config and layers
// into Components.
func (l Layout) packManifest(desc ocispecv1.Descriptor) (PackManifest, error) {
	manifest, err := l.readManifestBlob(desc)
	if err != nil {
		return PackManifest{}, err
	}

	pm := PackManifest{
		Digest:       desc.Digest,
		MediaType:    desc.MediaType,
		ArtifactType: manifest.ArtifactType,
		Annotations:  manifest.Annotations,
	}
	if desc.Platform != nil {
		pm.Platform = platforms.Format(*desc.Platform)
	}

	pm.Components = append(pm.Components, componentOf("config", manifest.Config))
	for _, layer := range manifest.Layers {
		pm.Components = append(pm.Components, componentOf("layer", layer))
	}
	return pm, nil
}

func componentOf(role string, d ocispecv1.Descriptor) Component {
	return Component{
		Role:         role,
		Title:        d.Annotations[ocispecv1.AnnotationTitle],
		Digest:       d.Digest,
		MediaType:    d.MediaType,
		ArtifactType: d.ArtifactType,
		Size:         d.Size,
		Annotations:  d.Annotations,
	}
}

// kindOf maps a manifest media type to a short, human-friendly kind.
func kindOf(mt string) string {
	switch mt {
	case ocispecv1.MediaTypeImageIndex, images.MediaTypeDockerSchema2ManifestList:
		return "index"
	case ocispecv1.MediaTypeImageManifest, images.MediaTypeDockerSchema2Manifest:
		return "image"
	default:
		return mt
	}
}
