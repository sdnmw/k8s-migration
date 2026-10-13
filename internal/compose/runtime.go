package compose

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/compose-spec/compose-go/v2/loader"
	types "github.com/compose-spec/compose-go/v2/types"
	"gopkg.in/yaml.v3"
)

const TraefikRuntimeDirectory = "/run/sks-migration-traefik"

// RuntimeDependencyError is deterministic: retrying cannot repair a Docker API dependency.
type RuntimeDependencyError struct{ Service, Reason string }

func (e *RuntimeDependencyError) Error() string {
	return fmt.Sprintf("Compose 服务 %s 的运行时依赖无法自动转换：%s；源项目保持不变，请配置目标端适配后重新评估", e.Service, e.Reason)
}
func (*RuntimeDependencyError) Permanent() bool { return true }

// Runtime sockets are control interfaces, never data that Kopia should snapshot.
func IsRuntimeSocket(source, target string) bool {
	for _, value := range []string{source, target} {
		value = path.Clean(value)
		if (strings.HasPrefix(value, "/run/") || strings.HasPrefix(value, "/var/run/")) && strings.HasSuffix(value, ".sock") {
			return true
		}
	}
	return false
}

type TraefikRuntime struct {
	Service       string
	ConfigName    string
	Configuration []byte
	BackendPorts  map[string]int
}

type TargetRuntime struct {
	ComposeYAML []byte
	Proxies     []TraefikRuntime
}

// PrepareTargetRuntime never changes the source definition. It replaces the Docker
// provider with an offline file provider backed by Kubernetes service DNS, preserving
// explicit HTTP routers, TLS resolvers, middleware and persistent certificate storage.
func PrepareTargetRuntime(ctx context.Context, projectName string, contents, envFile []byte) (TargetRuntime, error) {
	result := TargetRuntime{ComposeYAML: contents}
	if !bytes.Contains(contents, []byte(".sock")) {
		return result, nil
	}
	env := map[string]string{}
	if len(envFile) > 0 {
		var err error
		env, err = dotenv.Parse(bytes.NewReader(envFile))
		if err != nil {
			return result, err
		}
	}
	project, err := loader.LoadWithContext(ctx, types.ConfigDetails{WorkingDir: ".", Environment: types.Mapping(env), ConfigFiles: []types.ConfigFile{{Filename: "compose.yaml", Content: contents}}}, func(o *loader.Options) {
		o.SetProjectName(projectName, true)
		o.ResolvePaths = false
		o.SkipExtends, o.SkipInclude = true, true
	}, loader.WithDiscardEnvFiles, loader.WithProfiles([]string{"*"}))
	if err != nil {
		return result, err
	}
	return prepareProjectRuntime(project, contents)
}

