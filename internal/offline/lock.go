package offline

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const LockAPIVersion = "migration.smartx.com/v1alpha1"
const LockKind = "OfflineImageLock"

var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var platformPattern = regexp.MustCompile(`^linux/(amd64|arm64)$`)
var targetProjectPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,126}$`)

type ImageLock struct {
	APIVersion    string        `yaml:"apiVersion" json:"apiVersion"`
	Kind          string        `yaml:"kind" json:"kind"`
	BundleVersion string        `yaml:"bundleVersion" json:"bundleVersion"`
	Images        []LockedImage `yaml:"images" json:"images"`
}

type LockedImage struct {
	Name       string   `yaml:"name" json:"name"`
	Source     string   `yaml:"source" json:"source"`
	Target     string   `yaml:"target" json:"target"`
	Platforms  []string `yaml:"platforms" json:"platforms"`
	LayoutPath string   `yaml:"layoutPath" json:"layoutPath"`
	SBOMPath   string   `yaml:"sbomPath" json:"sbomPath"`
	Signature  string   `yaml:"signaturePath" json:"signaturePath"`
	ScanPath   string   `yaml:"scanPath" json:"scanPath"`
}

func LoadImageLock(reader io.Reader) (ImageLock, error) {
	decoder := yaml.NewDecoder(io.LimitReader(reader, 4<<20))
	decoder.KnownFields(true)
	var value ImageLock
	if err := decoder.Decode(&value); err != nil {
		return ImageLock{}, fmt.Errorf("decode image lock: %w", err)
	}
	if err := value.Validate(); err != nil {
		return ImageLock{}, err
	}
	return value, nil
}

func (l ImageLock) Validate() error {
	if l.APIVersion != LockAPIVersion || l.Kind != LockKind || strings.TrimSpace(l.BundleVersion) == "" {
		return errors.New("image lock apiVersion, kind and bundleVersion are required")
	}
	if len(l.Images) == 0 {
		return errors.New("image lock must contain at least one image")
	}
	names, targets := map[string]bool{}, map[string]bool{}
	for index, image := range l.Images {
		if strings.TrimSpace(image.Name) == "" || names[image.Name] {
			return fmt.Errorf("image %d has an empty or duplicate name", index)
		}
		names[image.Name] = true
		at := strings.LastIndex(image.Source, "@")
		if at <= 0 || !digestPattern.MatchString(image.Source[at+1:]) {
			return fmt.Errorf("image %s source must be pinned by sha256 digest", image.Name)
		}
		if err := validateTarget(image.Target); err != nil {
			return fmt.Errorf("image %s target: %w", image.Name, err)
		}
		if targets[image.Target] {
			return fmt.Errorf("image %s has duplicate target %s", image.Name, image.Target)
		}
		targets[image.Target] = true
		if len(image.Platforms) == 0 {
			return fmt.Errorf("image %s must declare at least one platform", image.Name)
		}
		platforms := append([]string(nil), image.Platforms...)
		sort.Strings(platforms)
		for platformIndex, platform := range platforms {
			if !platformPattern.MatchString(platform) || (platformIndex > 0 && platform == platforms[platformIndex-1]) {
				return fmt.Errorf("image %s has invalid or duplicate platform %q", image.Name, platform)
			}
		}
		for _, path := range []string{image.LayoutPath, image.SBOMPath, image.Signature, image.ScanPath} {
			if err := validateRelativePath(path); err != nil {
				return fmt.Errorf("image %s: %w", image.Name, err)
			}
		}
	}
	return nil
}

func validateTarget(value string) error {
	if strings.Contains(value, "://") || strings.Contains(value, "@") || strings.HasPrefix(value, "/") {
		return errors.New("must be a repository:tag without scheme or digest")
	}
	slash := strings.LastIndex(value, "/")
	colon := strings.LastIndex(value, ":")
	if colon <= slash+1 || colon == len(value)-1 {
		return errors.New("must include an explicit tag")
	}
	return nil
}

// RetargetImage keeps the locked image name and tag but places it under the
// Harbor project selected at installation time.
func RetargetImage(image LockedImage, project string) (LockedImage, error) {
	project = strings.TrimSpace(project)
	if !targetProjectPattern.MatchString(project) {
		return LockedImage{}, errors.New("Harbor project must contain only lower-case letters, digits, dot, underscore or dash")
	}
	if err := validateTarget(image.Target); err != nil {
		return LockedImage{}, err
	}
	colon := strings.LastIndex(image.Target, ":")
	repository, tag := image.Target[:colon], image.Target[colon+1:]
	if slash := strings.LastIndex(repository, "/"); slash >= 0 {
		repository = repository[slash+1:]
	}
	image.Target = project + "/" + repository + ":" + tag
	return image, nil
}

func validateRelativePath(value string) error {
	clean := filepath.Clean(value)
	if value == "" || filepath.IsAbs(value) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean != value {
		return fmt.Errorf("bundle path %q is not a clean relative path", value)
	}
	return nil
}
