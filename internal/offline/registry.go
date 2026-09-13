package offline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	mediaOCIManifest    = "application/vnd.oci.image.manifest.v1+json"
	mediaOCIIndex       = "application/vnd.oci.image.index.v1+json"
	mediaDockerManifest = "application/vnd.docker.distribution.manifest.v2+json"
	mediaDockerList     = "application/vnd.docker.distribution.manifest.list.v2+json"
)

type RegistryConfig struct {
	Endpoint string
	Username string
	Password string
	Insecure bool
}

type ImportResult struct {
	Image         string `json:"image"`
	Target        string `json:"target"`
	UploadedBlobs int    `json:"uploadedBlobs"`
	SkippedBlobs  int    `json:"skippedBlobs"`
	Manifests     int    `json:"manifests"`
}

type RegistryImporter struct {
	endpoint *url.URL
	username string
	password string
	client   *http.Client
	insecure bool
}

type ociDescriptor struct {
	MediaType string            `json:"mediaType"`
	Digest    string            `json:"digest"`
	Size      int64             `json:"size"`
	Platform  map[string]string `json:"platform,omitempty"`
}

type ociIndex struct {
	SchemaVersion int             `json:"schemaVersion"`
	Manifests     []ociDescriptor `json:"manifests"`
}

type ociManifest struct {
	SchemaVersion int             `json:"schemaVersion"`
	Config        ociDescriptor   `json:"config"`
	Layers        []ociDescriptor `json:"layers"`
}

func NewRegistryImporter(config RegistryConfig) (*RegistryImporter, error) {
	endpoint, err := url.Parse(strings.TrimRight(config.Endpoint, "/"))
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "https" && !(config.Insecure && endpoint.Scheme == "http")) {
		return nil, errors.New("registry endpoint must be HTTPS, or HTTP only when insecure mode is explicit")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if config.Insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- explicit user-controlled lab/offline registry option.
	}
	return &RegistryImporter{endpoint: endpoint, username: config.Username, password: config.Password, client: &http.Client{Transport: transport}, insecure: config.Insecure}, nil
}

func (i *RegistryImporter) Import(ctx context.Context, bundleRoot string, image LockedImage) (ImportResult, error) {
	if err := imageLockForOne(image).Validate(); err != nil {
		return ImportResult{}, err
	}
	layoutRoot, err := confinedPath(bundleRoot, image.LayoutPath)
	if err != nil {
		return ImportResult{}, err
	}
	indexBytes, err := os.ReadFile(filepath.Join(layoutRoot, "index.json"))
	if err != nil {
		return ImportResult{}, fmt.Errorf("read OCI index for %s: %w", image.Name, err)
	}
	var index ociIndex
	if err := json.Unmarshal(indexBytes, &index); err != nil || index.SchemaVersion != 2 || len(index.Manifests) != 1 {
		return ImportResult{}, fmt.Errorf("OCI layout for %s must have one schemaVersion 2 root descriptor", image.Name)
	}
	lockedDigest := image.Source[strings.LastIndex(image.Source, "@")+1:]
	if index.Manifests[0].Digest != lockedDigest {
		return ImportResult{}, fmt.Errorf("OCI layout root %s does not match locked source digest %s", index.Manifests[0].Digest, lockedDigest)
	}
	repository, tag, err := splitTarget(image.Target)
	if err != nil {
		return ImportResult{}, err
	}
	result := ImportResult{Image: image.Name, Target: image.Target}
	visited := map[string]bool{}
	payload, err := i.pushDescriptor(ctx, layoutRoot, repository, index.Manifests[0], visited, &result)
	if err != nil {
		return ImportResult{}, fmt.Errorf("import image %s: %w", image.Name, err)
	}
	if err := i.putManifest(ctx, repository, tag, index.Manifests[0].MediaType, payload); err != nil {
		return ImportResult{}, fmt.Errorf("tag imported image %s: %w", image.Name, err)
	}
	result.Manifests++
	return result, nil
}

