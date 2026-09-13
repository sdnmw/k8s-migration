package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	sshadapter "github.com/smartx/sks-migration-center/internal/adapter/ssh"
	"github.com/smartx/sks-migration-center/internal/compose"
	"github.com/smartx/sks-migration-center/internal/security"
	cryptossh "golang.org/x/crypto/ssh"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestLiveHarborDiscovery(t *testing.T) {
	if os.Getenv("LIVE_HARBOR_DISCOVERY") != "1" {
		t.Skip("explicit live diagnostic only")
	}
	root := os.Getenv("MIGRATION_WORKSPACE")
	cmd := exec.Command("kubectl", "--kubeconfig", root+"/.data/kubeconfigs/mw.yaml", "-n", "sks-migration-center", "exec", "sks-migration-center-postgres-0", "--", "sh", "-c", `psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At -c "select json_build_object('id',c.id,'payload',encode(c.encrypted_payload,'base64'),'endpoint',e.endpoint) from credentials c join environments e on e.credential_id=c.id where e.name='harbor'"`)
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal("read stored credential metadata failed")
	}
	var row struct{ ID, Payload, Endpoint string }
	if json.Unmarshal(raw, &row) != nil {
		t.Fatal("invalid diagnostic record")
	}
	encrypted, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(row.Payload, "\n", ""))
	if err != nil {
		t.Fatal(err)
	}
	key, err := security.LoadKeyringFile(root+"/.data/secrets/master-key", 1)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := key.Decrypt(encrypted, []byte(row.ID+":SSH"))
	if err != nil {
		t.Fatal(err)
	}
	var credential sshadapter.Credential
	if json.Unmarshal(plain, &credential) != nil {
		t.Fatal("invalid SSH credential")
	}
	clear(plain)
	t.Log("SSH account", credential.Username)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client := sshadapter.NewClient(12 * time.Second)
	if action := os.Getenv("HARBOR_DIAGNOSTIC_ACTION"); action == "inspect" || action == "stop_failed_helper" || action == "data_size" || action == "data_manifest" {
		prepared, err := client.Prepare(row.Endpoint, credential)
		if err != nil {
			t.Fatal(err)
		}
		remote, err := cryptossh.Dial("tcp", prepared.Address, prepared.Config)
		if err != nil {
			t.Fatal(err)
		}
		defer remote.Close()
		session, err := remote.NewSession()
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		command := "docker ps -a --format '{{.ID}} {{.Names}} {{.Status}}'"
		if action == "inspect" && os.Getenv("HARBOR_DIAGNOSTIC_DETAIL") == "snapshot" {
			command = "docker stats --no-stream --format '{{.Name}} {{.CPUPerc}} {{.MemUsage}} {{.NetIO}}' ce8e7d23d07e; docker logs --tail 5 ce8e7d23d07e"
		}
		if action == "stop_failed_helper" {
			command = "docker stop --time 15 bb32031a180b3608c0ead7d4dd819b28c84951fdb2512c06286f556f9c64ba47"
		}
		if action == "data_size" {
			command = "sudo -n du -sb /var/lib/smartx/harbor/data"
		}
		if action == "data_manifest" {
			command = "sudo -n sh -c 'cd /var/lib/smartx/harbor/data && printf \"files=\" && find . -type f | wc -l && find . -type f -exec sha256sum {} + | sort | sha256sum'"
		}
		out, err := session.CombinedOutput(command)
		t.Log(string(out))
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	listing, err := client.Run(ctx, row.Endpoint, credential, sshadapter.CommandComposeList)
	if err != nil {
		t.Fatal("Compose list failed", err)
	}
	t.Log("Compose list", listing)
	projects, err := client.DiscoverComposeProjects(ctx, row.Endpoint, credential)
	if err != nil {
		t.Fatal(err)
	}
	for _, project := range projects {
		value, err := compose.NewAnalyzer().Analyze(ctx, project.Name, project.ComposeYAML, nil)
		if err != nil {
			t.Errorf("project %s: %v", project.Name, err)
		} else {
			t.Log(project.Name, "services", len(value.Services))
		}
	}
}
