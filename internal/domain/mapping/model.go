package mapping

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/environment"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
)

type KeyValue struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type NodeLabelMapping struct {
	Source string `json:"source"`
	Target string `json:"target,omitempty"`
	Action string `json:"action"`
}

type NFSMapping struct {
	SourceServer       string `json:"sourceServer"`
	SourceExport       string `json:"sourceExport"`
	TargetServer       string `json:"targetServer"`
	TargetExport       string `json:"targetExport"`
	TargetStorageClass string `json:"targetStorageClass"`
}

type Profile struct {
	ID                  uuid.UUID          `json:"id"`
	Name                string             `json:"name"`
	TargetEnvironmentID uuid.UUID          `json:"targetEnvironmentId"`
	Storage             []KeyValue         `json:"storageMappings"`
	Namespaces          []KeyValue         `json:"namespaceMappings"`
	Ingress             []KeyValue         `json:"ingressMappings"`
	Registries          []KeyValue         `json:"registryMappings"`
	NFS                 []NFSMapping       `json:"nfsMappings"`
	NodeLabels          []NodeLabelMapping `json:"nodeLabelMappings"`
	CreatedAt           time.Time          `json:"createdAt"`
	UpdatedAt           time.Time          `json:"updatedAt"`
}

func (p *Profile) NormalizeAndValidate(target environment.Capabilities) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len(p.Name) > 128 {
		return errors.New("profile name is required and must not exceed 128 characters")
	}
	groups := []struct {
		name     string
		mappings []KeyValue
	}{{"storage", p.Storage}, {"namespace", p.Namespaces}, {"ingress", p.Ingress}, {"registry", p.Registries}}
	for _, group := range groups {
		name, mappings := group.name, group.mappings
		seen := map[string]struct{}{}
		for index := range mappings {
			mappings[index].Source, mappings[index].Target = strings.TrimSpace(mappings[index].Source), strings.TrimSpace(mappings[index].Target)
			if mappings[index].Source == "" || mappings[index].Target == "" {
				return fmt.Errorf("%s mapping source and target are required", name)
			}
			if _, exists := seen[mappings[index].Source]; exists {
				return fmt.Errorf("duplicate %s mapping source %q", name, mappings[index].Source)
			}
			seen[mappings[index].Source] = struct{}{}
		}
	}
	if err := validateNamespaceMappings(p.Namespaces); err != nil {
		return err
	}
	if err := validateRegistryMappings(p.Registries); err != nil {
		return err
	}
	if err := validateTargetNames("storage", p.Storage, storageClassSet(target)); err != nil {
		return err
	}
	if err := validateTargetNames("ingress", p.Ingress, stringSet(target.IngressClasses)); err != nil {
		return err
	}
	if err := validateNodeLabels(p.NodeLabels); err != nil {
		return err
	}
	if err := validateNFS(p.NFS, storageClassSet(target)); err != nil {
		return err
	}
	sortKeyValues(p.Storage)
	sortKeyValues(p.Namespaces)
	sortKeyValues(p.Ingress)
	sortKeyValues(p.Registries)
	sort.Slice(p.NodeLabels, func(i, j int) bool { return p.NodeLabels[i].Source < p.NodeLabels[j].Source })
	sort.Slice(p.NFS, func(i, j int) bool {
		return p.NFS[i].SourceServer+"\x00"+p.NFS[i].SourceExport < p.NFS[j].SourceServer+"\x00"+p.NFS[j].SourceExport
	})
	return nil
}

func validateNamespaceMappings(values []KeyValue) error {
	targets := map[string]string{}
	for _, value := range values {
		if len(k8svalidation.IsDNS1123Label(value.Source)) > 0 || len(k8svalidation.IsDNS1123Label(value.Target)) > 0 {
			return fmt.Errorf("namespace mapping %q -> %q must use DNS labels", value.Source, value.Target)
		}
		if source, exists := targets[value.Target]; exists && source != value.Source {
			return fmt.Errorf("namespace mappings %q and %q collide on target %q", source, value.Source, value.Target)
		}
		targets[value.Target] = value.Source
	}
	return nil
}

