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
