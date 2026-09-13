package ssh

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type ComposeAction string

const (
	ComposeStop  ComposeAction = "STOP"
	ComposeStart ComposeAction = "START"
	// ComposeRemove is used for explicit cleanup of an isolated migration test
	// project. The project name and compose definition remain subject to the
	// same validation as the normal lifecycle actions.
	ComposeRemove ComposeAction = "REMOVE"
)

type ComposeActionSpec struct {
	ProjectName     string
	ComposeYAML     []byte
	EnvironmentFile []byte
	Action          ComposeAction
}

type ComposeProject struct {
	Name        string   `json:"name"`
	Status      string   `json:"status,omitempty"`
	ConfigFiles []string `json:"configFiles"`
	WorkingDir  string   `json:"workingDir,omitempty"`
	ComposeYAML []byte   `json:"-"`
}

type composeProjectRow struct {
	Name        string `json:"Name"`
	Status      string `json:"Status"`
	ConfigFiles string `json:"ConfigFiles"`
}

type KopiaRepository struct {
	Image     string
	Endpoint  string
	Bucket    string
	Region    string
	Prefix    string
	AccessKey string
	SecretKey string
	CABundle  string
	TLSVerify bool
	Password  string
}

type KopiaSource struct {
	Name string
	Type string
	Path string
}

type KopiaSnapshotSpec struct {
	RunID      string
	Phase      string
	Repository KopiaRepository
	Source     KopiaSource
}

type KopiaSnapshot struct {
	ID        string
	SizeBytes int64
	Files     int64
}

// InspectBindFiles only tests filesystem type; it never reads file contents.
func (c *Client) InspectBindFiles(ctx context.Context, endpoint string, credential Credential, paths []string) (map[string]bool, error) {
	command := "set -eu; "
	for i, path := range paths {
		if _, err := kopiaSourceMount(KopiaSource{Type: "bind", Path: path}); err != nil {
			return nil, err
		}
		command += fmt.Sprintf("if test -f %s || sudo -n test -f %s; then printf '%d\\n'; elif test -d %s || sudo -n test -d %s; then :; else exit 1; fi; ", shellQuote(path), shellQuote(path), i, shellQuote(path), shellQuote(path))
	}
	output, err := c.runControlled(ctx, endpoint, credential, command)
	if err != nil {
		return nil, err
	}
	result := map[string]bool{}
	for _, line := range strings.Fields(output) {
		var i int
		if _, err := fmt.Sscan(line, &i); err != nil || i < 0 || i >= len(paths) {
			return nil, errors.New("invalid file-type result")
		}
		result[paths[i]] = true
	}
	return result, nil
}

var safeDockerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// DiscoverComposeProjects enumerates real Compose projects and reads their
// normalized configuration. Project names and paths come only from Compose's
// own JSON output and are strictly validated before constructing the command.
func (c *Client) DiscoverComposeProjects(ctx context.Context, endpoint string, credential Credential) ([]ComposeProject, error) {
	output, err := c.Run(ctx, endpoint, credential, CommandComposeList)
	if err != nil {
		return nil, err
	}
	rows := make([]composeProjectRow, 0)
	if strings.TrimSpace(output) != "" {
		if err := json.Unmarshal([]byte(output), &rows); err != nil {
			return nil, errors.New("Docker Compose project list is not valid JSON")
		}
	}
	projects := make([]ComposeProject, 0, len(rows))
	for _, row := range rows {
		if !safeDockerName.MatchString(row.Name) {
			continue
		}
		files := splitComposeConfigFiles(row.ConfigFiles)
		if len(files) == 0 {
			continue
		}
		args := make([]string, 0, len(files)*2)
		valid := true
		for _, file := range files {
			clean := filepath.Clean(file)
			if !filepath.IsAbs(clean) || strings.ContainsAny(clean, "\r\n\x00") {
				valid = false
				break
			}
			args = append(args, "-f", shellQuote(clean))
		}
		if !valid {
			continue
		}
		// Resolve .env and env_file on the source host, where the referenced
		// files actually exist. The normalized definition is stored encrypted.
		command := "docker compose --project-name " + shellQuote(row.Name) + " " + strings.Join(args, " ") + " config"
		definition, configErr := c.runControlled(ctx, endpoint, credential, command)
		// Some appliance installations allow Docker access to an operator but
		// keep generated env files root-owned. Use existing non-interactive sudo
		// authorization only for this fixed, read-only config operation.
		if configErr != nil && strings.Contains(configErr.Error(), "permission denied") {
			if elevated, elevatedErr := c.runControlled(ctx, endpoint, credential, "sudo -n -- "+command); elevatedErr == nil {
				definition, configErr = elevated, nil
			}
		}
		if configErr != nil {
			return projects, fmt.Errorf("读取 Compose 项目 %s 配置失败，请检查配置文件及引用文件是否存在：%w", row.Name, configErr)
		}
		projects = append(projects, ComposeProject{
			Name: row.Name, Status: row.Status, ConfigFiles: files,
			WorkingDir: filepath.Dir(files[0]), ComposeYAML: []byte(definition),
		})
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
	return projects, nil
}

func splitComposeConfigFiles(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if item := strings.TrimSpace(part); item != "" {
			result = append(result, item)
		}
	}
	return result
}