func (i *RegistryImporter) pushDescriptor(ctx context.Context, layoutRoot, repository string, descriptor ociDescriptor, visited map[string]bool, result *ImportResult) ([]byte, error) {
	if visited[descriptor.Digest] {
		return i.readBlob(layoutRoot, descriptor)
	}
	payload, err := i.readBlob(layoutRoot, descriptor)
	if err != nil {
		return nil, err
	}
	switch descriptor.MediaType {
	case mediaOCIIndex, mediaDockerList:
		var index ociIndex
		if err := json.Unmarshal(payload, &index); err != nil || index.SchemaVersion != 2 {
			return nil, fmt.Errorf("decode image index %s", descriptor.Digest)
		}
		for _, child := range index.Manifests {
			if _, err := i.pushDescriptor(ctx, layoutRoot, repository, child, visited, result); err != nil {
				return nil, err
			}
		}
		if err := i.putManifest(ctx, repository, descriptor.Digest, descriptor.MediaType, payload); err != nil {
			return nil, err
		}
		result.Manifests++
	case mediaOCIManifest, mediaDockerManifest:
		var manifest ociManifest
		if err := json.Unmarshal(payload, &manifest); err != nil || manifest.SchemaVersion != 2 {
			return nil, fmt.Errorf("decode image manifest %s", descriptor.Digest)
		}
		blobs := append([]ociDescriptor{manifest.Config}, manifest.Layers...)
		for _, blob := range blobs {
			if err := i.pushBlob(ctx, layoutRoot, repository, blob, result); err != nil {
				return nil, err
			}
		}
		if err := i.putManifest(ctx, repository, descriptor.Digest, descriptor.MediaType, payload); err != nil {
			return nil, err
		}
		result.Manifests++
	default:
		return nil, fmt.Errorf("unsupported OCI descriptor media type %q", descriptor.MediaType)
	}
	visited[descriptor.Digest] = true
	return payload, nil
}

