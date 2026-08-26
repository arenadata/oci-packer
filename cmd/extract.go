package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/arenadata/oci-packer/internal/logger"
	"github.com/arenadata/oci-packer/pkg/extract"
	"github.com/arenadata/oci-packer/pkg/registry/reference"
)

var extractCmd = &cobra.Command{
	Use:   "extract <oci://layout:repo:tag> <dir>",
	Short: "Write the directory that would rebuild an artifact: pack.yaml and every packed file",
	Long: `Extract an artifact from an OCI layout into a directory — the inverse of
packing: pack.yaml with the artifact's type, annotations and items, every packed
file at its title (under the directory a dir:// item named), and a mounted
image's config as image.json when --image-config is set.

A mounted member becomes a cr://<registry>/<repo>@<digest> item; an artifact
mounted among the members is extracted too, into a sibling directory named by
the --name-by annotation, so a pack of artifacts comes out as one flat tree:

  oci-packer extract oci://./layout:packs/adh:2.1.0 ./out --registry registry.example --name-by io.horchestra.name --image-config
  ./out/adh/pack.yaml  ./out/adh/schema.json  ./out/kafka/pack.yaml  ./out/kafka/templates/…  ./out/kafka/image.json …`,
	Args: cobra.ExactArgs(2),
	Run:  extractCmdRun,
}

func init() {
	extractCmd.Flags().String("registry", "", "Registry host the cr:// items of the written pack files point at")
	extractCmd.Flags().String("name-by", "", "Annotation that names an artifact's directory (default org.opencontainers.image.title)")
	extractCmd.Flags().Bool("image-config", false, "Write a mounted image's config as image.json")
	rootCmd.AddCommand(extractCmd)
}

func extractCmdRun(cmd *cobra.Command, args []string) {
	log := logger.New("extract")
	l, err := openLayout(args[0])
	if err != nil {
		log.WithError(err).Fatal("failed to open layout")
	}
	parsedRef, err := reference.Parse(args[0])
	if err != nil {
		log.WithError(err).Fatal("failed to parse reference")
	}
	registryHost, _ := cmd.Flags().GetString("registry")
	nameBy, _ := cmd.Flags().GetString("name-by")
	imageConfig, _ := cmd.Flags().GetBool("image-config")
	// In an oci:// reference the path is the layout directory and Ref is
	// <repo>:<tag> or <repo>@<digest>; the repository is what cr:// items name.
	repo := parsedRef.Ref
	if i := strings.LastIndexAny(repo, ":@"); i >= 0 {
		repo = repo[:i]
	}
	dir, err := extract.Extract(cmd.Context(), l, reference.Reference{Ref: parsedRef.Ref}, args[1],
		extract.Options{Registry: registryHost, Repo: repo, NameBy: nameBy, ImageConfig: imageConfig})
	if err != nil {
		log.WithError(err).Fatal("extract failed")
	}
	fmt.Println(dir)
}