// RunComposeAction executes one fixed Compose lifecycle operation. The uploaded
// definition is sent over the SSH session and removed from the remote temp dir.
func (c *Client) RunComposeAction(ctx context.Context, endpoint string, credential Credential, spec ComposeActionSpec) error {
	command, err := composeActionCommand(spec)
	if err != nil {
		return err
	}
	_, err = c.runControlled(ctx, endpoint, credential, command)
	return err
}

// CreateKopiaSnapshot runs the pinned helper image on the Compose host. It only
// mounts the selected named volume or absolute bind path read-only.
func (c *Client) CreateKopiaSnapshot(ctx context.Context, endpoint string, credential Credential, spec KopiaSnapshotSpec) (KopiaSnapshot, error) {
	command, err := kopiaSnapshotCommand(spec)
	if err != nil {
		return KopiaSnapshot{}, err
	}
	output, err := c.runControlled(ctx, endpoint, credential, command)
	if err != nil {
		return KopiaSnapshot{}, err
	}
	return parseKopiaSnapshot(output)
}

func (c *Client) runControlled(ctx context.Context, endpoint string, credential Credential, command string) (string, error) {
	prepared, err := c.Prepare(endpoint, credential)
	if err != nil {
		return "", err
	}
	client, err := c.connect(ctx, prepared)
	if err != nil {
		return "", err
	}
	defer client.Close()
	return runCommand(ctx, client, command)
}

func composeActionCommand(spec ComposeActionSpec) (string, error) {
	if !safeDockerName.MatchString(spec.ProjectName) || len(spec.ComposeYAML) == 0 {
		return "", errors.New("Compose project name and definition are required")
	}
	var action string
	switch spec.Action {
	case ComposeStop:
		action = "stop"
	case ComposeStart:
		action = "up -d"
	case ComposeRemove:
		action = "down --volumes --remove-orphans"
	default:
		return "", errors.New("unsupported Compose lifecycle action")
	}
	compose := base64.StdEncoding.EncodeToString(spec.ComposeYAML)
	environment := base64.StdEncoding.EncodeToString(spec.EnvironmentFile)
	return "set -eu; d=$(mktemp -d); trap 'rm -rf -- \"$d\"' EXIT HUP INT TERM; " +
		"printf %s " + shellQuote(compose) + " | base64 -d >\"$d/compose.yaml\"; " +
		"printf %s " + shellQuote(environment) + " | base64 -d >\"$d/.env\"; " +
		"docker compose --project-name " + shellQuote(spec.ProjectName) + " --env-file \"$d/.env\" -f \"$d/compose.yaml\" " + action, nil
}

