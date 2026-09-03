package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tuna-os/tacklebox/internal/purefs"
	"github.com/tuna-os/tacklebox/internal/runner"
)

// GrubConfigDirs lists the directories a signed GRUB may read grub.cfg from:
// its own directory and the vendor directory the shim+GRUB pair came from.
//
// Both are written because the embedded prefix resolves to either depending on
// the build — the same reason purefs.BootChain carries Vendor. Fedora 44's
// grubx64.efi carries the string /EFI/fedora and loads %s/grub.cfg from it.
func GrubConfigDirs(vendor string) []string {
	dirs := []string{"EFI/BOOT"}
	if vendor != "" {
		dirs = append(dirs, "EFI/"+vendor)
	}
	return dirs
}

// StageSignedChain copies the image's shim+GRUB pair into the ESP staging
// tree, so the ISO boots with Secure Boot enabled. shim is signed by the
// Microsoft UEFI CA that firmware carries in db, and loads a GRUB signed by
// the distribution CA shim embeds; nothing is signed here, the binaries ship
// in the image.
//
// Staged without a shim ahead of it, a systemd-boot BOOTX64.EFI is rejected
// with `Access Denied -- rejected probably by Secure Boot`, then `No bootable
// option or device was found` (measured on OVMF_CODE.secboot.fd with the
// default keys enrolled). Signing it does not help on its own: firmware
// validates against db, and no distribution CA is in db — that is what shim
// is for.
//
// The pair is the common shape: bootupd ships it on the Fedora/RHEL-derived
// images that make up most of the bootc ecosystem (bluefin, aurora, bonito,
// centos, the Fedora bootc bases). The deb families keep the same pair under
// /usr/lib/shim and /usr/lib/grub/x86_64-efi-signed, which is a fourth layout
// rather than an absence. Returns false with no error for the images that ship
// systemd-boot instead — the TunaOS editions, and any image carrying neither
// pair. The caller then falls back to ExtractEFIBinary.
//
// purefs.DetectBootChain resolves the same thing from an unpacked OCI tree and
// is the better home for this once IsoTarget can supply one. Two differences
// until then:
//
//   - It probes a tree; this runs the probe inside the image, which is what
//     IsoTarget has — an image reference, not a tree.
//   - It resolves the pair; this also stages it, which purebuild and tbwasm
//     each do for themselves.
//
// The systemd-boot precedence is upstream's: an image shipping both keeps the
// sdboot path, so adding this one cannot change a previously-working build.
func StageSignedChain(image, espStaging string) (string, bool, error) {
	tmpDir, err := os.MkdirTemp("", "tbox-sb-*")
	if err != nil {
		return "", false, fmt.Errorf("mktemp: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	if err := os.Chmod(tmpDir, 0777); err != nil {
		return "", false, fmt.Errorf("chmod tmpdir: %w", err)
	}

	// Globbed rather than branched on architecture: the image is the authority
	// on which of BOOTX64/BOOTAA64 it ships, and a glob is shorter than a table
	// that has to be kept in step with it. mm*.efi is MokManager, shim's own
	// fallback — harmless when unused and the only way out if a key ever does
	// need enrolling, so it travels with shim rather than being left behind.
	// Absence is a marker rather than a non-zero exit, so an error from the run
	// below is unambiguously a real one: an image shipping no pair and an image
	// that could not start must not look alike.
	// The three layouts purefs.DetectBootChain knows, in its order: bootupd's
	// older single-vendor-directory shape, ostree-boot, then bootupd's current
	// versioned payloads. All three are in use across the Fedora/RHEL-derived
	// images; probing only the last finds no pair on the traditional-ostree
	// ones (bluefin, aurora, bonito) and falls back to unsigned media without
	// saying so. The deb layout is a fourth arm below, and upstream knows none
	// of it.
	//
	// Hardlinks need no handling here. Fedora ships shimx64.efi, shim.efi and
	// EFI/BOOT/BOOTX64.EFI as one inode, and cp inside the image follows them;
	// purefs resolves them by hand only because it walks an unpacked tar where
	// every name after the first is a link entry.
	const script = `set -eu
for p in /usr/lib/systemd/boot/efi/systemd-bootx64.efi \
         /usr/lib/systemd/boot/efi/systemd-bootx64.efi.signed; do
    if [ -f "$p" ]; then echo "CHAIN=none"; exit 0; fi
done
shim=""; grub=""; mok=""; vendor=""
for base in /usr/lib/bootupd/updates/EFI /usr/lib/ostree-boot/efi/EFI; do
    if [ ! -d "$base" ]; then continue; fi
    for vd in "$base"/*/; do
        if [ -f "${vd}shimx64.efi" ] && [ -f "${vd}grubx64.efi" ]; then
            shim="${vd}shimx64.efi"
            grub="${vd}grubx64.efi"
            vendor=$(basename "$vd")
            if [ -f "${vd}mmx64.efi" ]; then mok="${vd}mmx64.efi"; fi
            break
        fi
    done
    if [ -n "$shim" ]; then break; fi
done
if [ -z "$shim" ]; then
    grub=$(ls /usr/lib/efi/grub2/*/EFI/*/grubx64.efi 2>/dev/null | head -1)
    if [ -n "$grub" ]; then
        vendor=$(basename "$(dirname "$grub")")
        shim=$(ls /usr/lib/efi/shim/*/EFI/"$vendor"/shimx64.efi 2>/dev/null | head -1)
        mok=$(ls /usr/lib/efi/shim/*/EFI/"$vendor"/mmx64.efi 2>/dev/null | head -1)
        if [ -z "$shim" ]; then grub=""; fi
    fi
fi
# The deb families, which none of the layouts above reach: two separate trees,
# a .signed suffix, and no vendor directory at all. The vendor is the
# os-release ID, which is what their grubx64 embeds as its prefix. Ubuntu also
# ships shimx64.efi.signed.latest and .previous; .signed is the one both
# families have. Its MokManager is unsigned there and is left behind rather
# than staged, since an unsigned mmx64 cannot load under Secure Boot anyway.
if [ -z "$shim" ]; then
    if [ -f /usr/lib/shim/shimx64.efi.signed ] &&
       [ -f /usr/lib/grub/x86_64-efi-signed/grubx64.efi.signed ]; then
        shim=/usr/lib/shim/shimx64.efi.signed
        grub=/usr/lib/grub/x86_64-efi-signed/grubx64.efi.signed
        vendor=$(. /etc/os-release && echo "$ID")
        if [ -f /usr/lib/shim/mmx64.efi.signed ]; then
            mok=/usr/lib/shim/mmx64.efi.signed
        fi
    fi
fi
if [ -z "$shim" ] || [ -z "$grub" ]; then echo "CHAIN=none"; exit 0; fi
cp "$shim" /dest/BOOTX64.EFI
cp "$grub" /dest/grubx64.efi
if [ -n "$mok" ]; then cp "$mok" /dest/mmx64.efi; fi
echo "VENDOR=$vendor"
echo "CHAIN=ok"`

	upodman := UserPodmanPrefix()
	args := append(upodman[1:],
		"run", "--rm",
		"--security-opt", "label=disable",
		"--log-driver", "k8s-file",
		"-v", tmpDir+":/dest",
		"--entrypoint", "/bin/sh",
		image, "-c", script)
	out, err := runner.Output(upodman[0], args...)
	if err != nil {
		return "", false, fmt.Errorf("probe %s for a signed EFI chain: %w", image, err)
	}
	if !strings.Contains(string(out), "CHAIN=ok") {
		return "", false, nil
	}
	vendor := ""
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "VENDOR="); ok {
			vendor = rest
			break
		}
	}

	// Everything lands in EFI/BOOT: firmware loads shim from the removable
	// media path, and shim looks for its second stage in its own directory.
	destDir := filepath.Join(espStaging, "EFI", "BOOT")
	if err := runner.Run("sudo", "mkdir", "-p", destDir); err != nil {
		return "", false, err
	}
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return "", false, fmt.Errorf("read staged chain: %w", err)
	}
	for _, e := range entries {
		if err := runner.Run("sudo", "cp",
			filepath.Join(tmpDir, e.Name()), filepath.Join(destDir, e.Name())); err != nil {
			return "", false, fmt.Errorf("place %s: %w", e.Name(), err)
		}
	}
	return vendor, true, nil
}

