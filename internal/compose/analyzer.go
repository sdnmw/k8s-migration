package compose

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"sort"
	"strings"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/compose-spec/compose-go/v2/loader"
	composetypes "github.com/compose-spec/compose-go/v2/types"
	"gopkg.in/yaml.v3"

	"github.com/smartx/sks-migration-center/internal/domain/application"
)

const (
	MaxComposeBytes = 2 * 1024 * 1024
	MaxEnvBytes     = 1024 * 1024
)

var ErrInvalidCompose = errors.New("invalid Compose configuration")

var composeVariablePattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)[^}]*\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

type Analyzer struct{}

func NewAnalyzer() *Analyzer { return &Analyzer{} }

func (a *Analyzer) Analyze(ctx context.Context, projectName string, composeYAML, environmentFile []byte) (application.ComposeInventory, error) {
	if len(composeYAML) == 0 || len(composeYAML) > MaxComposeBytes || len(environmentFile) > MaxEnvBytes {
		return application.ComposeInventory{}, fmt.Errorf("%w: compose file must be 1 byte to 2 MiB and env file at most 1 MiB", ErrInvalidCompose)
	}
	if err := rejectExternalReads(composeYAML); err != nil {
		return application.ComposeInventory{}, err
	}
	environment := map[string]string{}
	if len(environmentFile) > 0 {
		parsed, err := dotenv.Parse(bytes.NewReader(environmentFile))
		if err != nil {
			return application.ComposeInventory{}, fmt.Errorf("%w: .env file cannot be parsed", ErrInvalidCompose)
		}
		environment = parsed
	}
	unresolvedVariables := []string(nil)
	if len(environmentFile) == 0 {
		environment, unresolvedVariables = discoveryPlaceholderEnvironment(composeYAML)
	}
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		projectName = "migration"
	}
	details := composetypes.ConfigDetails{
		WorkingDir: ".", Environment: composetypes.Mapping(environment),
		ConfigFiles: []composetypes.ConfigFile{{Filename: "compose.yaml", Content: composeYAML}},
	}
	project, err := loader.LoadWithContext(ctx, details, func(options *loader.Options) {
		options.SetProjectName(projectName, true)
		options.ResolvePaths = false
		// Automatic host discovery intentionally uses `docker compose config
		// --no-interpolate`, otherwise environment values such as API tokens and
		// passwords would be copied into the platform. Typed placeholders let the
		// Compose loader normalize variable-based ports and mounts without using
		// the real values. The original references are restored in the inventory.
		options.SkipConsistencyCheck = len(unresolvedVariables) > 0
		options.SkipExtends = true
		options.SkipInclude = true
	}, loader.WithDiscardEnvFiles, loader.WithProfiles([]string{"*"}))
	if err != nil {
		return application.ComposeInventory{}, fmt.Errorf("%w: %v", ErrInvalidCompose, err)
	}
	result := inventory(project)
	if len(unresolvedVariables) > 0 {
		preserveVariableReferences(composeYAML, &result)
		result.Warnings = append(result.Warnings, application.ComposeWarning{
			Code:    "COMPOSE_UNRESOLVED_VARIABLES",
			Message: fmt.Sprintf("发现 %d 个未展开的 Compose 环境变量；请在迁移前上传受控 .env 或完成对应映射", len(unresolvedVariables)),
		})
	}
	return result, nil
}

