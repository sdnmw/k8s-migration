package objectstorage

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const sourceLockAPIVersion = "migration.smartx.com/v1alpha1"
const sourceLockKind = "MinIOSourceLock"

var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
var imageDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

var ErrReleaseBlocked = errors.New("MinIO release is blocked by the security gate")

type SourcePolicy struct {
	APIVersion          string         `yaml:"apiVersion" json:"apiVersion"`
	Kind                string         `yaml:"kind" json:"kind"`
	Repository          string         `yaml:"repository" json:"repository"`
	Ref                 string         `yaml:"ref" json:"ref"`
	Commit              string         `yaml:"commit" json:"commit"`
	License             string         `yaml:"license" json:"license"`
	SourceOfferRequired bool           `yaml:"sourceOfferRequired" json:"sourceOfferRequired"`
	OfficialImage       OfficialImage  `yaml:"officialImage" json:"officialImage"`
	ReleaseAllowed      bool           `yaml:"releaseAllowed" json:"releaseAllowed"`
	SecurityGate        SecurityGate   `yaml:"securityGate" json:"securityGate"`
	RiskAcceptance      RiskAcceptance `yaml:"riskAcceptance" json:"riskAcceptance"`
}

type OfficialImage struct {
	Repository string `yaml:"repository" json:"repository"`
	Tag        string `yaml:"tag" json:"tag"`
	Digest     string `yaml:"digest" json:"digest"`
}

type RiskAcceptance struct {
	Accepted   bool      `yaml:"accepted" json:"accepted"`
	Scope      string    `yaml:"scope" json:"scope"`
	Rationale  string    `yaml:"rationale" json:"rationale"`
	AcceptedAt time.Time `yaml:"acceptedAt" json:"acceptedAt"`
}

type SecurityGate struct {
	Status    string            `yaml:"status" json:"status"`
	CheckedAt time.Time         `yaml:"checkedAt" json:"checkedAt"`
	Blockers  []SecurityBlocker `yaml:"blockers" json:"blockers"`
}

type SecurityBlocker struct {
	ID       string `yaml:"id" json:"id"`
	Severity string `yaml:"severity" json:"severity"`
	Reason   string `yaml:"reason" json:"reason"`
}

func LoadSourcePolicy(reader io.Reader) (SourcePolicy, error) {
	decoder := yaml.NewDecoder(io.LimitReader(reader, 1<<20))
	decoder.KnownFields(true)
	var value SourcePolicy
	if err := decoder.Decode(&value); err != nil {
		return SourcePolicy{}, fmt.Errorf("decode MinIO source lock: %w", err)
	}
	if err := value.Validate(); err != nil {
		return SourcePolicy{}, err
	}
	return value, nil
}

func (p SourcePolicy) Validate() error {
	if p.APIVersion != sourceLockAPIVersion || p.Kind != sourceLockKind {
		return errors.New("MinIO source lock apiVersion or kind is invalid")
	}
	if p.Repository != "https://github.com/minio/minio.git" || strings.TrimSpace(p.Ref) == "" || !commitPattern.MatchString(p.Commit) {
		return errors.New("MinIO source repository, ref, and full commit are required")
	}
	if p.License != "AGPL-3.0-only" || !p.SourceOfferRequired {
		return errors.New("MinIO AGPL source-offer compliance must be explicit")
	}
	if p.OfficialImage.Repository != "docker.io/minio/minio" || p.OfficialImage.Tag != p.Ref || !imageDigestPattern.MatchString(p.OfficialImage.Digest) {
		return errors.New("MinIO official image repository, release tag, and digest are required")
	}
	if p.SecurityGate.CheckedAt.IsZero() || (p.SecurityGate.Status != "PASSED" && p.SecurityGate.Status != "BLOCKED" && p.SecurityGate.Status != "ACCEPTED_RISK") {
		return errors.New("MinIO security gate status and check time are required")
	}
	for _, blocker := range p.SecurityGate.Blockers {
		if strings.TrimSpace(blocker.ID) == "" || (blocker.Severity != "HIGH" && blocker.Severity != "CRITICAL") || strings.TrimSpace(blocker.Reason) == "" {
			return errors.New("MinIO security blockers require id, HIGH/CRITICAL severity, and reason")
		}
	}
	switch p.SecurityGate.Status {
	case "PASSED":
		if !p.ReleaseAllowed || len(p.SecurityGate.Blockers) != 0 || p.RiskAcceptance.Accepted {
			return errors.New("passed MinIO gate cannot contain findings or risk acceptance")
		}
	case "BLOCKED":
		if p.ReleaseAllowed || len(p.SecurityGate.Blockers) == 0 || p.RiskAcceptance.Accepted {
			return errors.New("blocked MinIO gate requires unresolved findings")
		}
	case "ACCEPTED_RISK":
		if !p.ReleaseAllowed || len(p.SecurityGate.Blockers) == 0 || !p.RiskAcceptance.Accepted || p.RiskAcceptance.AcceptedAt.IsZero() || strings.TrimSpace(p.RiskAcceptance.Scope) == "" || strings.TrimSpace(p.RiskAcceptance.Rationale) == "" {
			return errors.New("accepted-risk MinIO gate requires findings, scope, rationale, time, and release allowance")
		}
	}
	return nil
}

func (p SourcePolicy) RequireAllowed() error {
	if err := p.Validate(); err != nil {
		return err
	}
	if !p.ReleaseAllowed {
		ids := make([]string, 0, len(p.SecurityGate.Blockers))
		for _, blocker := range p.SecurityGate.Blockers {
			ids = append(ids, blocker.ID)
		}
		return fmt.Errorf("%w: unresolved HIGH/CRITICAL findings: %s", ErrReleaseBlocked, strings.Join(ids, ", "))
	}
	return nil
}