func prepareProjectRuntime(project *types.Project, original []byte) (TargetRuntime, error) {
	result := TargetRuntime{ComposeYAML: original}
	for _, name := range project.ServiceNames() {
		service := project.Services[name]
		hasSocket := false
		for _, mount := range service.Volumes {
			if mount.Type == types.VolumeTypeBind && IsRuntimeSocket(mount.Source, mount.Target) {
				hasSocket = true
			}
		}
		if !hasSocket {
			continue
		}
		fail := func(reason string) (TargetRuntime, error) {
			return result, &RuntimeDependencyError{Service: name, Reason: reason}
		}
		image := strings.Split(service.Image, "@")[0]
		if colon := strings.LastIndex(image, ":"); colon > strings.LastIndex(image, "/") {
			image = image[:colon]
		}
		if path.Base(image) != "traefik" {
			return fail("依赖 Docker/容器运行时 socket，不能作为 PVC 或数据卷迁移")
		}
		if len(result.Proxies) != 0 {
			return fail("多个 Docker provider 的路由作用域需要显式配置")
		}
		args, enabled, exposed := []string{}, false, true
		for _, arg := range service.Command {
			lower := strings.ToLower(arg)
			if strings.HasPrefix(lower, "--configfile") || strings.HasPrefix(lower, "--providers.file") || strings.HasPrefix(lower, "--providers.swarm") {
				return fail("现有静态配置、File 或 Swarm provider 需要手动合并")
			}
			if lower == "--providers.docker" || lower == "--providers.docker=true" {
				enabled = true
			}
			if lower == "--providers.docker.exposedbydefault=false" {
				exposed = false
			}
			if strings.HasPrefix(lower, "--providers.docker") {
				continue
			}
			args = append(args, arg)
		}
		for key, value := range service.Environment {
			upper := strings.ToUpper(key)
			if strings.HasPrefix(upper, "TRAEFIK_PROVIDERS_DOCKER") {
				if value != nil && upper == "TRAEFIK_PROVIDERS_DOCKER" && strings.EqualFold(*value, "true") {
					enabled = true
				}
				if value != nil && upper == "TRAEFIK_PROVIDERS_DOCKER_EXPOSEDBYDEFAULT" && strings.EqualFold(*value, "false") {
					exposed = false
				}
				delete(service.Environment, key)
			}
			if upper == "TRAEFIK_CONFIGFILE" || strings.HasPrefix(upper, "TRAEFIK_PROVIDERS_FILE") {
				return fail("环境变量中的静态/File provider 配置需要手动合并")
			}
		}
		if !enabled || exposed {
			return fail("只支持显式开启 Docker provider 且 exposedByDefault=false 的 Traefik HTTP 路由")
		}
		configuration, ports, err := traefikHTTPConfiguration(project, name)
		if err != nil {
			return fail(err.Error())
		}
		mounts := make([]types.ServiceVolumeConfig, 0, len(service.Volumes))
		for _, mount := range service.Volumes {
			if mount.Type == types.VolumeTypeBind && IsRuntimeSocket(mount.Source, mount.Target) {
				if path.Base(mount.Target) != "docker.sock" {
					return fail("Traefik 挂载了非 Docker socket")
				}
				continue
			}
			if strings.HasPrefix(path.Clean(mount.Target), TraefikRuntimeDirectory) {
				return fail("目标动态配置目录与源挂载冲突")
			}
			mounts = append(mounts, mount)
		}
		service.Volumes = mounts
		service.Command = append(args, "--providers.docker=false", "--providers.file.filename="+TraefikRuntimeDirectory+"/dynamic.yaml", "--providers.file.watch=true")
		project.Services[name] = service
		result.Proxies = append(result.Proxies, TraefikRuntime{Service: name, ConfigName: strings.ReplaceAll(name, "_", "-") + "-migration-routing", Configuration: configuration, BackendPorts: ports})
	}
	if len(result.Proxies) > 0 {
		encoded, err := project.MarshalYAML()
		if err != nil {
			return result, err
		}
		result.ComposeYAML = encoded
	}
	return result, nil
}