func discoveryPlaceholderEnvironment(contents []byte) (map[string]string, []string) {
	values := map[string]string{}
	for _, match := range composeVariablePattern.FindAllSubmatch(contents, -1) {
		name := string(match[1])
		if name == "" {
			name = string(match[2])
		}
		if _, exists := values[name]; exists {
			continue
		}
		placeholder := "sks-migration-placeholder"
		upper := strings.ToUpper(name)
		if strings.Contains(upper, "PORT") {
			hash := fnv.New32a()
			_, _ = hash.Write([]byte(name))
			placeholder = fmt.Sprintf("%d", 20000+hash.Sum32()%20000)
		} else if strings.Contains(upper, "IMAGE") {
			placeholder = "placeholder.invalid/discovered/image:latest"
		}
		values[name] = placeholder
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return values, names
}

type rawComposeReferences struct {
	Services map[string]struct {
		Image   string `yaml:"image"`
		Ports   []any  `yaml:"ports"`
		Volumes []any  `yaml:"volumes"`
	} `yaml:"services"`
}

func preserveVariableReferences(contents []byte, result *application.ComposeInventory) {
	var raw rawComposeReferences
	if yaml.Unmarshal(contents, &raw) != nil {
		return
	}
	for index := range result.Services {
		service := &result.Services[index]
		references, exists := raw.Services[service.Name]
		if !exists {
			continue
		}
		if strings.Contains(references.Image, "$") {
			service.Image = references.Image
		}
		for mountIndex, value := range references.Volumes {
			if mountIndex >= len(service.Mounts) {
				break
			}
			mount, ok := value.(map[string]any)
			if !ok {
				continue
			}
			source, _ := mount["source"].(string)
			if strings.Contains(source, "$") {
				service.Mounts[mountIndex].Source = source
			}
		}
		for portIndex, port := range references.Ports {
			if portIndex >= len(service.Ports) {
				break
			}
			if value, ok := port.(string); ok && strings.HasPrefix(value, "${") {
				if end := strings.Index(value, "}:"); end > 1 {
					service.Ports[portIndex].Published = value[:end+1]
				}
			}
		}
	}
}

func rejectExternalReads(contents []byte) error {
	var raw map[string]any
	if err := yaml.Unmarshal(contents, &raw); err != nil {
		return fmt.Errorf("%w: compose YAML cannot be parsed", ErrInvalidCompose)
	}
	if _, ok := raw["include"]; ok {
		return fmt.Errorf("%w: include is not allowed; upload a self-contained Compose file", ErrInvalidCompose)
	}
	services, _ := raw["services"].(map[string]any)
	for name, value := range services {
		service, _ := value.(map[string]any)
		for _, key := range []string{"env_file", "label_file"} {
			if _, ok := service[key]; ok {
				return fmt.Errorf("%w: service %s uses %s; merge it into the uploaded files", ErrInvalidCompose, name, key)
			}
		}
		if extends, ok := service["extends"].(map[string]any); ok {
			if _, hasFile := extends["file"]; hasFile {
				return fmt.Errorf("%w: service %s uses an external extends file", ErrInvalidCompose, name)
			}
		}
	}
	for _, section := range []string{"configs", "secrets"} {
		items, _ := raw[section].(map[string]any)
		for name, value := range items {
			item, _ := value.(map[string]any)
			if _, ok := item["file"]; ok {
				return fmt.Errorf("%w: %s %s references a server-local file", ErrInvalidCompose, section, name)
			}
		}
	}
	return nil
}

func inventory(project *composetypes.Project) application.ComposeInventory {
	result := application.ComposeInventory{
		ProjectName: project.Name, Services: []application.ComposeService{}, Volumes: []application.ComposeResource{},
		Networks: []application.ComposeResource{}, Configs: project.ConfigNames(), Secrets: project.SecretNames(),
		DisabledServices: project.DisabledServiceNames(), Warnings: []application.ComposeWarning{},
	}
	services := make(composetypes.Services, len(project.Services)+len(project.DisabledServices))
	for name, service := range project.Services {
		services[name] = service
	}
	for name, service := range project.DisabledServices {
		services[name] = service
	}
	serviceNames := make([]string, 0, len(services))
	for name := range services {
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)
	for _, name := range serviceNames {
		service := services[name]
		summary := application.ComposeService{
			Name: name, Image: service.Image, Build: service.Build != nil, Profiles: sortedCopy(service.Profiles),
			DependsOn: sortedMapKeys(service.DependsOn), Networks: sortedMapKeys(service.Networks),
			EnvironmentKeys: sortedMapKeys(service.Environment), Privileged: service.Privileged, NetworkMode: service.NetworkMode,
			Ports: []application.ComposePort{}, Expose: sortedCopy([]string(service.Expose)), Mounts: []application.ComposeMount{},
		}
		for _, port := range service.Ports {
			summary.Ports = append(summary.Ports, application.ComposePort{Name: port.Name, Target: port.Target, Published: port.Published, Protocol: port.Protocol, HostIP: port.HostIP})
		}
		for _, mount := range service.Volumes {
			summary.Mounts = append(summary.Mounts, application.ComposeMount{Type: mount.Type, Source: mount.Source, Target: mount.Target, ReadOnly: mount.ReadOnly})
			if mount.Type == composetypes.VolumeTypeBind {
				result.Warnings = append(result.Warnings, application.ComposeWarning{Code: "COMPOSE_BIND_MOUNT", Service: name, Message: "bind mount 需要配置允许目录并执行文件迁移"})
			}
		}
		if service.Build != nil {
			result.Warnings = append(result.Warnings, application.ComposeWarning{Code: "COMPOSE_BUILD_UNSUPPORTED", Service: name, Message: "系统不构建业务镜像，请提供可拉取的镜像"})
		}
		if service.Privileged {
			result.Warnings = append(result.Warnings, application.ComposeWarning{Code: "COMPOSE_PRIVILEGED", Service: name, Message: "特权容器需要安全评估"})
		}
		result.Services = append(result.Services, summary)
	}
	for _, name := range project.VolumeNames() {
		item := project.Volumes[name]
		result.Volumes = append(result.Volumes, application.ComposeResource{Name: name, RuntimeName: item.Name, Driver: item.Driver, External: bool(item.External)})
	}
	for _, name := range project.NetworkNames() {
		item := project.Networks[name]
		result.Networks = append(result.Networks, application.ComposeResource{Name: name, Driver: item.Driver, External: bool(item.External)})
	}
	return result
}

func sortedMapKeys[T any](values map[string]T) []string {
	result := make([]string, 0, len(values))
	for name := range values {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func sortedCopy(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}
