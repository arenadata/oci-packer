package extract

import (
	"context"
	"io"
	"testing"

	"github.com/arenadata/oci-packer/pkg/registry/reference"
	"github.com/arenadata/oci-packer/pkg/registry/regtest"
	"github.com/arenadata/oci-packer/pkg/registry/remote"
)

// One file of a pack in a registry, named the way a person writes it: oci://host/repo:tag:path.
func TestOpen_OverARegistry(t *testing.T) {
	res, _ := packed(t)
	srv := regtest.Serve(t, res, "packs/app")

	full := "oci://" + regtest.Host(srv) + "/packs/app:1.0:templates/nested/b.tmpl"
	ref, file, err := reference.ParseFile(full)
	if err != nil {
		t.Fatal(err)
	}
	src, err := remote.New(ref.String(), remote.WithPlainHttp())
	if err != nil {
		t.Fatal(err)
	}
	_, r, err := Open(context.Background(), src, reference.Reference{Ref: ref.Ref}, file, Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r)
	_ = r.Close()
	if string(b) != "b\n" {
		t.Fatalf("file = %q", b)
	}

	files, err := List(context.Background(), src, reference.Reference{Ref: ref.Ref}, Options{})
	if err != nil || len(files) != 3 {
		t.Fatalf("files = %v, %v", files, err)
	}
}
