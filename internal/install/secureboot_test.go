package install

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tuna-os/tacklebox/internal/runner"
)

// The menu is derived from the BLS entries, so this asserts the derivation
// keeps the kernel, initrd and options intact.
func TestWriteGrubConfigDerivesTheMenuFromTheBLSEntries(t *testing.T) {
	esp := t.TempDir()
	entries := filepath.Join(esp, "loader", "entries")
	if err := os.MkdirAll(entries, 0755); err != nil {
		t.Fatal(err)
	}
	bls := "title Fedora installer\nsort-key 00-tbox-installer\n" +
		"linux /images/pxeboot/installer/vmlinuz\n" +
		"initrd /images/pxeboot/installer/initrd.img\n" +
		"options root=tbox:CDLABEL=TECT rd.live.overlay=tmpfs\n"
	if err := os.WriteFile(filepath.Join(entries, "installer.conf"), []byte(bls), 0644); err != nil {
		t.Fatal(err)
	}

	// Capture the write instead of shelling out to sudo tee.
	var written string
	paths := map[string]bool{}
	old := runner.RunFn
	defer func() { runner.RunFn = old }()
	runner.RunFn = func(stdin io.Reader, _ string, args ...string) error {
		if len(args) > 1 && args[0] == "tee" {
			body, err := io.ReadAll(stdin)
			if err != nil {
				return err
			}
			written = string(body)
			paths[strings.TrimPrefix(args[1], esp+"/")] = true
		}
		return nil
	}

	if err := WriteGrubConfig(esp, GrubConfigDirs("fedora")); err != nil {
		t.Fatalf("WriteGrubConfig: %v", err)
	}
	for _, want := range []string{
		"set timeout=3",
		"menuentry 'Fedora installer'",
		"linux /images/pxeboot/installer/vmlinuz root=tbox:CDLABEL=TECT rd.live.overlay=tmpfs",
		"initrd /images/pxeboot/installer/initrd.img",
	} {
		if !strings.Contains(written, want) {
			t.Errorf("grub.cfg missing %q, got:\n%s", want, written)
		}
	}
	// Both candidates: which one a signed GRUB reads depends on its build.
	for _, want := range []string{"EFI/BOOT/grub.cfg", "EFI/fedora/grub.cfg"} {
		if !paths[want] {
			t.Errorf("grub.cfg not written to %s; wrote %v", want, paths)
		}
	}
}

// An empty menu must be refused rather than written: GRUB finding no menuentry
// drops to its own shell, which on installer media looks like broken media.
func TestWriteGrubConfigRefusesAnEmptyMenu(t *testing.T) {
	esp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(esp, "loader", "entries"), 0755); err != nil {
		t.Fatal(err)
	}
	old := runner.RunFn
	defer func() { runner.RunFn = old }()
	runner.RunFn = func(io.Reader, string, ...string) error { return nil }

	if err := WriteGrubConfig(esp, GrubConfigDirs("fedora")); err == nil {
		t.Fatal("expected a refusal when no BLS entry is usable")
	}
}