func (i *RegistryImporter) pushBlob(ctx context.Context, layoutRoot, repository string, descriptor ociDescriptor, result *ImportResult) error {
	payload, err := i.readBlob(layoutRoot, descriptor)
	if err != nil {
		return err
	}
	request, err := i.request(ctx, http.MethodHead, registryPath(repository, "blobs", descriptor.Digest), nil)
	if err != nil {
		return err
	}
	response, err := i.do(request)
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	if response.StatusCode == http.StatusOK {
		result.SkippedBlobs++
		return nil
	}
	if response.StatusCode != http.StatusNotFound {
		return fmt.Errorf("check blob %s: registry returned %s", descriptor.Digest, response.Status)
	}
	request, err = i.request(ctx, http.MethodPost, registryPath(repository, "blobs", "uploads")+"/", nil)
	if err != nil {
		return err
	}
	response, err = i.do(request)
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		return fmt.Errorf("start blob upload: registry returned %s", response.Status)
	}
	location, err := response.Location()
	if err != nil {
		return errors.New("registry blob upload did not return a valid Location")
	}
	if !sameOrigin(i.endpoint, location) {
		return errors.New("registry blob upload Location changed origin")
	}
	query := location.Query()
	query.Set("digest", descriptor.Digest)
	location.RawQuery = query.Encode()
	request, err = http.NewRequestWithContext(ctx, http.MethodPut, location.String(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	i.authorize(request)
	request.Header.Set("Content-Type", "application/octet-stream")
	response, err = i.do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("upload blob: registry returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	result.UploadedBlobs++
	return nil
}

func (i *RegistryImporter) putManifest(ctx context.Context, repository, reference, mediaType string, payload []byte) error {
	request, err := i.request(ctx, http.MethodPut, registryPath(repository, "manifests", reference), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", mediaType)
	response, err := i.do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusAccepted {
		return fmt.Errorf("push manifest %s: registry returned %s", reference, response.Status)
	}
	return nil
}

func (i *RegistryImporter) readBlob(layoutRoot string, descriptor ociDescriptor) ([]byte, error) {
	return readOCIBlob(layoutRoot, descriptor)
}

func readOCIBlob(layoutRoot string, descriptor ociDescriptor) ([]byte, error) {
	if !digestPattern.MatchString(descriptor.Digest) || descriptor.Size < 0 {
		return nil, errors.New("OCI descriptor has an invalid digest or size")
	}
	value, err := os.ReadFile(filepath.Join(layoutRoot, "blobs", "sha256", strings.TrimPrefix(descriptor.Digest, "sha256:")))
	if err != nil {
		return nil, fmt.Errorf("read OCI blob %s: %w", descriptor.Digest, err)
	}
	hash := sha256.Sum256(value)
	if hex.EncodeToString(hash[:]) != strings.TrimPrefix(descriptor.Digest, "sha256:") || int64(len(value)) != descriptor.Size {
		return nil, fmt.Errorf("OCI blob %s failed digest or size verification", descriptor.Digest)
	}
	return value, nil
}

func validateOCILayout(layoutRoot, lockedSource string) error {
	layoutBytes, err := os.ReadFile(filepath.Join(layoutRoot, "oci-layout"))
	if err != nil {
		return err
	}
	var layout struct {
		Version string `json:"imageLayoutVersion"`
	}
	if err := json.Unmarshal(layoutBytes, &layout); err != nil || layout.Version != "1.0.0" {
		return errors.New("oci-layout must declare imageLayoutVersion 1.0.0")
	}
	indexBytes, err := os.ReadFile(filepath.Join(layoutRoot, "index.json"))
	if err != nil {
		return err
	}
	var index ociIndex
	if err := json.Unmarshal(indexBytes, &index); err != nil || index.SchemaVersion != 2 || len(index.Manifests) != 1 {
		return errors.New("index.json must have one schemaVersion 2 root descriptor")
	}
	lockedDigest := lockedSource[strings.LastIndex(lockedSource, "@")+1:]
	if index.Manifests[0].Digest != lockedDigest {
		return fmt.Errorf("root %s does not match locked source digest %s", index.Manifests[0].Digest, lockedDigest)
	}
	return verifyDescriptorTree(layoutRoot, index.Manifests[0], map[string]bool{})
}

func verifyDescriptorTree(layoutRoot string, descriptor ociDescriptor, visited map[string]bool) error {
	if visited[descriptor.Digest] {
		return nil
	}
	payload, err := readOCIBlob(layoutRoot, descriptor)
	if err != nil {
		return err
	}
	visited[descriptor.Digest] = true
	switch descriptor.MediaType {
	case mediaOCIIndex, mediaDockerList:
		var index ociIndex
		if err := json.Unmarshal(payload, &index); err != nil || index.SchemaVersion != 2 {
			return fmt.Errorf("decode image index %s", descriptor.Digest)
		}
		for _, child := range index.Manifests {
			if err := verifyDescriptorTree(layoutRoot, child, visited); err != nil {
				return err
			}
		}
	case mediaOCIManifest, mediaDockerManifest:
		var manifest ociManifest
		if err := json.Unmarshal(payload, &manifest); err != nil || manifest.SchemaVersion != 2 {
			return fmt.Errorf("decode image manifest %s", descriptor.Digest)
		}
		for _, blob := range append([]ociDescriptor{manifest.Config}, manifest.Layers...) {
			if _, err := readOCIBlob(layoutRoot, blob); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported OCI descriptor media type %q", descriptor.MediaType)
	}
	return nil
}

func (i *RegistryImporter) request(ctx context.Context, method, suffix string, body io.Reader) (*http.Request, error) {
	endpoint := *i.endpoint
	trailingSlash := strings.HasSuffix(suffix, "/")
	endpoint.Path = path.Join(endpoint.Path, suffix)
	if trailingSlash && !strings.HasSuffix(endpoint.Path, "/") {
		endpoint.Path += "/"
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err == nil {
		i.authorize(request)
	}
	return request, err
}

func (i *RegistryImporter) authorize(request *http.Request) {
	if i.username != "" {
		request.SetBasicAuth(i.username, i.password)
	}
}

func (i *RegistryImporter) do(request *http.Request) (*http.Response, error) {
	response, err := i.client.Do(request)
	if err != nil || response.StatusCode != http.StatusUnauthorized {
		return response, err
	}
	challenge := response.Header.Get("WWW-Authenticate")
	_ = response.Body.Close()
	realm, parameters, err := parseBearerChallenge(challenge)
	if err != nil {
		return nil, err
	}
	tokenURL, err := url.Parse(realm)
	if err != nil || tokenURL.Host == "" {
		return nil, errors.New("registry returned an invalid bearer token realm")
	}
	if !sameOrigin(i.endpoint, tokenURL) || (tokenURL.Scheme != "https" && !i.insecure) {
		return nil, errors.New("registry bearer token realm changed origin or downgraded transport")
	}
	query := tokenURL.Query()
	for _, key := range []string{"service", "scope"} {
		if parameters[key] != "" {
			query.Set(key, parameters[key])
		}
	}
	tokenURL.RawQuery = query.Encode()
	tokenRequest, err := http.NewRequestWithContext(request.Context(), http.MethodGet, tokenURL.String(), nil)
	if err != nil {
		return nil, err
	}
	if i.username != "" {
		tokenRequest.SetBasicAuth(i.username, i.password)
	}
	tokenResponse, err := i.client.Do(tokenRequest)
	if err != nil {
		return nil, err
	}
	defer tokenResponse.Body.Close()
	if tokenResponse.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry token service returned %s", tokenResponse.Status)
	}
	var token struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(tokenResponse.Body, 1<<20)).Decode(&token); err != nil {
		return nil, errors.New("registry token response is invalid")
	}
	if token.Token == "" {
		token.Token = token.AccessToken
	}
	if token.Token == "" {
		return nil, errors.New("registry token response is empty")
	}
	retry := request.Clone(request.Context())
	if request.GetBody != nil {
		retry.Body, err = request.GetBody()
		if err != nil {
			return nil, err
		}
	} else if request.Body != nil {
		return nil, errors.New("registry request cannot be retried after authentication challenge")
	}
	retry.Header = request.Header.Clone()
	retry.Header.Set("Authorization", "Bearer "+token.Token)
	return i.client.Do(retry)
}

func parseBearerChallenge(value string) (string, map[string]string, error) {
	if !strings.HasPrefix(strings.ToLower(value), "bearer ") {
		return "", nil, errors.New("registry rejected credentials without a bearer challenge")
	}
	parameters := map[string]string{}
	for _, field := range strings.Split(value[len("Bearer "):], ",") {
		parts := strings.SplitN(strings.TrimSpace(field), "=", 2)
		if len(parts) != 2 {
			continue
		}
		parameters[strings.ToLower(parts[0])] = strings.Trim(parts[1], `"`)
	}
	if parameters["realm"] == "" {
		return "", nil, errors.New("registry bearer challenge has no realm")
	}
	return parameters["realm"], parameters, nil
}

func registryPath(repository string, segments ...string) string {
	parts := []string{"v2"}
	parts = append(parts, strings.Split(repository, "/")...)
	parts = append(parts, segments...)
	for index := range parts {
		parts[index] = url.PathEscape(parts[index])
	}
	return strings.Join(parts, "/")
}

func splitTarget(value string) (string, string, error) {
	if err := validateTarget(value); err != nil {
		return "", "", err
	}
	colon := strings.LastIndex(value, ":")
	return value[:colon], value[colon+1:], nil
}

func sameOrigin(left, right *url.URL) bool {
	return strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
}

func confinedPath(root, relative string) (string, error) {
	if err := validateRelativePath(relative); err != nil {
		return "", err
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	result := filepath.Join(root, filepath.FromSlash(relative))
	rel, err := filepath.Rel(root, result)
	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("bundle path escapes root")
	}
	return result, nil
}

func imageLockForOne(image LockedImage) ImageLock {
	return ImageLock{APIVersion: LockAPIVersion, Kind: LockKind, BundleVersion: "import", Images: []LockedImage{image}}
}