func validateRegistryMappings(values []KeyValue) error {
	for index, value := range values {
		for otherIndex := index + 1; otherIndex < len(values); otherIndex++ {
			other := values[otherIndex]
			if prefixOverlaps(value.Source, other.Source) {
				return fmt.Errorf("registry source prefixes %q and %q overlap", value.Source, other.Source)
			}
			if value.Target == other.Target {
				return fmt.Errorf("registry mappings collide on target prefix %q", value.Target)
			}
		}
	}
	return nil
}

func prefixOverlaps(first, second string) bool {
	first, second = strings.TrimSuffix(first, "/"), strings.TrimSuffix(second, "/")
	return first == second || strings.HasPrefix(first, second+"/") || strings.HasPrefix(second, first+"/")
}

func validateTargetNames(kind string, values []KeyValue, available map[string]struct{}) error {
	if len(available) == 0 {
		return nil
	}
	for _, value := range values {
		if _, found := available[value.Target]; !found {
			return fmt.Errorf("target %s %q was not discovered", kind, value.Target)
		}
	}
	return nil
}

func validateNodeLabels(values []NodeLabelMapping) error {
	seen := map[string]struct{}{}
	targets := map[string]string{}
	for index := range values {
		values[index].Source, values[index].Target, values[index].Action = strings.TrimSpace(values[index].Source), strings.TrimSpace(values[index].Target), strings.ToUpper(strings.TrimSpace(values[index].Action))
		if len(k8svalidation.IsQualifiedName(values[index].Source)) > 0 {
			return fmt.Errorf("invalid source node label %q", values[index].Source)
		}
		if _, exists := seen[values[index].Source]; exists {
			return fmt.Errorf("duplicate node label mapping source %q", values[index].Source)
		}
		seen[values[index].Source] = struct{}{}
		switch values[index].Action {
		case "MAP":
			if len(k8svalidation.IsQualifiedName(values[index].Target)) > 0 {
				return fmt.Errorf("invalid target node label %q", values[index].Target)
			}
			if source, exists := targets[values[index].Target]; exists && source != values[index].Source {
				return fmt.Errorf("node label mappings %q and %q collide on target %q", source, values[index].Source, values[index].Target)
			}
			targets[values[index].Target] = values[index].Source
		case "DROP":
			values[index].Target = ""
		default:
			return fmt.Errorf("node label action must be MAP or DROP")
		}
	}
	return nil
}

func validateNFS(values []NFSMapping, storageClasses map[string]struct{}) error {
	seen := map[string]struct{}{}
	targets := map[string]string{}
	for index := range values {
		item := &values[index]
		item.SourceServer, item.SourceExport = strings.TrimSpace(item.SourceServer), strings.TrimSpace(item.SourceExport)
		item.TargetServer, item.TargetExport, item.TargetStorageClass = strings.TrimSpace(item.TargetServer), strings.TrimSpace(item.TargetExport), strings.TrimSpace(item.TargetStorageClass)
		if item.SourceServer == "" || item.SourceExport == "" || item.TargetServer == "" || item.TargetExport == "" || item.TargetStorageClass == "" {
			return errors.New("NFS source, target and target StorageClass are required")
		}
		if !strings.HasPrefix(item.SourceExport, "/") || !strings.HasPrefix(item.TargetExport, "/") {
			return errors.New("NFS exports must be absolute paths")
		}
		key := item.SourceServer + "\x00" + item.SourceExport
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate NFS source %s:%s", item.SourceServer, item.SourceExport)
		}
		seen[key] = struct{}{}
		targetKey := item.TargetServer + "\x00" + item.TargetExport
		if source, exists := targets[targetKey]; exists && source != key {
			return fmt.Errorf("NFS sources collide on target %s:%s", item.TargetServer, item.TargetExport)
		}
		targets[targetKey] = key
		if len(storageClasses) > 0 {
			if _, found := storageClasses[item.TargetStorageClass]; !found {
				return fmt.Errorf("target storage %q was not discovered", item.TargetStorageClass)
			}
		}
	}
	return nil
}

func storageClassSet(value environment.Capabilities) map[string]struct{} {
	result := map[string]struct{}{}
	for _, class := range value.StorageClasses {
		result[class.Name] = struct{}{}
	}
	return result
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func sortKeyValues(values []KeyValue) {
	sort.Slice(values, func(i, j int) bool { return values[i].Source < values[j].Source })
}