func traefikHTTPConfiguration(project *types.Project, proxy string) ([]byte, map[string]int, error) {
	routers, services, middlewares := map[string]any{}, map[string]any{}, map[string]any{}
	owners, ports := map[string]string{}, map[string]int{}
	for _, name := range project.ServiceNames() {
		service := project.Services[name]
		if !strings.EqualFold(service.Labels["traefik.enable"], "true") {
			continue
		}
		labels := make([]string, 0, len(service.Labels))
		for key := range service.Labels {
			labels = append(labels, key)
		}
		sort.Strings(labels)
		localServices, localRouters := map[string]bool{}, map[string]bool{}
		for _, key := range labels {
			lower := strings.ToLower(key)
			if strings.HasPrefix(lower, "traefik.tcp.") || strings.HasPrefix(lower, "traefik.udp.") {
				return nil, nil, fmt.Errorf("服务 %s 的 TCP/UDP 路由尚不支持自动转换", name)
			}
			if !strings.HasPrefix(lower, "traefik.http.") {
				continue
			}
			parts := strings.Split(lower, ".")
			if len(parts) < 5 {
				return nil, nil, fmt.Errorf("无效路由标签 %s", key)
			}
			group, item, property, value := parts[2], parts[3], strings.Join(parts[4:], "."), service.Labels[key]
			switch group {
			case "routers":
				if old := owners[item]; old != "" && old != name {
					return nil, nil, fmt.Errorf("重复路由 %s", item)
				}
				owners[item] = name
				localRouters[item] = true
				router := ensureRuntimeMap(routers, item)
				switch property {
				case "rule", "rulesyntax":
					router[map[string]string{"rule": "rule", "rulesyntax": "ruleSyntax"}[property]] = value
				case "service":
					router["service"] = strings.TrimSuffix(value, "@docker")
				case "entrypoints":
					router["entryPoints"] = runtimeList(value, false)
				case "middlewares":
					router["middlewares"] = runtimeList(value, true)
				case "priority":
					n, e := strconv.Atoi(value)
					if e != nil {
						return nil, nil, fmt.Errorf("无效路由优先级 %s", item)
					}
					router["priority"] = n
				case "tls":
					if value == "true" {
						ensureRuntimeMap(router, "tls")
					} else if value != "false" {
						return nil, nil, fmt.Errorf("无效 TLS 标签 %s", item)
					}
				case "tls.certresolver":
					ensureRuntimeMap(router, "tls")["certResolver"] = value
				default:
					return nil, nil, fmt.Errorf("暂不支持路由标签 %s", key)
				}
			case "services":
				if property != "loadbalancer.server.port" && property != "loadbalancer.server.scheme" {
					return nil, nil, fmt.Errorf("暂不支持服务标签 %s", key)
				}
				localServices[item] = true
			case "middlewares":
				field, kind, ok := runtimeHeaderField(property)
				if !ok {
					return nil, nil, fmt.Errorf("暂不支持中间件标签 %s", key)
				}
				headers := ensureRuntimeMap(ensureRuntimeMap(middlewares, item), "headers")
				var converted any = value
				if kind == "bool" {
					n, e := strconv.ParseBool(value)
					if e != nil {
						return nil, nil, fmt.Errorf("无效布尔标签 %s", key)
					}
					converted = n
				}
				if kind == "int" {
					n, e := strconv.Atoi(value)
					if e != nil || n < 0 {
						return nil, nil, fmt.Errorf("无效整数标签 %s", key)
					}
					converted = n
				}
				if old, found := headers[field]; found && fmt.Sprint(old) != fmt.Sprint(converted) {
					return nil, nil, fmt.Errorf("重复中间件 %s", item)
				}
				headers[field] = converted
			default:
				return nil, nil, fmt.Errorf("暂不支持 Traefik 标签 %s", key)
			}
		}
		if len(localRouters) == 0 {
			return nil, nil, fmt.Errorf("服务 %s 需要显式 HTTP router.rule，不能猜测 Docker 默认路由", name)
		}
		if len(localServices) == 0 {
			localServices[name] = true
		}
		for item := range localServices {
			port := 0
			scheme := "http"
			for key, value := range service.Labels {
				switch strings.ToLower(key) {
				case "traefik.http.services." + item + ".loadbalancer.server.port":
					n, e := strconv.Atoi(value)
					if e != nil {
						return nil, nil, fmt.Errorf("无效后端端口 %s", item)
					}
					port = n
				case "traefik.http.services." + item + ".loadbalancer.server.scheme":
					scheme = value
				}
			}
			if port == 0 {
				candidates := map[int]bool{}
				for _, p := range service.Ports {
					if p.Protocol == "tcp" || p.Protocol == "" {
						candidates[int(p.Target)] = true
					}
				}
				for _, p := range service.Expose {
					n, e := strconv.Atoi(strings.TrimSuffix(p, "/tcp"))
					if e == nil {
						candidates[n] = true
					}
				}
				if len(candidates) != 1 {
					return nil, nil, fmt.Errorf("服务 %s 的后端端口不明确，请声明 loadbalancer.server.port", name)
				}
				for n := range candidates {
					port = n
				}
			}
			if port < 1 || port > 65535 || (scheme != "http" && scheme != "https" && scheme != "h2c") {
				return nil, nil, fmt.Errorf("无效后端协议或端口 %s", item)
			}
			if _, exists := services[item]; exists {
				return nil, nil, fmt.Errorf("重复后端服务 %s", item)
			}
			services[item] = map[string]any{"loadBalancer": map[string]any{"servers": []any{map[string]any{"url": fmt.Sprintf("%s://%s:%d", scheme, strings.ReplaceAll(name, "_", "-"), port)}}}}
			if old := ports[name]; old != 0 && old != port {
				return nil, nil, fmt.Errorf("服务 %s 的多个后端端口需显式适配", name)
			}
			ports[name] = port
		}
		for item := range localRouters {
			router := routers[item].(map[string]any)
			if router["rule"] == nil || router["rule"] == "" {
				return nil, nil, fmt.Errorf("路由 %s 缺少 rule", item)
			}
			if router["service"] == nil {
				if len(localServices) != 1 {
					return nil, nil, fmt.Errorf("路由 %s 需要显式 service", item)
				}
				for s := range localServices {
					router["service"] = s
				}
			}
		}
	}
	if len(routers) == 0 {
		return nil, nil, fmt.Errorf("Traefik %s 没有可转换的显式 HTTP 路由", proxy)
	}
	for name, raw := range routers {
		router := raw.(map[string]any)
		if services[fmt.Sprint(router["service"])] == nil {
			return nil, nil, fmt.Errorf("路由 %s 引用了未转换的服务", name)
		}
		if refs, ok := router["middlewares"].([]string); ok {
			for _, ref := range refs {
				if !strings.Contains(ref, "@") && middlewares[ref] == nil {
					return nil, nil, fmt.Errorf("路由 %s 引用了未转换的中间件 %s", name, ref)
				}
			}
		}
	}
	config, err := yaml.Marshal(map[string]any{"http": map[string]any{"routers": routers, "services": services, "middlewares": middlewares}})
	return config, ports, err
}

