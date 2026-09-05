package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// touch creates rel under root, holding body or a stub PE header.
func touch(t *testing.T, root, rel, body string) {
	t.Helper()
	if body == "" {
		body = "PE\x00"
	}
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

// resolve builds a fixture tree and returns the chain and staged names.
func resolve(t *testing.T, files []string, osRelease string) (*bootChain, []string) {
	t.Helper()
	root := t.TempDir()
	for _, f := range files {
		touch(t, root, f, "")
	}
	if osRelease != "" {
		touch(t, root, "etc/os-release", osRelease)
	}
	c := resolveBootloader(root)
	if c == nil {
		return nil, nil
	}
	var names []string
	for _, f := range c.Files {
		names = append(names, f.Name)
	}
	return c, names
}

// Version directory names keep the punctuation real packages use.
func TestResolveBootloaderFindsEveryLayout(t *testing.T) {
	for _, tc := range []struct {
		name       string
		files      []string
		osRelease  string
		wantKind   string
		wantVendor string
		wantStaged []string
	}{
		{
			name:       "systemd-boot from the image, not the host",
			files:      []string{"usr/lib/systemd/boot/efi/systemd-bootx64.efi"},
			wantKind:   "sdboot",
			wantStaged: []string{"BOOTX64.EFI"},
		},
		{
			name:       "systemd-boot .signed boots unchanged as BOOTX64.EFI",
			files:      []string{"usr/lib/systemd/boot/efi/systemd-bootx64.efi.signed"},
			wantKind:   "sdboot",
			wantStaged: []string{"BOOTX64.EFI"},
		},
		{
			name:       "aarch64 systemd-boot lands as BOOTAA64.EFI",
			files:      []string{"usr/lib/systemd/boot/efi/systemd-bootaa64.efi"},
			wantKind:   "sdboot",
			wantStaged: []string{"BOOTAA64.EFI"},
		},
		{
			name: "bootupd single vendor directory",
			files: []string{
				"usr/lib/bootupd/updates/EFI/fedora/shimx64.efi",
				"usr/lib/bootupd/updates/EFI/fedora/grubx64.efi",
				"usr/lib/bootupd/updates/EFI/fedora/mmx64.efi",
			},
			wantKind:   "grub2",
			wantVendor: "fedora",
			wantStaged: []string{"BOOTX64.EFI", "grubx64.efi", "mmx64.efi"},
		},
		{
			name: "ostree-boot",
			files: []string{
				"usr/lib/ostree-boot/efi/EFI/centos/shimx64.efi",
				"usr/lib/ostree-boot/efi/EFI/centos/grubx64.efi",
			},
			wantKind:   "grub2",
			wantVendor: "centos",
			wantStaged: []string{"BOOTX64.EFI", "grubx64.efi"},
		},
		{
			name: "versioned payloads, fedora epoch colon",
			files: []string{
				"usr/lib/efi/grub2/1:2.12-64.fc44/EFI/fedora/grubx64.efi",
				"usr/lib/efi/shim/16.1-5/EFI/fedora/shimx64.efi",
				"usr/lib/efi/shim/16.1-5/EFI/fedora/mmx64.efi",
			},
			wantKind:   "grub2",
			wantVendor: "fedora",
			wantStaged: []string{"BOOTX64.EFI", "grubx64.efi", "mmx64.efi"},
		},
		{
			name: "versioned payloads, shim tree also holds an EFI/BOOT",
			files: []string{
				"usr/lib/efi/grub2/1+2.14+3/EFI/debian/grubx64.efi",
				"usr/lib/efi/shim/1.51+16.1-2/EFI/BOOT/grubx64.efi",
				"usr/lib/efi/shim/1.51+16.1-2/EFI/debian/shimx64.efi",
				"usr/lib/efi/shim/1.51+16.1-2/EFI/debian/mmx64.efi",
			},
			wantKind:   "grub2",
			wantVendor: "debian",
			wantStaged: []string{"BOOTX64.EFI", "grubx64.efi", "mmx64.efi"},
		},
		{
			// Measured on a real arm64 fedora-bootc.
			name: "aarch64 versioned payloads",
			files: []string{
				"usr/lib/efi/grub2/1:2.12-64.fc44/EFI/fedora/grubaa64.efi",
				"usr/lib/efi/shim/16.1-5/EFI/fedora/shimaa64.efi",
				"usr/lib/efi/shim/16.1-5/EFI/fedora/mmaa64.efi",
			},
			wantKind:   "grub2",
			wantVendor: "fedora",
			wantStaged: []string{"BOOTAA64.EFI", "grubaa64.efi", "mmaa64.efi"},
		},
		{
			name: "aarch64 bootupd single vendor directory",
			files: []string{
				"usr/lib/bootupd/updates/EFI/fedora/shimaa64.efi",
				"usr/lib/bootupd/updates/EFI/fedora/grubaa64.efi",
			},
			wantKind:   "grub2",
			wantVendor: "fedora",
			wantStaged: []string{"BOOTAA64.EFI", "grubaa64.efi"},
		},
		{
			name: "aarch64 ostree-boot",
			files: []string{
				"usr/lib/ostree-boot/efi/EFI/centos/shimaa64.efi",
				"usr/lib/ostree-boot/efi/EFI/centos/grubaa64.efi",
			},
			wantKind:   "grub2",
			wantVendor: "centos",
			wantStaged: []string{"BOOTAA64.EFI", "grubaa64.efi"},
		},
		{
			// x64 is tried first.
			name: "an image carrying both architectures takes x64",
			files: []string{
				"usr/lib/bootupd/updates/EFI/fedora/shimx64.efi",
				"usr/lib/bootupd/updates/EFI/fedora/grubx64.efi",
				"usr/lib/bootupd/updates/EFI/fedora/shimaa64.efi",
				"usr/lib/bootupd/updates/EFI/fedora/grubaa64.efi",
			},
			wantKind:   "grub2",
			wantVendor: "fedora",
			wantStaged: []string{"BOOTX64.EFI", "grubx64.efi"},
		},
		{
			// MokManager is optional.
			name: "versioned payloads with no MokManager",
			files: []string{
				"usr/lib/efi/grub2/1:2.12-64.fc44/EFI/fedora/grubx64.efi",
				"usr/lib/efi/shim/16.1-5/EFI/fedora/shimx64.efi",
			},
			wantKind:   "grub2",
			wantVendor: "fedora",
			wantStaged: []string{"BOOTX64.EFI", "grubx64.efi"},
		},
		{
			name: "deb families take the vendor from os-release",
			files: []string{
				"usr/lib/shim/shimx64.efi.signed",
				"usr/lib/grub/x86_64-efi-signed/grubx64.efi.signed",
				"usr/lib/shim/mmx64.efi.signed",
			},
			osRelease:  "NAME=\"Debian\"\nID=debian\n",
			wantKind:   "grub2",
			wantVendor: "debian",
			wantStaged: []string{"BOOTX64.EFI", "grubx64.efi", "mmx64.efi"},
		},
		{
			name: "ubuntu leaves its unsigned MokManager behind",
			files: []string{
				"usr/lib/shim/shimx64.efi.signed",
				"usr/lib/grub/x86_64-efi-signed/grubx64.efi.signed",
				"usr/lib/shim/mmx64.efi",
			},
			osRelease:  "ID=ubuntu\n",
			wantKind:   "grub2",
			wantVendor: "ubuntu",
			wantStaged: []string{"BOOTX64.EFI", "grubx64.efi"},
		},
		{
			name: "deb layout, os-release names no ID",
			files: []string{
				"usr/lib/shim/shimx64.efi.signed",
				"usr/lib/grub/x86_64-efi-signed/grubx64.efi.signed",
			},
			osRelease:  "NAME=\"Something\"\n",
			wantKind:   "grub2",
			wantVendor: "",
			wantStaged: []string{"BOOTX64.EFI", "grubx64.efi"},
		},
		{
			name: "deb layout, no os-release at all",
			files: []string{
				"usr/lib/shim/shimx64.efi.signed",
				"usr/lib/grub/x86_64-efi-signed/grubx64.efi.signed",
			},
			wantKind:   "grub2",
			wantVendor: "",
			wantStaged: []string{"BOOTX64.EFI", "grubx64.efi"},
		},
		{
			name: "aarch64 deb layout uses the arm64 grub directory",
			files: []string{
				"usr/lib/shim/shimaa64.efi.signed",
				"usr/lib/grub/arm64-efi-signed/grubaa64.efi.signed",
			},
			osRelease:  "ID=debian\n",
			wantKind:   "grub2",
			wantVendor: "debian",
			wantStaged: []string{"BOOTAA64.EFI", "grubaa64.efi"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, staged := resolve(t, tc.files, tc.osRelease)
			if c == nil {
				t.Fatal("resolved no bootloader")
			}
			if c.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", c.Kind, tc.wantKind)
			}
			if c.Vendor != tc.wantVendor {
				t.Errorf("Vendor = %q, want %q", c.Vendor, tc.wantVendor)
			}
			if strings.Join(staged, ",") != strings.Join(tc.wantStaged, ",") {
				t.Errorf("staged %v, want %v", staged, tc.wantStaged)
			}
		})
	}
}

