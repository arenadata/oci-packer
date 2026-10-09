package packer

import (
	"context"
	"strings"
	"testing"

	"github.com/arenadata/oci-packer/pkg/registry/reference"
	"github.com/arenadata/oci-packer/pkg/registry/remote"
)

// A pack file's oci:// item is a repository of the registry the pack is
// pushed to — the image loaded there beforehand: it names no registry, and
// no local layout, which a registry cannot mount from. Refused before
// anything is built.
func TestValidate_Items(t *testing.T) {
	for from, want := range map[string]string{
		"oci://docker.io/library/postgres:18-alpine": "an item names no registry",
		"oci://localhost:5000/images/postgres:18":    "an item names no registry",
		"oci://localhost/images/postgres:18":         "an item names no registry",
		"oci://./layout:images/postgres:18":          "a local OCI layout",
		"oci:///home/user/layout:images/postgres:18": "a local OCI layout",
		"oci://layout-dir:images/postgres:18":        "a local OCI layout",
	} {
		err := Validate(Pack{Items: []Descriptor{{From: from, Platform: "linux/amd64"}}})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", from, err, want)
		}
	}
	for _, from := range []string{"oci://images/postgres:18", "oci://postgres:18", "oci://ee/adh/postgres@sha256:" + strings.Repeat("a", 64)} {
		if err := Validate(Pack{Items: []Descriptor{{From: from, Platform: "linux/amd64"}}}); err != nil {
			t.Errorf("%s: %v", from, err)
		}
		ref, err := reference.ParseItem(from)
		if err != nil || ref.Host != "" || len(ref.Path) == 0 {
			t.Errorf("%s: %+v %v", from, ref, err)
		}
	}
}

// The registry client refuses a mount from another registry itself, whoever asks.
func TestMountFrom_AnotherRegistry(t *testing.T) {
	r, err := remote.New("oci://registry.example.com/library/image:latest")
	if err != nil {
		t.Fatal(err)
	}
	src := reference.Reference{Scheme: reference.OciScheme, Host: "docker.io", Path: "library/postgres", Ref: "18"}
	if _, err := r.MountFrom(context.Background(), src); err == nil || !strings.Contains(err.Error(), "another registry") {
		t.Errorf("a mount from another registry must be refused: %v", err)
	}
}
