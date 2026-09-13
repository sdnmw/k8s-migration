package ssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	cryptossh "golang.org/x/crypto/ssh"

	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
)

const maxCommandOutput = 64 * 1024

type CommandID string

const (
	CommandDockerVersion  CommandID = "DOCKER_VERSION"
	CommandComposeVersion CommandID = "COMPOSE_VERSION"
	CommandDockerRoot     CommandID = "DOCKER_ROOT"
	CommandDockerDriver   CommandID = "DOCKER_DRIVER"
	CommandDockerArch     CommandID = "DOCKER_ARCH"
	CommandDockerOS       CommandID = "DOCKER_OS"
	CommandDockerCPUs     CommandID = "DOCKER_CPUS"
	CommandDockerMemory   CommandID = "DOCKER_MEMORY"
	CommandCgroupDriver   CommandID = "CGROUP_DRIVER"
	CommandComposeList    CommandID = "COMPOSE_LIST"
)

var commandText = map[CommandID]string{
	CommandDockerVersion:  "docker version --format '{{.Server.Version}}'",
	CommandComposeVersion: "docker compose version --short",
	CommandDockerRoot:     "docker info --format '{{.DockerRootDir}}'",
	CommandDockerDriver:   "docker info --format '{{.Driver}}'",
	CommandDockerArch:     "docker info --format '{{.Architecture}}'",
	CommandDockerOS:       "docker info --format '{{.OperatingSystem}}'",
	CommandDockerCPUs:     "docker info --format '{{.NCPU}}'",
	CommandDockerMemory:   "docker info --format '{{.MemTotal}}'",
	CommandCgroupDriver:   "docker info --format '{{.CgroupDriver}}'",
	CommandComposeList:    "docker compose ls --all --format json",
}

type Credential struct {
	Username           string `json:"username"`
	Password           string `json:"password,omitempty"`
	PrivateKey         string `json:"privateKey,omitempty"`
	PrivateKeyPassword string `json:"privateKeyPassword,omitempty"`
	HostKeyFingerprint string `json:"hostKeyFingerprint"`
}

type PreparedConfig struct {
	Endpoint string
	Address  string
	Config   *cryptossh.ClientConfig
}

type ProbeResult struct {
	Endpoint string
	Runtime  map[string]string
	Checks   []domainenvironment.ConnectionCheck
}

type Client struct {
	timeout time.Duration
}

func NewClient(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 12 * time.Second
	}
	return &Client{timeout: timeout}
}

// Fingerprint reads the host key before sending any authentication credentials.
func (c *Client) Fingerprint(ctx context.Context, endpoint string) (string, error) {
	_, address, err := parseEndpoint(endpoint)
	if err != nil {
		return "", err
	}
	conn, err := (&net.Dialer{Timeout: c.timeout}).DialContext(ctx, "tcp", address)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(c.timeout))
	fingerprint := ""
	_, _, _, _ = cryptossh.NewClientConn(conn, address, &cryptossh.ClientConfig{
		User: "host-key-probe", HostKeyAlgorithms: []string{cryptossh.KeyAlgoED25519, cryptossh.KeyAlgoECDSA256, cryptossh.KeyAlgoECDSA384, cryptossh.KeyAlgoECDSA521, cryptossh.KeyAlgoRSASHA512, cryptossh.KeyAlgoRSASHA256},
		HostKeyCallback: func(_ string, _ net.Addr, key cryptossh.PublicKey) error {
			fingerprint = cryptossh.FingerprintSHA256(key)
			return errors.New("host key collected")
		},
	})
	if fingerprint == "" {
		return "", errors.New("无法读取 SSH 主机密钥，请检查地址、端口和网络连接")
	}
	return fingerprint, nil
}