// WriteGrubConfig renders the default BLS entry already staged on the ESP as a
// GRUB menu, into each of dirs.
//
// The BLS entry is the source, so the sdboot and GRUB paths cannot disagree
// about which kernel boots or with what options, and the menu itself comes
// from purefs.LiveGrubCfg so there is one renderer.
func WriteGrubConfig(espStaging string, dirs []string) error {
	entryDir := filepath.Join(espStaging, "loader", "entries")
	entries, err := os.ReadDir(entryDir)
	if err != nil {
		return fmt.Errorf("read BLS entries: %w", err)
	}

	var title, linux, initrd, options string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".conf") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(entryDir, e.Name()))
		if err != nil {
			return fmt.Errorf("read %s: %w", e.Name(), err)
		}
		field := func(key string) string {
			for _, line := range strings.Split(string(body), "\n") {
				if rest, ok := strings.CutPrefix(strings.TrimSpace(line), key+" "); ok {
					return strings.TrimSpace(rest)
				}
			}
			return ""
		}
		k, i := field("linux"), field("initrd")
		if k == "" || i == "" {
			continue
		}
		// WriteBLSEntry gives the default entry a 00-tbox- sort key; take it
		// when present, otherwise the first usable one (ReadDir is sorted).
		isDefault := strings.HasPrefix(field("sort-key"), "00-tbox-")
		if linux == "" || isDefault {
			title, linux, initrd, options = field("title"), k, i, field("options")
		}
		if isDefault {
			break
		}
	}
	if linux == "" {
		return fmt.Errorf("no usable BLS entry under %s to build a grub menu from", entryDir)
	}
	if title == "" {
		title = "Live environment"
	}

	menu := purefs.LiveGrubCfg(title, linux, initrd, options)
	for _, prefix := range dirs {
		dir := filepath.Join(espStaging, prefix)
		if err := runner.Run("sudo", "mkdir", "-p", dir); err != nil {
			return err
		}
		if err := runner.RunWithStdin(strings.NewReader(menu),
			"sudo", "tee", filepath.Join(dir, "grub.cfg")); err != nil {
			return fmt.Errorf("write %s/grub.cfg: %w", prefix, err)
		}
	}
	return nil
}
