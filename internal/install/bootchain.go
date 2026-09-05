package install

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tuna-os/tacklebox/internal/purefs"
	"github.com/tuna-os/tacklebox/internal/runner"
)

// EFI architecture suffixes probed, with the removable-media name firmware
// loads for each. Tried in order, so an image carrying both takes x64.
var bootloaderArches = []struct{ suffix, bootName string }{
	{"x64", "BOOTX64.EFI"},
	{"aa64", "BOOTAA64.EFI"},
}

// Directories a bootloader lives in.
const (
	osReleaseFile = "etc/os-release"
	sdbootDir     = "usr/lib/systemd/boot/efi"
	bootupdDir    = "usr/lib/bootupd/updates/EFI"
	ostreeDir     = "usr/lib/ostree-boot/efi/EFI"
	efiDir        = "usr/lib/efi"
	debShimDir    = "usr/lib/shim"
	debGrubDir    = "usr/lib/grub"
)

// Paths copied out of an image.
var bootloaderCandidates = []string{
	osReleaseFile,
	sdbootDir,
	bootupdDir,
	ostreeDir,
	efiDir,
	debShimDir,
	debGrubDir,
}

// copyCandidatesScript copies bootloaderCandidates from $ROOT to $DEST.
func copyCandidatesScript() string {
	var b strings.Builder
	b.WriteString("set -eu\n")
	for _, d := range bootloaderCandidates {
		fmt.Fprintf(&b, `if [ -e "$ROOT"/%[1]s ]; then mkdir -p "$DEST"/%[2]s; cp -a "$ROOT"/%[1]s "$DEST"/%[1]s; fi
`, shellEsc(d), shellEsc(filepath.Dir(d)))
	}
	return b.String()
}

// bootChain is the bootloader an image ships.
type bootChain struct {
	// Kind is "grub2" for a shim+GRUB pair, or "sdboot".
	Kind string
	// Vendor is the EFI directory a grub2 pair came from, or empty.
	Vendor string
	// Files are the binaries to place on the ESP, in order.
	Files []espFile
}

// espFile is a binary under the name it takes on the ESP.
type espFile struct{ Name, Src string }

// resolveBootloader picks the bootloader out of a copied candidate tree, or
// nil. systemd-boot wins over a shim+GRUB pair, as in purefs.DetectBootChain.
func resolveBootloader(root string) *bootChain {
	for _, arch := range bootloaderArches {
		a := arch.suffix
		for _, n := range []string{"systemd-boot" + a + ".efi", "systemd-boot" + a + ".efi.signed"} {
			p := filepath.Join(root, sdbootDir, n)
			if isFile(p) {
				return &bootChain{Kind: "sdboot", Files: []espFile{{arch.bootName, p}}}
			}
		}
		if c := resolveGrubPair(root, a, arch.bootName); c != nil {
			return c
		}
	}
	return nil
}

// resolveGrubPair looks for a shim+GRUB pair in the four layouts.
func resolveGrubPair(root, a, bootName string) *bootChain {
	pair := func(shim, grub, mok, vendor string) *bootChain {
		files := []espFile{{bootName, shim}, {"grub" + a + ".efi", grub}}
		if isFile(mok) {
			files = append(files, espFile{"mm" + a + ".efi", mok})
		}
		return &bootChain{Kind: "grub2", Vendor: vendor, Files: files}
	}

	// bootupd and ostree-boot: all three under one vendor directory.
	for _, base := range []string{bootupdDir, ostreeDir} {
		vendorDirs, _ := filepath.Glob(filepath.Join(root, base, "*"))
		sort.Strings(vendorDirs)
		for _, vd := range vendorDirs {
			shim, grub := filepath.Join(vd, "shim"+a+".efi"), filepath.Join(vd, "grub"+a+".efi")
			if isFile(shim) && isFile(grub) {
				return pair(shim, grub, filepath.Join(vd, "mm"+a+".efi"), filepath.Base(vd))
			}
		}
	}

	// Versioned payloads: separate version trees such as 1:2.12-64.fc44,
	// matched on vendor. Two versions take the first in string order, which
	// sorts 2.10 before 2.9.
	for _, grub := range globFiles(filepath.Join(root, efiDir, "grub2/*/EFI/*/grub"+a+".efi")) {
		vendor := filepath.Base(filepath.Dir(grub))
		for _, shim := range globFiles(filepath.Join(root, efiDir, "shim/*/EFI", vendor, "shim"+a+".efi")) {
			return pair(shim, grub, filepath.Join(filepath.Dir(shim), "mm"+a+".efi"), vendor)
		}
	}

	// Debian and Ubuntu: a .signed suffix and no vendor directory, so the
	// vendor comes from os-release. Only a .signed MokManager is taken.
	debDir := map[string]string{"x64": "x86_64-efi-signed", "aa64": "arm64-efi-signed"}[a]
	shim := filepath.Join(root, debShimDir, "shim"+a+".efi.signed")
	grub := filepath.Join(root, debGrubDir, debDir, "grub"+a+".efi.signed")
	if isFile(shim) && isFile(grub) {
		return pair(shim, grub, filepath.Join(root, debShimDir, "mm"+a+".efi.signed"),
			osReleaseID(filepath.Join(root, osReleaseFile)))
	}
	return nil
}