func (c *Client) Prepare(endpoint string, credential Credential) (PreparedConfig, error) {
	canonical, address, err := parseEndpoint(endpoint)
	if err != nil {
		return PreparedConfig{}, err
	}
	credential.Username = strings.TrimSpace(credential.Username)
	if credential.Username == "" || len(credential.Username) > 128 {
		return PreparedConfig{}, errors.New("SSH username is required")
	}
	if !strings.HasPrefix(credential.HostKeyFingerprint, "SHA256:") {
		return PreparedConfig{}, errors.New("SSH SHA256 host key fingerprint is required")
	}
	auth := make([]cryptossh.AuthMethod, 0, 2)
	if credential.Password != "" {
		if len(credential.Password) > 4096 {
			return PreparedConfig{}, errors.New("SSH password is too large")
		}
		auth = append(auth, cryptossh.Password(credential.Password))
	}
	if credential.PrivateKey != "" {
		var signer cryptossh.Signer
		if credential.PrivateKeyPassword == "" {
			signer, err = cryptossh.ParsePrivateKey([]byte(credential.PrivateKey))
		} else {
			signer, err = cryptossh.ParsePrivateKeyWithPassphrase([]byte(credential.PrivateKey), []byte(credential.PrivateKeyPassword))
		}
		if err != nil {
			return PreparedConfig{}, errors.New("SSH private key cannot be parsed")
		}
		auth = append(auth, cryptossh.PublicKeys(signer))
	} else if credential.PrivateKeyPassword != "" {
		return PreparedConfig{}, errors.New("SSH private-key password requires a private key")
	}
	if len(auth) == 0 {
		return PreparedConfig{}, errors.New("SSH password or private key is required")
	}
	expectedFingerprint := credential.HostKeyFingerprint
	config := &cryptossh.ClientConfig{
		User: credential.Username, Auth: auth, Timeout: c.timeout,
		// Prefer one stable modern key type. Some servers expose RSA, ECDSA and
		// Ed25519 simultaneously; the upstream default prefers ECDSA, while
		// OpenSSH commonly displays Ed25519 when accepting a host interactively.
		// Pinning the preference avoids validating a different key than the one
		// the administrator copied from ssh-keyscan/ssh-keygen.
		HostKeyAlgorithms: []string{
			cryptossh.KeyAlgoED25519,
			cryptossh.KeyAlgoECDSA256,
			cryptossh.KeyAlgoECDSA384,
			cryptossh.KeyAlgoECDSA521,
			cryptossh.KeyAlgoRSASHA512,
			cryptossh.KeyAlgoRSASHA256,
		},
		HostKeyCallback: func(_ string, _ net.Addr, key cryptossh.PublicKey) error {
			if cryptossh.FingerprintSHA256(key) != expectedFingerprint {
				return errors.New("SSH host key fingerprint mismatch")
			}
			return nil
		},
	}
	return PreparedConfig{Endpoint: canonical, Address: address, Config: config}, nil
}

func (c *Client) Probe(ctx context.Context, endpoint string, credential Credential) (ProbeResult, error) {
	prepared, err := c.Prepare(endpoint, credential)
	if err != nil {
		return ProbeResult{}, err
	}
	client, err := c.connect(ctx, prepared)
	if err != nil {
		return ProbeResult{Endpoint: prepared.Endpoint, Checks: []domainenvironment.ConnectionCheck{{Name: "SSH Host Key", Status: domainenvironment.CheckFailed, Message: "SSH 连接或主机指纹校验失败"}}}, err
	}
	defer client.Close()
	result := ProbeResult{Endpoint: prepared.Endpoint, Runtime: map[string]string{}, Checks: []domainenvironment.ConnectionCheck{{Name: "SSH Host Key", Status: domainenvironment.CheckPassed, Message: "主机指纹匹配"}}}
	dockerVersion, err := runCommand(ctx, client, commandText[CommandDockerVersion])
	if err != nil {
		result.Checks = append(result.Checks, domainenvironment.ConnectionCheck{Name: "Docker Engine", Status: domainenvironment.CheckFailed, Message: "Docker Engine 不可用或当前用户无权限"})
		return result, err
	}
	result.Runtime["dockerVersion"] = dockerVersion
	result.Checks = append(result.Checks, domainenvironment.ConnectionCheck{Name: "Docker Engine", Status: domainenvironment.CheckPassed, Message: dockerVersion})
	composeVersion, err := runCommand(ctx, client, commandText[CommandComposeVersion])
	if err != nil {
		result.Checks = append(result.Checks, domainenvironment.ConnectionCheck{Name: "Docker Compose", Status: domainenvironment.CheckFailed, Message: "Docker Compose v2 不可用"})
		return result, err
	}
	result.Runtime["composeVersion"] = composeVersion
	result.Checks = append(result.Checks, domainenvironment.ConnectionCheck{Name: "Docker Compose", Status: domainenvironment.CheckPassed, Message: composeVersion})
	return result, nil
}