func kopiaSnapshotCommand(spec KopiaSnapshotSpec) (string, error) {
	repository, endpoint, err := validateKopiaRepository(spec.Repository)
	if err != nil {
		return "", err
	}
	if !safeDockerName.MatchString(spec.RunID) || !safeDockerName.MatchString(spec.Source.Name) {
		return "", errors.New("Kopia run and source names are invalid")
	}
	mount, err := kopiaSourceMount(spec.Source)
	if err != nil {
		return "", err
	}
	args := []string{"--bucket=" + repository.Bucket, "--endpoint=" + endpoint, "--region=" + repository.Region, "--prefix=" + repository.Prefix}
	if !repository.TLSVerify {
		args = append(args, "--disable-tls-verification")
	}
	quotedArgs := make([]string, len(args))
	for index := range args {
		quotedArgs[index] = shellQuote(args[index])
	}
	inner := "set -eu; if [ -n \"${ROOT_CA_PEM_BASE64:-}\" ]; then " +
		"printf %s \"$ROOT_CA_PEM_BASE64\" | base64 -d >/tmp/sks-migration-root-ca.pem; " +
		"export SSL_CERT_FILE=/tmp/sks-migration-root-ca.pem; fi; " +
		"if ! kopia repository connect s3 " + strings.Join(quotedArgs, " ") + " >/dev/null 2>&1; then " +
		"kopia repository create s3 " + strings.Join(quotedArgs, " ") + " >/dev/null 2>&1 || kopia repository connect s3 " + strings.Join(quotedArgs, " ") + " >/dev/null; fi; " +
		"kopia snapshot create /data --json --tags=" + shellQuote("migrationRun:"+spec.RunID) + " --tags=" + shellQuote("volume:"+spec.Source.Name)
	identity := fmt.Sprintf("%x", sha256.Sum256([]byte(spec.RunID+"\x00"+spec.Phase+"\x00"+spec.Source.Name+"\x00"+spec.Source.Path)))
	containerName := "smc-kopia-" + identity[:40]
	run := "docker run -d --name " + shellQuote(containerName) + " --label migration.smartx.com/operation=" + shellQuote(identity) + " --user 0:0 --entrypoint /bin/sh" +
		" -e KOPIA_PASSWORD=" + shellQuote(repository.Password) +
		" -e AWS_ACCESS_KEY_ID=" + shellQuote(repository.AccessKey) +
		" -e AWS_SECRET_ACCESS_KEY=" + shellQuote(repository.SecretKey) +
		" -e ROOT_CA_PEM_BASE64=" + shellQuote(base64.StdEncoding.EncodeToString([]byte(repository.CABundle))) +
		" -v " + shellQuote(mount) + " " + shellQuote(repository.Image) + " -ec " + shellQuote(inner)
	// A worker restart must reconnect to the existing helper rather than
	// launch a second snapshot or fail with a container-name conflict.
	return "set -eu; name=" + shellQuote(containerName) + "; " +
		"if docker container inspect \"$name\" >/dev/null 2>&1; then " +
		"test \"$(docker inspect --format '{{index .Config.Labels \"migration.smartx.com/operation\"}}' \"$name\")\" = " + shellQuote(identity) + "; " +
		"id=$name; else id=$(" + run + "); fi; " +
		"code=$(docker wait \"$id\"); docker logs \"$id\"; docker rm \"$id\" >/dev/null; test \"$code\" = 0", nil
}

func validateKopiaRepository(value KopiaRepository) (KopiaRepository, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value.Endpoint))
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return value, "", errors.New("Kopia repository endpoint is invalid")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return value, "", errors.New("Kopia repository endpoint must use HTTP or HTTPS")
	}
	if parsed.Scheme == "http" {
		value.TLSVerify = false
	}
	if !strings.Contains(value.Image, "@sha256:") || value.Bucket == "" || value.AccessKey == "" || value.SecretKey == "" || value.Password == "" {
		return value, "", errors.New("pinned Kopia image and repository credentials are required")
	}
	value.Prefix = strings.Trim(value.Prefix, "/") + "/"
	return value, parsed.Host, nil
}

func kopiaSourceMount(value KopiaSource) (string, error) {
	switch value.Type {
	case "volume":
		if !safeDockerName.MatchString(value.Path) {
			return "", errors.New("named volume runtime name is invalid")
		}
		return value.Path + ":/data:ro", nil
	case "bind":
		clean := filepath.Clean(value.Path)
		if !filepath.IsAbs(clean) || dangerousBindPath(clean) {
			return "", fmt.Errorf("bind source %q is outside the allowed migration scope", value.Path)
		}
		return clean + ":/data:ro", nil
	default:
		return "", errors.New("Kopia source type must be volume or bind")
	}
}

func dangerousBindPath(value string) bool {
	if value == "/" {
		return true
	}
	for _, root := range []string{"/etc", "/proc", "/sys", "/dev", "/run", "/var/run"} {
		if value == root || strings.HasPrefix(value, root+"/") {
			return true
		}
	}
	return false
}

func parseKopiaSnapshot(output string) (KopiaSnapshot, error) {
	// Kopia pretty-prints the snapshot JSON and can emit status text before or
	// after it. Try each object boundary and return the first object containing
	// a snapshot ID instead of depending on compact JSON formatting.
	for start := 0; start < len(output); start++ {
		if output[start] != '{' {
			continue
		}
		var value struct {
			ID        string `json:"id"`
			RootEntry struct {
				Summary struct {
					Size  int64 `json:"size"`
					Files int64 `json:"files"`
				} `json:"summ"`
			} `json:"rootEntry"`
		}
		if err := json.NewDecoder(strings.NewReader(output[start:])).Decode(&value); err == nil && value.ID != "" {
			return KopiaSnapshot{ID: value.ID, SizeBytes: value.RootEntry.Summary.Size, Files: value.RootEntry.Summary.Files}, nil
		}
	}
	return KopiaSnapshot{}, errors.New("Kopia snapshot output did not contain a valid snapshot JSON object")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
