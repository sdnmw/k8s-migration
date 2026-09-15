package harbor

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var safeProjectPart = regexp.MustCompile(`[^a-z0-9._-]+`)

// Client manages the public Harbor projects used as the image boundary for
// Compose migrations. Credentials are held in memory and never included in
// returned errors or migration events.
type Client struct {
	endpoint     *url.URL
	registryHost string
	prefix       string
	username     string
	password     string
	http         *http.Client
}

func NewClient(endpoint, registryHost, prefix, username, password string, insecure bool) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(endpoint), "/"))
	if err != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("Compose Harbor endpoint must be a base URL")
	}
	if parsed.Scheme != "https" && !(insecure && parsed.Scheme == "http") {
		return nil, errors.New("Compose Harbor endpoint must use HTTPS unless insecure mode is explicit")
	}
	registryHost = strings.TrimSpace(registryHost)
	if registryHost == "" || strings.Contains(registryHost, "/") || strings.ContainsAny(registryHost, "\r\n\x00") {
		return nil, errors.New("Compose Harbor registry host is invalid")
	}
	prefix = normalizeProjectPart(prefix)
	if prefix == "" || strings.TrimSpace(username) == "" || password == "" {
		return nil, errors.New("Compose Harbor project prefix and credential are required")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- explicit administrator setting for lab Harbor.
	}
	parsed.Path = ""
	return &Client{
		endpoint: parsed, registryHost: registryHost, prefix: prefix,
		username: username, password: password,
		http: &http.Client{Transport: transport, Timeout: 30 * time.Second},
	}, nil
}

// EnsurePublicProject creates (or updates) a stable project for one Compose
// application and returns the repository prefix used by docker tag/push.
func (c *Client) EnsurePublicProject(ctx context.Context, application string) (string, error) {
	fallback := c.registryHost + "/library"
	project := c.prefix + "-" + normalizeProjectPart(application)
	project = strings.Trim(project, "-._")
	if project == "" {
		return "", errors.New("Compose application cannot be converted to a Harbor project name")
	}
	if len(project) > 128 {
		project = strings.TrimRight(project[:128], "-._")
	}
	payload, _ := json.Marshal(map[string]any{"project_name": project, "public": true})
	status, body, err := c.do(ctx, http.MethodPost, "/api/v2.0/projects", payload)
	if err != nil {
		return fallback, fmt.Errorf("ensure public Harbor project %s: %w", project, err)
	}
	if status == http.StatusConflict {
		update, _ := json.Marshal(map[string]any{"metadata": map[string]string{"public": "true"}})
		status, body, err = c.do(ctx, http.MethodPut, "/api/v2.0/projects/"+url.PathEscape(project), update)
		if err != nil {
			return fallback, fmt.Errorf("make Harbor project %s public: %w", project, err)
		}
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return fallback, fmt.Errorf("Harbor project %s returned %s: %s", project, http.StatusText(status), strings.TrimSpace(string(body)))
	}
	return c.registryHost + "/" + project, nil
}

func (c *Client) do(ctx context.Context, method, path string, payload []byte) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.endpoint.String()+path, strings.NewReader(string(payload)))
	if err != nil {
		return 0, nil, err
	}
	request.SetBasicAuth(c.username, c.password)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	return response.StatusCode, body, err
}

func normalizeProjectPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = safeProjectPart.ReplaceAllString(value, "-")
	return strings.Trim(value, "-._")
}