// osReleaseID reads ID from an os-release file, or "" if absent.
func osReleaseID(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(body), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "ID="); ok {
			return strings.Trim(rest, `"'`)
		}
	}
	return ""
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func globFiles(pattern string) []string {
	matches, _ := filepath.Glob(pattern)
	sort.Strings(matches)
	var out []string
	for _, m := range matches {
		if isFile(m) {
			out = append(out, m)
		}
	}
	return out
}

// StageBootloader copies image's own bootloader into destDir, the EFI/BOOT
// directory of the installer media's ESP. kind is "grub2" for a shim+GRUB
// pair, with vendor naming the EFI directory it came from, or
// "sdboot" with an empty vendor. An image with no bootloader, or one that
// cannot be read, falls back to the host's systemd-boot.
func StageBootloader(image, destDir string) (kind, vendor string, err error) {
	if err := runner.Run("sudo", "mkdir", "-p", destDir); err != nil {
		return "", "", err
	}

	hostFallback := func(why string) (string, string, error) {
		fmt.Printf(">>> [bootloader] %s; using the host's systemd-boot\n", why)
		if _, err := ExtractEFIBinary(image, destDir); err != nil {
			return "", "", err
		}
		return "sdboot", "", nil
	}

	copied, err := runScriptOnImageMount(image, copyCandidatesScript())
	if err != nil {
		return hostFallback(fmt.Sprintf("could not read %s (%v)", image, err))
	}
	defer os.RemoveAll(copied)

	chain := resolveBootloader(copied)
	if chain == nil {
		return hostFallback(image + " ships no bootloader")
	}

	for _, f := range chain.Files {
		if err := runner.Run("sudo", "cp", f.Src, filepath.Join(destDir, f.Name)); err != nil {
			return "", "", fmt.Errorf("place %s: %w", f.Name, err)
		}
	}
	if chain.Kind == "grub2" {
		fmt.Printf(">>> [bootloader] staged %s's shim+GRUB pair, vendor %s\n", image, chain.Vendor)
	} else {
		fmt.Printf(">>> [bootloader] staged %s's systemd-boot\n", image)
	}
	return chain.Kind, chain.Vendor, nil
}

// WriteGrubConfig renders the GRUB menu from the BLS entries staged under
// espStaging, into EFI/BOOT and the vendor directory when there is one.
// Both loaders read the same entries, so they cannot name different kernels.
// A GRUB embeds its vendor path as a prefix and falls back to the directory
// it was loaded from, so both are written.
func WriteGrubConfig(espStaging, vendor string) error {
	entries, err := stagedGrubEntries(espStaging)
	if err != nil {
		return err
	}
	dirs := []string{filepath.Join("EFI", "BOOT")}
	if vendor != "" {
		dirs = append(dirs, filepath.Join("EFI", vendor))
	}
	cfg := purefs.LiveGrubMenu(entries)
	for _, d := range dirs {
		dir := filepath.Join(espStaging, d)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "grub.cfg"), []byte(cfg), 0644); err != nil {
			return fmt.Errorf("write %s/grub.cfg: %w", d, err)
		}
	}
	return nil
}

// stagedGrubEntries reads the staged BLS entries in the order systemd-boot
// by sort-key, with the one loader.conf names as default first.
func stagedGrubEntries(espStaging string) ([]purefs.GrubEntry, error) {
	files, _ := filepath.Glob(filepath.Join(espStaging, "loader", "entries", "*.conf"))

	type staged struct {
		id, sortKey string
		entry       purefs.GrubEntry
	}
	var found []staged
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		s := staged{id: strings.TrimSuffix(filepath.Base(f), ".conf")}
		for _, line := range strings.Split(string(body), "\n") {
			key, val, ok := strings.Cut(strings.TrimSpace(line), " ")
			if !ok {
				continue
			}
			switch key {
			case "title":
				s.entry.Title = val
			case "sort-key":
				s.sortKey = val
			case "linux":
				s.entry.Kernel = val
			case "initrd":
				s.entry.Initrd = val
			case "options":
				s.entry.Kargs = val
			}
		}
		if s.entry.Kernel == "" {
			return nil, fmt.Errorf("BLS entry %s names no kernel", f)
		}
		found = append(found, s)
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no BLS entries under %s", espStaging)
	}

	sort.SliceStable(found, func(i, j int) bool { return found[i].sortKey < found[j].sortKey })
	if d := loaderDefault(espStaging); d != "" {
		sort.SliceStable(found, func(i, j int) bool { return found[i].id == d && found[j].id != d })
	}

	entries := make([]purefs.GrubEntry, len(found))
	for i, s := range found {
		entries[i] = s.entry
	}
	return entries, nil
}

// loaderDefault reads the default entry id from loader.conf, returning ""
// when the file is absent or names a glob.
func loaderDefault(espStaging string) string {
	body, err := os.ReadFile(filepath.Join(espStaging, "loader", "loader.conf"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(body), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "default "); ok {
			v = strings.TrimSpace(v)
			if strings.ContainsAny(v, "*?") {
				return ""
			}
			return strings.TrimSuffix(v, ".conf")
		}
	}
	return ""
}
