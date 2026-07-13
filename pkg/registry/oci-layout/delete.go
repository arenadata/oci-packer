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
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/arenadata/oci-packer/pkg/registry/reference"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/opencontainers/go-digest"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// Delete removes the image (or artifact) tagged by ref from the layout. Its
// entry is dropped from the index and every blob reachable only from it is
// garbage-collected. Blobs still referenced by another index entry — shared
// layers, or the same manifest tagged under a second name — are retained.
//
// In unpack mode a layer directory that is currently overlay-mounted (used as a
// lowerdir) is never removed: Delete aborts before touching anything and
// reports the mounted layers so the caller can 'oci-packer umount' first.
// Mounting is Linux-only, so this guard is a no-op on other platforms.
func (l Layout) Delete(_ context.Context, ref reference.Reference) error {
	index, err := l.readIndex()
	if err != nil {
		return err
	}

	repoRef := l.ref.Merge(ref)
	idx := -1
	for i, m := range index.Manifests {
		if m.Annotations[ocispecv1.AnnotationRefName] == repoRef.Ref {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("no image tagged %q in layout %q", repoRef.Ref, l.ref.Path)
	}

	log.WithFields(map[string]any{
		"ref":    repoRef.Ref,
		"digest": index.Manifests[idx].Digest,
	}).Debug("deleting image from layout")

	// Blobs reachable from the image being removed.
	victim, err := l.referencedBlobs(index.Manifests[idx])
	if err != nil {
		return err
	}

	// Remove the index entry, then collect the blobs still reachable from every
	// surviving entry: those must be kept even if the victim also referenced
	// them (shared layers / doubly-tagged manifests).
	remaining := make([]ocispecv1.Descriptor, 0, len(index.Manifests)-1)
	remaining = append(remaining, index.Manifests[:idx]...)
	remaining = append(remaining, index.Manifests[idx+1:]...)

	keep := make(map[digest.Digest]struct{})
	for _, m := range remaining {
		refs, err := l.referencedBlobs(m)
		if err != nil {
			return err
		}
		for d := range refs {
			keep[d] = struct{}{}
		}
	}

	// Candidate blobs for removal: reachable from the victim, referenced by no
	// survivor. Sorted for deterministic behaviour and log output.
	var orphans []digest.Digest
	for d := range victim {
		if _, shared := keep[d]; !shared {
			orphans = append(orphans, d)
		}
	}
	sort.Slice(orphans, func(i, j int) bool { return orphans[i] < orphans[j] })

	// A currently-mounted layer must not be pulled out from under a live overlay
	// mount. Abort before mutating the index so the layout stays intact. Only
	// unpack-mode layers become overlay lowerdirs, so the check is scoped to it.
	if l.unpack {
		mounted, err := l.mountedOrphans(orphans)
		if err != nil {
			return fmt.Errorf("failed to check mounted layers: %w", err)
		}
		if len(mounted) > 0 {
			return fmt.Errorf("cannot delete %q: %d layer(s) are currently mounted "+
				"(%s); unmount them first with 'oci-packer umount'",
				repoRef.Ref, len(mounted), strings.Join(digestsToStrings(mounted), ", "))
		}
	}

	// Persist the trimmed index before removing blobs. If blob removal is
	// interrupted the index no longer references the orphans, so the layout
	// remains consistent (at worst a few unreferenced blobs linger on disk).
	index.Manifests = remaining
	if err = l.writeIndex(index); err != nil {
		return err
	}

	for _, d := range orphans {
		if err = os.RemoveAll(l.getBlobPath(d)); err != nil {
			log.WithError(err).WithField("digest", d).Warn("failed to remove blob")
		} else {
			log.WithField("digest", d).Debug("removed blob")
		}
	}

	log.WithFields(map[string]any{
		"ref":            repoRef.Ref,
		"blobs_removed":  len(orphans),
		"blobs_retained": len(victim) - len(orphans),
	}).Debug("image deleted from layout")
	return nil
}

// referencedBlobs returns the set of blob digests reachable from desc, including
// desc's own digest. An index recurses into its child manifests; an image
// manifest contributes its config and layer digests.
func (l Layout) referencedBlobs(desc ocispecv1.Descriptor) (map[digest.Digest]struct{}, error) {
	seen := make(map[digest.Digest]struct{})
	if err := l.collectBlobs(desc, seen); err != nil {
		return nil, err
	}
	return seen, nil
}

func (l Layout) collectBlobs(desc ocispecv1.Descriptor, seen map[digest.Digest]struct{}) error {
	if _, ok := seen[desc.Digest]; ok {
		return nil
	}
	seen[desc.Digest] = struct{}{}

	switch desc.MediaType {
	case ocispecv1.MediaTypeImageIndex, images.MediaTypeDockerSchema2ManifestList:
		var index ocispecv1.Index
		if err := l.readJSONBlob(desc.Digest, &index); err != nil {
			return err
		}
		for _, m := range index.Manifests {
			if err := l.collectBlobs(m, seen); err != nil {
				return err
			}
		}

	case ocispecv1.MediaTypeImageManifest, images.MediaTypeDockerSchema2Manifest:
		var manifest ocispecv1.Manifest
		if err := l.readJSONBlob(desc.Digest, &manifest); err != nil {
			return err
		}
		seen[manifest.Config.Digest] = struct{}{}
		for _, layer := range manifest.Layers {
			seen[layer.Digest] = struct{}{}
		}
	}
	return nil
}

func digestsToStrings(ds []digest.Digest) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.String()
	}
	return out
}