// Discover returns migration-relevant Docker and Compose capabilities. Every
// command is fixed in the allowlist; no user-provided shell fragment is used.
func (c *Client) Discover(ctx context.Context, endpoint string, credential Credential) (ProbeResult, error) {
	result, err := c.Probe(ctx, endpoint, credential)
	if err != nil {
		return result, err
	}
	checks := []struct {
		id    CommandID
		key   string
		label string
	}{
		{CommandDockerRoot, "dockerRootDir", "Docker 数据目录"},
		{CommandDockerDriver, "storageDriver", "存储驱动"},
		{CommandDockerArch, "architecture", "主机架构"},
		{CommandDockerOS, "operatingSystem", "操作系统"},
		{CommandDockerCPUs, "cpus", "CPU"},
		{CommandDockerMemory, "memoryBytes", "内存"},
		{CommandCgroupDriver, "cgroupDriver", "Cgroup Driver"},
	}
	for _, check := range checks {
		value, runErr := c.Run(ctx, endpoint, credential, check.id)
		if runErr != nil {
			result.Checks = append(result.Checks, domainenvironment.ConnectionCheck{Name: check.label, Status: domainenvironment.CheckWarning, Message: "无法读取"})
			continue
		}
		result.Runtime[check.key] = value
		result.Checks = append(result.Checks, domainenvironment.ConnectionCheck{Name: check.label, Status: domainenvironment.CheckPassed, Message: value})
	}
	projects, listErr := c.DiscoverComposeProjects(ctx, endpoint, credential)
	if listErr != nil {
		result.Checks = append(result.Checks, domainenvironment.ConnectionCheck{Name: "Compose 项目", Status: domainenvironment.CheckWarning, Message: "无法枚举 Compose 项目"})
	} else {
		result.Runtime["projectCount"] = fmt.Sprintf("%d", len(projects))
		result.Checks = append(result.Checks, domainenvironment.ConnectionCheck{Name: "Compose 项目", Status: domainenvironment.CheckPassed, Message: fmt.Sprintf("发现 %d 个项目", len(projects))})
	}
	return result, nil
}

func (c *Client) Run(ctx context.Context, endpoint string, credential Credential, commandID CommandID) (string, error) {
	command, ok := commandText[commandID]
	if !ok {
		return "", errors.New("SSH command is not allowed")
	}
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

func (c *Client) connect(ctx context.Context, prepared PreparedConfig) (*cryptossh.Client, error) {
	connection, err := (&net.Dialer{Timeout: c.timeout}).DialContext(ctx, "tcp", prepared.Address)
	if err != nil {
		return nil, errors.New("SSH TCP connection failed")
	}
	sshConnection, channels, requests, err := cryptossh.NewClientConn(connection, prepared.Address, prepared.Config)
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("SSH handshake or host key verification failed: %w", err)
	}
	return cryptossh.NewClient(sshConnection, channels, requests), nil
}

func runCommand(ctx context.Context, client *cryptossh.Client, command string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", errors.New("SSH session could not be created")
	}
	defer session.Close()
	output := &limitedBuffer{remaining: maxCommandOutput}
	session.Stdout = output
	stderr := &limitedBuffer{remaining: maxCommandOutput}
	session.Stderr = stderr
	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()
	select {
	case <-ctx.Done():
		_ = session.Close()
		return "", ctx.Err()
	case err := <-done:
		if err != nil {
			detail := strings.TrimSpace(stderr.String())
			if len(detail) > 2048 {
				detail = detail[len(detail)-2048:]
			}
			if detail != "" {
				return "", fmt.Errorf("controlled SSH command failed: %s", detail)
			}
			return "", errors.New("controlled SSH command failed")
		}
		return strings.TrimSpace(output.String()), nil
	}
}

type limitedBuffer struct {
	lock      sync.Mutex
	buffer    bytes.Buffer
	remaining int
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	b.lock.Lock()
	defer b.lock.Unlock()
	originalLength := len(value)
	if len(value) > b.remaining {
		value = value[:b.remaining]
	}
	if len(value) > 0 {
		_, _ = b.buffer.Write(value)
		b.remaining -= len(value)
	}
	return originalLength, nil
}

func (b *limitedBuffer) String() string {
	b.lock.Lock()
	defer b.lock.Unlock()
	return b.buffer.String()
}

func parseEndpoint(value string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "ssh" || parsed.Host == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") {
		return "", "", errors.New("SSH endpoint must use ssh://host[:port] without username or path")
	}
	hostname := parsed.Hostname()
	port := parsed.Port()
	if hostname == "" {
		return "", "", errors.New("SSH endpoint host is required")
	}
	if port == "" {
		port = "22"
	}
	address := net.JoinHostPort(hostname, port)
	return fmt.Sprintf("ssh://%s", address), address, nil
}