// systemd-boot wins when the image ships both.
func TestResolveBootloaderPrefersSystemdBoot(t *testing.T) {
	c, staged := resolve(t, []string{
		"usr/lib/systemd/boot/efi/systemd-bootx64.efi",
		"usr/lib/bootupd/updates/EFI/fedora/shimx64.efi",
		"usr/lib/bootupd/updates/EFI/fedora/grubx64.efi",
	}, "")
	if c.Kind != "sdboot" {
		t.Errorf("Kind = %q, want sdboot", c.Kind)
	}
	if strings.Join(staged, ",") != "BOOTX64.EFI" {
		t.Errorf("staged %v, want only BOOTX64.EFI", staged)
	}
}

// An image with no bootloader resolves to nothing rather than an error.
func TestResolveBootloaderReportsNothingForAnEmptyImage(t *testing.T) {
	if c, _ := resolve(t, nil, ""); c != nil {
		t.Errorf("resolved %+v from an image with no bootloader", c)
	}
}

// A GRUB with no shim beside it is not a chain.
func TestResolveBootloaderRefusesGrubWithoutShim(t *testing.T) {
	c, _ := resolve(t, []string{
		"usr/lib/efi/grub2/1:2.12-64.fc44/EFI/fedora/grubx64.efi",
	}, "")
	if c != nil {
		t.Errorf("resolved %+v with no shim present", c)
	}
}

