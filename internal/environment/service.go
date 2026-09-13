package environment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	kubernetesadapter "github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
	sshadapter "github.com/smartx/sks-migration-center/internal/adapter/ssh"
	domaincredential "github.com/smartx/sks-migration-center/internal/domain/credential"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	"github.com/smartx/sks-migration-center/internal/repository"
)

const MaxCredentialBytes = 1024 * 1024

var ErrInvalidInput = errors.New("invalid environment input")
var ErrConnection = errors.New("environment connection failed")

type Vault interface {
	Store(context.Context, string, domaincredential.Type, []byte) (domaincredential.Metadata, error)
	Resolve(context.Context, uuid.UUID) ([]byte, error)
	Delete(context.Context, uuid.UUID) error
}

type KubernetesClient interface {
	Prepare([]byte) (kubernetesadapter.PreparedConfig, error)
	Probe(context.Context, []byte) (kubernetesadapter.ProbeResult, error)
	Discover(context.Context, []byte) (kubernetesadapter.ProbeResult, error)
	ListNamespaces(context.Context, []byte) ([]string, error)
}

type SSHClient interface {
	Prepare(string, sshadapter.Credential) (sshadapter.PreparedConfig, error)
	Probe(context.Context, string, sshadapter.Credential) (sshadapter.ProbeResult, error)
	Discover(context.Context, string, sshadapter.Credential) (sshadapter.ProbeResult, error)
}

type CreateInput struct {
	Name       string
	Role       domainenvironment.Role
	Kind       domainenvironment.Kind
	Credential []byte
	Endpoint   string
	SSH        *sshadapter.Credential
}

type Service struct {
	repository repository.EnvironmentRepository
	vault      Vault
	kubernetes KubernetesClient
	ssh        SSHClient
	clock      func() time.Time
}