func ensureRuntimeMap(parent map[string]any, key string) map[string]any {
	if found, ok := parent[key].(map[string]any); ok {
		return found
	}
	value := map[string]any{}
	parent[key] = value
	return value
}
func runtimeList(value string, rewrite bool) []string {
	result := []string{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if rewrite {
			item = strings.TrimSuffix(item, "@docker")
		}
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}
func runtimeHeaderField(property string) (string, string, bool) {
	fields := map[string]string{"sslhost": "sslHost:string", "sslredirect": "sslRedirect:bool", "ssltemporaryredirect": "sslTemporaryRedirect:bool", "sslforcehost": "sslForceHost:bool", "stsseconds": "stsSeconds:int", "stsincludesubdomains": "stsIncludeSubdomains:bool", "stspreload": "stsPreload:bool", "forcestsheader": "forceSTSHeader:bool", "browserxssfilter": "browserXssFilter:bool", "contenttypenosniff": "contentTypeNosniff:bool", "framedeny": "frameDeny:bool", "contentsecuritypolicy": "contentSecurityPolicy:string", "referrerpolicy": "referrerPolicy:string", "permissionspolicy": "permissionsPolicy:string"}
	if !strings.HasPrefix(property, "headers.") {
		return "", "", false
	}
	field, ok := fields[strings.TrimPrefix(property, "headers.")]
	parts := strings.Split(field, ":")
	if !ok {
		return "", "", false
	}
	return parts[0], parts[1], true
}
