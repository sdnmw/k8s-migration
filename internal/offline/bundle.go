package offline

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const ManifestName = "bundle-manifest.json"
const ChecksumsName = "SHA256SUMS"
const ImageLockName = "images.lock.yaml"

type BundleManifest struct {
	SchemaVersion int            `json:"schemaVersion"`
	BundleVersion string         `json:"bundleVersion"`
	Files         []ManifestFile `json:"files"`
}

type ManifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func BuildManifest(root string) (BundleManifest, ImageLock, error) {
	lockFile, err := os.Open(filepath.Join(root, ImageLockName))
	if err != nil {
		return BundleManifest{}, ImageLock{}, fmt.Errorf("open image lock: %w", err)
	}
	lock, err := LoadImageLock(lockFile)
	_ = lockFile.Close()
	if err != nil {
		return BundleManifest{}, ImageLock{}, err
	}
	if err := verifyLockedAssets(root, lock); err != nil {
		return BundleManifest{}, ImageLock{}, err
	}
	files := make([]ManifestFile, 0)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links are not allowed in offline bundles: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == ManifestName || relative == ChecksumsName || strings.HasSuffix(relative, "/.DS_Store") || relative == ".DS_Store" {
			return nil
		}
		checksum, size, err := checksumFile(path)
		if err != nil {
			return err
		}
		files = append(files, ManifestFile{Path: relative, SHA256: checksum, Size: size})
		return nil
	})
	if err != nil {
		return BundleManifest{}, ImageLock{}, fmt.Errorf("inventory offline bundle: %w", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return BundleManifest{SchemaVersion: 1, BundleVersion: lock.BundleVersion, Files: files}, lock, nil
}

func PackDirectory(root string, output io.Writer) error {
	manifest, _, err := BuildManifest(root)
	if err != nil {
		return err
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode bundle manifest: %w", err)
	}
	manifestJSON = append(manifestJSON, '\n')
	checksums := checksumsFor(manifest.Files, manifestJSON)

	gzipWriter := gzip.NewWriter(output)
	gzipWriter.Header.ModTime = time.Unix(0, 0).UTC()
	tarWriter := tar.NewWriter(gzipWriter)
	for _, file := range manifest.Files {
		if err := addFileToArchive(tarWriter, root, file.Path); err != nil {
			_ = tarWriter.Close()
			_ = gzipWriter.Close()
			return err
		}
	}
	if err := addBytesToArchive(tarWriter, ManifestName, manifestJSON); err != nil {
		return err
	}
	if err := addBytesToArchive(tarWriter, ChecksumsName, []byte(checksums)); err != nil {
		return err
	}
	if err := tarWriter.Close(); err != nil {
		return fmt.Errorf("close bundle tar: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return fmt.Errorf("close bundle gzip: %w", err)
	}
	return nil
}

func VerifyDirectory(root string) (BundleManifest, error) {
	manifestBytes, err := os.ReadFile(filepath.Join(root, ManifestName))
	if err != nil {
		return BundleManifest{}, fmt.Errorf("read bundle manifest: %w", err)
	}
	var expected BundleManifest
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&expected); err != nil || expected.SchemaVersion != 1 {
		return BundleManifest{}, errors.New("bundle manifest is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return BundleManifest{}, errors.New("bundle manifest has trailing content")
	}
	lockFile, err := os.Open(filepath.Join(root, ImageLockName))
	if err != nil {
		return BundleManifest{}, fmt.Errorf("open image lock: %w", err)
	}
	lock, err := LoadImageLock(lockFile)
	_ = lockFile.Close()
	if err != nil {
		return BundleManifest{}, err
	}
	if expected.BundleVersion != lock.BundleVersion {
		return BundleManifest{}, fmt.Errorf("bundle version mismatch: manifest=%s image-lock=%s", expected.BundleVersion, lock.BundleVersion)
	}
	if err := verifyLockedAssets(root, lock); err != nil {
		return BundleManifest{}, err
	}
	seen := make(map[string]struct{}, len(expected.Files))
	for _, file := range expected.Files {
		clean := filepath.Clean(filepath.FromSlash(file.Path))
		if file.Path == "" || clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || file.Path == ManifestName || file.Path == ChecksumsName {
			return BundleManifest{}, fmt.Errorf("bundle manifest contains invalid file path %q", file.Path)
		}
		if _, exists := seen[file.Path]; exists {
			return BundleManifest{}, fmt.Errorf("bundle manifest contains duplicate file path %q", file.Path)
		}
		seen[file.Path] = struct{}{}
		info, statErr := os.Lstat(filepath.Join(root, clean))
		if statErr != nil {
			return BundleManifest{}, fmt.Errorf("bundle file verification failed for %s: %w", file.Path, statErr)
		}
		if !info.Mode().IsRegular() {
			return BundleManifest{}, fmt.Errorf("bundle file verification failed for %s: expected a regular file", file.Path)
		}
		checksum, size, checksumErr := checksumFile(filepath.Join(root, clean))
		if checksumErr != nil {
			return BundleManifest{}, fmt.Errorf("bundle file verification failed for %s: %w", file.Path, checksumErr)
		}
		if checksum != file.SHA256 || size != file.Size {
			return BundleManifest{}, fmt.Errorf("bundle file verification failed for %s: checksum or size mismatch", file.Path)
		}
	}
	wantChecksums := checksumsFor(expected.Files, manifestBytes)
	checksums, err := os.ReadFile(filepath.Join(root, ChecksumsName))
	if err != nil || string(checksums) != wantChecksums {
		return BundleManifest{}, errors.New("SHA256SUMS does not match bundle manifest")
	}
	return expected, nil
}

func verifyLockedAssets(root string, lock ImageLock) error {
	for _, image := range lock.Images {
		for label, relative := range map[string]string{"OCI layout": image.LayoutPath, "SBOM": image.SBOMPath, "signature": image.Signature, "vulnerability scan": image.ScanPath} {
			path := filepath.Join(root, filepath.FromSlash(relative))
			info, err := os.Stat(path)
			if err != nil {
				return fmt.Errorf("image %s %s is unavailable: %w", image.Name, label, err)
			}
			if label == "OCI layout" {
				if !info.IsDir() {
					return fmt.Errorf("image %s OCI layout must be a directory", image.Name)
				}
				for _, required := range []string{"oci-layout", "index.json"} {
					if nested, err := os.Stat(filepath.Join(path, required)); err != nil || !nested.Mode().IsRegular() {
						return fmt.Errorf("image %s OCI layout is missing %s", image.Name, required)
					}
				}
				if err := validateOCILayout(path, image.Source); err != nil {
					return fmt.Errorf("image %s OCI layout: %w", image.Name, err)
				}
			} else if !info.Mode().IsRegular() {
				return fmt.Errorf("image %s %s must be a regular file", image.Name, label)
			}
		}
	}
	return nil
}

func checksumFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func checksumsFor(files []ManifestFile, manifest []byte) string {
	var builder strings.Builder
	for _, file := range files {
		fmt.Fprintf(&builder, "%s  %s\n", file.SHA256, file.Path)
	}
	hash := sha256.Sum256(manifest)
	fmt.Fprintf(&builder, "%s  %s\n", hex.EncodeToString(hash[:]), ManifestName)
	return builder.String()
}

func addFileToArchive(writer *tar.Writer, root, relative string) error {
	file, err := os.Open(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	mode := int64(0o644)
	if info.Mode().Perm()&0o111 != 0 {
		mode = 0o755
	}
	header := &tar.Header{Name: relative, Mode: mode, Size: info.Size(), ModTime: time.Unix(0, 0).UTC(), Typeflag: tar.TypeReg}
	if err := writer.WriteHeader(header); err != nil {
		return err
	}
	_, err = io.Copy(writer, file)
	return err
}

func addBytesToArchive(writer *tar.Writer, name string, value []byte) error {
	if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(value)), ModTime: time.Unix(0, 0).UTC(), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err := writer.Write(value)
	return err
}