func TestOsReleaseID(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"plain", "ID=debian\n", "debian"},
		{"quoted", "ID=\"ubuntu\"\n", "ubuntu"},
		{"among others", "NAME=\"Debian\"\nID=debian\nVERSION_ID=13\n", "debian"},
		{"no ID", "NAME=\"Something\"\n", ""},
		{"ID_LIKE is not ID", "ID_LIKE=debian\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			touch(t, root, "etc/os-release", tc.body)
			if got := osReleaseID(filepath.Join(root, "etc/os-release")); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
	// A missing file is not an error.
	if got := osReleaseID(filepath.Join(t.TempDir(), "etc/os-release")); got != "" {
		t.Errorf("got %q from a missing file", got)
	}
}

// bls stages one BLS entry the way WriteBLSEntry does.
func bls(t *testing.T, esp, id, sortKey, title, kernel string) {
	t.Helper()
	touch(t, esp, filepath.Join("loader/entries", id+".conf"),
		"title "+title+"\nsort-key "+sortKey+"\nlinux "+kernel+
			"\ninitrd /images/pxeboot/"+id+"/initrd.img\noptions root=tbox:CDLABEL=X\n")
}

// menuTitles returns the menuentry titles of a written grub.cfg, in order.
func menuTitles(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		if rest, ok := strings.CutPrefix(line, "menuentry '"); ok {
			out = append(out, strings.TrimSuffix(rest, "' {"))
		}
	}
	return out
}

