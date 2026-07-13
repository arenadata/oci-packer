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

package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/arenadata/oci-packer/internal/logger"
	"github.com/arenadata/oci-packer/pkg/registry/oci-layout"
	"github.com/arenadata/oci-packer/pkg/registry/reference"

	"github.com/docker/go-units"
	"github.com/opencontainers/go-digest"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:     "list <oci://layout[:repo:tag]>",
	Aliases: []string{"ls"},
	Short:   "List images in an OCI layout, or the components of one Pack",
	Long: `List the contents of an OCI layout. The reference decides what is shown:

  - oci://<layout-dir>                    lists every image/artifact in the layout
    e.g. oci://./layout

  - oci://<layout-dir>:<repository>:<tag> lists the components (config and
    e.g. oci://./layout:example/service:v1  layers) of that one Pack

For a Pack, each layer's title is taken from its org.opencontainers.image.title
annotation (the packed file name); a multi-platform index lists the components
of every platform variant.`,
	Args: cobra.ExactArgs(1),
	Run:  listCmdRun,
}

func init() {
	rootCmd.AddCommand(listCmd)
}

// openLayout parses an oci:// reference and opens the existing layout it points
// to, returning the concrete *layout.Layout so layout-specific methods (List,
// PackComponents, Delete, …) can be called.
func openLayout(src string) (*layout.Layout, error) {
	parsedRef, err := reference.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("failed to parse reference %q: %w", src, err)
	}

	resolver, err := layout.Open(parsedRef)
	if err != nil {
		return nil, fmt.Errorf("failed to open OCI layout: %w", err)
	}
	l, ok := resolver.(*layout.Layout)
	if !ok {
		return nil, fmt.Errorf("reference %q is not an OCI layout", src)
	}
	return l, nil
}

func listCmdRun(cmd *cobra.Command, args []string) {
	src := args[0]
	log := logger.New("list")

	l, err := openLayout(src)
	if err != nil {
		log.WithError(err).Fatal("failed to open layout")
	}

	// A bare layout reference lists every entry; a reference that also selects a
	// repo:tag (or @digest) drills into that single Pack's components. This is
	// the same boundary the parser uses: the first ':'/'@' after the layout path.
	if hasImageRef(src) {
		err = printPack(cmd.Context(), l)
	} else {
		err = printImages(l)
	}
	if err != nil {
		log.WithError(err).Fatal("list failed")
	}
}

// hasImageRef reports whether an oci:// reference selects an image inside the
// layout (a :repo:tag or @digest after the layout directory) rather than naming
// the layout directory alone.
func hasImageRef(src string) bool {
	_, rest, found := strings.Cut(src, "://")
	if !found {
		rest = src
	}
	return strings.ContainsAny(rest, ":@")
}

func printImages(l *layout.Layout) error {
	images, err := l.List()
	if err != nil {
		return fmt.Errorf("failed to read layout index: %w", err)
	}

	if len(images) == 0 {
		fmt.Println("layout is empty")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "REF\tKIND\tDIGEST\tPLATFORM\tARTIFACT TYPE\tSIZE")
	for _, img := range images {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			orDash(img.Ref), img.Kind, shortDigest(img.Digest),
			orDash(img.Platform), orDash(img.ArtifactType), units.BytesSize(float64(img.Size)))
	}
	return tw.Flush()
}

func printPack(ctx context.Context, l *layout.Layout) error {
	pack, err := l.PackComponents(ctx, reference.Reference{})
	if err != nil {
		return fmt.Errorf("failed to read pack components: %w", err)
	}

	fmt.Printf("Pack %s\n", orDash(pack.Ref))
	fmt.Printf("  digest:       %s\n", pack.Digest)
	fmt.Printf("  kind:         %s\n", pack.Kind)
	fmt.Printf("  artifactType: %s\n", orDash(pack.ArtifactType))

	multi := len(pack.Manifests) > 1
	for _, m := range pack.Manifests {
		fmt.Println()
		if multi || m.Platform != "" {
			fmt.Printf("Manifest %s  platform=%s  artifactType=%s\n",
				shortDigest(m.Digest), orDash(m.Platform), orDash(m.ArtifactType))
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "ROLE\tTITLE\tDIGEST\tMEDIA TYPE\tSIZE")
		for _, c := range m.Components {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
				c.Role, orDash(c.Title), shortDigest(c.Digest), c.MediaType,
				units.BytesSize(float64(c.Size)))
		}
		if err = tw.Flush(); err != nil {
			return err
		}
	}
	return nil
}

// shortDigest renders "sha256:" plus the first 12 hex characters of the digest.
func shortDigest(d digest.Digest) string {
	s := d.String()
	if enc := d.Encoded(); len(enc) > 12 {
		return d.Algorithm().String() + ":" + enc[:12]
	}
	return s
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