func NewService(store repository.EnvironmentRepository, vault Vault, kubernetes KubernetesClient, sshClients ...SSHClient) (*Service, error) {
	if store == nil || vault == nil || kubernetes == nil {
		return nil, errors.New("environment repository, credential vault and Kubernetes client are required")
	}
	var sshClient SSHClient
	if len(sshClients) > 0 {
		sshClient = sshClients[0]
	}
	return &Service{repository: store, vault: vault, kubernetes: kubernetes, ssh: sshClient, clock: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *Service) Create(ctx context.Context, input CreateInput) (domainenvironment.Environment, error) {
	input.Name = strings.TrimSpace(input.Name)
	if len(input.Name) > 128 {
		return domainenvironment.Environment{}, fmt.Errorf("%w: environment name must not exceed 128 characters", ErrInvalidInput)
	}
	var endpoint, credentialName string
	var credentialType domaincredential.Type
	var credentialPayload []byte
	switch input.Kind {
	case domainenvironment.KindKubernetes:
		if len(input.Credential) == 0 || len(input.Credential) > MaxCredentialBytes {
			return domainenvironment.Environment{}, fmt.Errorf("%w: kubeconfig must contain between 1 and %d bytes", ErrInvalidInput, MaxCredentialBytes)
		}
		prepared, err := s.kubernetes.Prepare(input.Credential)
		if err != nil {
			return domainenvironment.Environment{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		endpoint, credentialPayload = prepared.Endpoint, input.Credential
		credentialName, credentialType = input.Name+" kubeconfig", domaincredential.TypeKubeconfig
	case domainenvironment.KindDockerCompose:
		if s.ssh == nil || input.SSH == nil {
			return domainenvironment.Environment{}, fmt.Errorf("%w: SSH credential is required", ErrInvalidInput)
		}
		if strings.TrimSpace(input.SSH.HostKeyFingerprint) == "" {
			reader, ok := s.ssh.(interface {
				Fingerprint(context.Context, string) (string, error)
			})
			if !ok {
				return domainenvironment.Environment{}, fmt.Errorf("%w: automatic SSH host key discovery is unavailable", ErrInvalidInput)
			}
			fingerprint, err := reader.Fingerprint(ctx, input.Endpoint)
			if err != nil {
				return domainenvironment.Environment{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
			}
			input.SSH.HostKeyFingerprint = fingerprint
		}
		prepared, err := s.ssh.Prepare(input.Endpoint, *input.SSH)
		if err != nil {
			return domainenvironment.Environment{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		credentialPayload, err = json.Marshal(input.SSH)
		if err != nil || len(credentialPayload) > MaxCredentialBytes {
			return domainenvironment.Environment{}, fmt.Errorf("%w: SSH credential is too large", ErrInvalidInput)
		}
		endpoint = prepared.Endpoint
		credentialName, credentialType = input.Name+" SSH", domaincredential.TypeSSH
	default:
		return domainenvironment.Environment{}, fmt.Errorf("%w: unsupported environment kind", ErrInvalidInput)
	}
	now := s.clock()
	value := domainenvironment.Environment{
		ID: uuid.New(), Name: input.Name, Role: input.Role, Kind: input.Kind, Endpoint: endpoint,
		Status: domainenvironment.StatusPending, StatusMessage: "等待连接测试", CreatedAt: now, UpdatedAt: now,
	}
	if err := value.Validate(); err != nil {
		return domainenvironment.Environment{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	metadata, err := s.vault.Store(ctx, credentialName, credentialType, credentialPayload)
	if err != nil {
		return domainenvironment.Environment{}, fmt.Errorf("store environment credential: %w", err)
	}
	value.CredentialID = &metadata.ID
	if err := s.repository.Create(ctx, value); err != nil {
		_ = s.vault.Delete(ctx, metadata.ID)
		return domainenvironment.Environment{}, err
	}
	return value, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (domainenvironment.Environment, error) {
	return s.repository.Get(ctx, id)
}

func (s *Service) List(ctx context.Context) ([]domainenvironment.Environment, error) {
	return s.repository.List(ctx)
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	value, err := s.repository.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.repository.Delete(ctx, id); err != nil {
		return err
	}
	if value.CredentialID != nil {
		if err := s.vault.Delete(ctx, *value.CredentialID); err != nil && !errors.Is(err, repository.ErrNotFound) {
			return fmt.Errorf("delete environment credential: %w", err)
		}
	}
	return nil
}

func (s *Service) TestConnection(ctx context.Context, id uuid.UUID) (domainenvironment.ConnectionTest, error) {
	value, err := s.repository.Get(ctx, id)
	if err != nil {
		return domainenvironment.ConnectionTest{}, err
	}
	if value.CredentialID == nil {
		return domainenvironment.ConnectionTest{}, errors.New("environment does not have a credential")
	}
	credential, err := s.vault.Resolve(ctx, *value.CredentialID)
	if err != nil {
		return domainenvironment.ConnectionTest{}, fmt.Errorf("resolve environment credential: %w", err)
	}
	var checks []domainenvironment.ConnectionCheck
	var endpoint string
	var capabilities domainenvironment.Capabilities
	var probeErr error
	if value.Kind == domainenvironment.KindKubernetes {
		result, err := s.kubernetes.Probe(ctx, credential)
		probeErr = err
		checks, endpoint, capabilities = result.Checks, result.Endpoint, result.Capabilities
	} else {
		if s.ssh == nil {
			return domainenvironment.ConnectionTest{}, errors.New("SSH client is unavailable")
		}
		var sshCredential sshadapter.Credential
		if err := json.Unmarshal(credential, &sshCredential); err != nil {
			return domainenvironment.ConnectionTest{}, errors.New("stored SSH credential is invalid")
		}
		result, err := s.ssh.Probe(ctx, value.Endpoint, sshCredential)
		probeErr = err
		checks, endpoint = result.Checks, result.Endpoint
		capabilities.Runtime = result.Runtime
	}
	credential = nil
	now := s.clock()
	value.UpdatedAt = now
	value.Endpoint = endpoint
	if probeErr == nil {
		value.Status = domainenvironment.StatusConnected
		value.StatusMessage = "连接正常"
		value.Capabilities = capabilities
		value.CapabilitiesUpdatedAt = &now
	} else {
		value.Status = domainenvironment.StatusDisconnected
		value.StatusMessage = "连接测试失败"
		if capabilities.KubernetesVersion != "" || len(capabilities.Runtime) > 0 {
			value.Capabilities = capabilities
			value.CapabilitiesUpdatedAt = &now
		}
	}
	if err := s.repository.Update(ctx, value); err != nil {
		return domainenvironment.ConnectionTest{}, err
	}
	return domainenvironment.ConnectionTest{Success: probeErr == nil, Checks: checks}, nil
}

func (s *Service) Capabilities(ctx context.Context, id uuid.UUID) (domainenvironment.Capabilities, error) {
	value, err := s.repository.Get(ctx, id)
	if err != nil {
		return domainenvironment.Capabilities{}, err
	}
	return value.Capabilities, nil
}

func (s *Service) RefreshCapabilities(ctx context.Context, id uuid.UUID) (domainenvironment.Capabilities, error) {
	value, err := s.repository.Get(ctx, id)
	if err != nil {
		return domainenvironment.Capabilities{}, err
	}
	if value.CredentialID == nil {
		return domainenvironment.Capabilities{}, fmt.Errorf("%w: environment does not have a credential", ErrInvalidInput)
	}
	credential, err := s.vault.Resolve(ctx, *value.CredentialID)
	if err != nil {
		return domainenvironment.Capabilities{}, fmt.Errorf("resolve environment credential: %w", err)
	}
	var capabilities domainenvironment.Capabilities
	var endpoint string
	var checks []domainenvironment.ConnectionCheck
	if value.Kind == domainenvironment.KindKubernetes {
		result, discoverErr := s.kubernetes.Discover(ctx, credential)
		err, endpoint, checks, capabilities = discoverErr, result.Endpoint, result.Checks, result.Capabilities
	} else {
		if s.ssh == nil {
			return domainenvironment.Capabilities{}, errors.New("SSH client is unavailable")
		}
		var sshCredential sshadapter.Credential
		if jsonErr := json.Unmarshal(credential, &sshCredential); jsonErr != nil {
			return domainenvironment.Capabilities{}, errors.New("stored SSH credential is invalid")
		}
		result, discoverErr := s.ssh.Discover(ctx, value.Endpoint, sshCredential)
		err, endpoint, checks, capabilities.Runtime = discoverErr, result.Endpoint, result.Checks, result.Runtime
	}
	clearBytes(credential)
	if err != nil {
		value.Status = domainenvironment.StatusError
		value.StatusMessage = "能力发现失败"
		value.UpdatedAt = s.clock()
		_ = s.repository.Update(ctx, value)
		return domainenvironment.Capabilities{}, fmt.Errorf("%w: capability discovery failed", ErrConnection)
	}
	now := s.clock()
	value.Endpoint = endpoint
	value.Status = domainenvironment.StatusConnected
	value.StatusMessage = "能力发现完成"
	for _, check := range checks {
		if check.Status == domainenvironment.CheckWarning {
			value.StatusMessage = "能力发现完成，存在警告"
			break
		}
	}
	value.Capabilities = capabilities
	value.CapabilitiesUpdatedAt = &now
	value.UpdatedAt = now
	if err := s.repository.Update(ctx, value); err != nil {
		return domainenvironment.Capabilities{}, err
	}
	return value.Capabilities, nil
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func (s *Service) Namespaces(ctx context.Context, id uuid.UUID) ([]string, error) {
	_, credential, err := s.kubernetesCredential(ctx, id)
	if err != nil {
		return nil, err
	}
	names, err := s.kubernetes.ListNamespaces(ctx, credential)
	credential = nil
	if err != nil {
		return nil, fmt.Errorf("%w: namespace discovery failed", ErrConnection)
	}
	return names, nil
}

func (s *Service) kubernetesCredential(ctx context.Context, id uuid.UUID) (domainenvironment.Environment, []byte, error) {
	value, err := s.repository.Get(ctx, id)
	if err != nil {
		return domainenvironment.Environment{}, nil, err
	}
	if value.Kind != domainenvironment.KindKubernetes || value.CredentialID == nil {
		return domainenvironment.Environment{}, nil, fmt.Errorf("%w: environment does not have a Kubernetes credential", ErrInvalidInput)
	}
	credential, err := s.vault.Resolve(ctx, *value.CredentialID)
	if err != nil {
		return domainenvironment.Environment{}, nil, fmt.Errorf("resolve environment credential: %w", err)
	}
	return value, credential, nil
}