// The menu carries what the BLS entries carry, into every directory asked
// for, so neither loader can name a kernel the other does not.
func TestWriteGrubConfigDerivesTheMenuFromTheBLSEntries(t *testing.T) {
	esp := t.TempDir()
	bls(t, esp, "fedora", "00-tbox-fedora", "Fedora (live)", "/images/pxeboot/fedora/vmlinuz")

	if err := WriteGrubConfig(esp, "fedora"); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"EFI/BOOT", "EFI/fedora"} {
		body, err := os.ReadFile(filepath.Join(esp, d, "grub.cfg"))
		if err != nil {
			t.Fatalf("%s: %v", d, err)
		}
		for _, want := range []string{
			"menuentry 'Fedora (live)'",
			"linux /images/pxeboot/fedora/vmlinuz root=tbox:CDLABEL=X",
			"initrd /images/pxeboot/fedora/initrd.img",
		} {
			if !strings.Contains(string(body), want) {
				t.Errorf("%s/grub.cfg missing %q\n%s", d, want, body)
			}
		}
	}
}

// Every environment gets an entry, ordered by sort-key.
func TestWriteGrubConfigOrdersBySortKey(t *testing.T) {
	esp := t.TempDir()
	bls(t, esp, "cc", "0-tbox-3", "Third", "/k/c")
	bls(t, esp, "aa", "0-tbox-1", "First", "/k/a")
	bls(t, esp, "bb", "0-tbox-2", "Second", "/k/b")

	if err := WriteGrubConfig(esp, ""); err != nil {
		t.Fatal(err)
	}
	got := menuTitles(t, filepath.Join(esp, "EFI/BOOT/grub.cfg"))
	if strings.Join(got, ",") != "First,Second,Third" {
		t.Errorf("got %v", got)
	}
}

// GRUB boots its first entry, so the one loader.conf names goes first.
func TestWriteGrubConfigPutsTheLoaderDefaultFirst(t *testing.T) {
	esp := t.TempDir()
	bls(t, esp, "aa", "0-tbox-1", "First", "/k/a")
	bls(t, esp, "bb", "0-tbox-2", "Second", "/k/b")
	touch(t, esp, "loader/loader.conf", "timeout 5\ndefault bb.conf\neditor no\n")

	if err := WriteGrubConfig(esp, ""); err != nil {
		t.Fatal(err)
	}
	if got := menuTitles(t, filepath.Join(esp, "EFI/BOOT/grub.cfg")); got[0] != "Second" {
		t.Errorf("got %v, want Second first", got)
	}
}

// `default *` names no entry, so sort-key order stands.
func TestWriteGrubConfigKeepsSortOrderForAGlobDefault(t *testing.T) {
	esp := t.TempDir()
	bls(t, esp, "aa", "0-tbox-1", "First", "/k/a")
	bls(t, esp, "bb", "0-tbox-2", "Second", "/k/b")
	touch(t, esp, "loader/loader.conf", "timeout 5\ndefault *\neditor no\n")

	if err := WriteGrubConfig(esp, ""); err != nil {
		t.Fatal(err)
	}
	if got := menuTitles(t, filepath.Join(esp, "EFI/BOOT/grub.cfg")); got[0] != "First" {
		t.Errorf("got %v, want First first", got)
	}
}

// An empty menu that can't boot anything.
func TestWriteGrubConfigRefusesToWriteAnEmptyMenu(t *testing.T) {
	if err := WriteGrubConfig(t.TempDir(), ""); err == nil {
		t.Error("wrote a menu with no BLS entries")
	}
}

func TestWriteGrubConfigRefusesAnEntryWithNoKernel(t *testing.T) {
	esp := t.TempDir()
	touch(t, esp, "loader/entries/broken.conf", "title Broken\nsort-key 0-tbox-x\n")
	if err := WriteGrubConfig(esp, ""); err == nil {
		t.Error("accepted a BLS entry with no linux line")
	}
}
