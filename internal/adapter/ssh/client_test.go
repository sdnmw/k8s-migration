package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	cryptossh "golang.org/x/crypto/ssh"
)

func TestProbeUsesPinnedHostKeyAndOnlyControlledCommands(t *testing.T) {
	endpoint, credential, commands := startTestSSHServer(t)
	client := NewClient(3 * time.Second)
	prepared, err := client.Prepare(endpoint, credential)
	if err != nil || len(prepared.Config.HostKeyAlgorithms) == 0 || prepared.Config.HostKeyAlgorithms[0] != cryptossh.KeyAlgoED25519 {
		t.Fatalf("expected deterministic Ed25519 host-key preference: %+v, %v", prepared.Config.HostKeyAlgorithms, err)
	}
	result, err := client.Probe(context.Background(), endpoint, credential)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if result.Runtime["dockerVersion"] != "29.1.0" || result.Runtime["composeVersion"] != "v2.40.0" {
		t.Fatalf("unexpected runtime: %+v commands=%v", result.Runtime, commands())
	}
	if len(result.Checks) != 3 {
		t.Fatalf("unexpected checks: %+v", result.Checks)
	}
	if got := commands(); !reflect.DeepEqual(got, []string{commandText[CommandDockerVersion], commandText[CommandComposeVersion]}) {
		t.Fatalf("unexpected remote commands: %v", got)
	}
	if _, err := client.Run(context.Background(), endpoint, credential, CommandID("rm-everything")); err == nil {
		t.Fatal("unknown command ID must be rejected")
	}
}

func TestPrepareRequiresPinnedHostFingerprintAndPrivateKey(t *testing.T) {
	client := NewClient(time.Second)
	if _, err := client.Prepare("ssh://host.example", Credential{Username: "migration"}); err == nil {
		t.Fatal("missing host fingerprint must be rejected")
	}
	if _, err := client.Prepare("https://host.example", Credential{Username: "migration"}); err == nil {
		t.Fatal("non-SSH endpoint must be rejected")
	}
}

func TestProbeSupportsPasswordAuthentication(t *testing.T) {
	endpoint, credential, _ := startTestSSHServer(t)
	credential.PrivateKey = ""
	credential.Password = "test-password"
	result, err := NewClient(3*time.Second).Probe(context.Background(), endpoint, credential)
	if err != nil || result.Runtime["composeVersion"] != "v2.40.0" {
		t.Fatalf("password probe = %+v, %v", result, err)
	}
}

func TestFingerprintProbeDoesNotExecuteRemoteCommands(t *testing.T) {
	endpoint, credential, commands := startTestSSHServer(t)
	fingerprint, err := NewClient(3*time.Second).Fingerprint(context.Background(), endpoint)
	if err != nil || fingerprint != credential.HostKeyFingerprint {
		t.Fatalf("fingerprint probe failed: %v", err)
	}
	if len(commands()) != 0 {
		t.Fatal("fingerprint probe executed a command")
	}
}

func startTestSSHServer(t *testing.T) (string, Credential, func() []string) {
	t.Helper()
	_, hostPrivate, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, _ := cryptossh.NewSignerFromKey(hostPrivate)
	clientPublic, clientPrivate, _ := ed25519.GenerateKey(rand.Reader)
	clientSSHKey, _ := cryptossh.NewPublicKey(clientPublic)
	encodedPrivate, _ := x509.MarshalPKCS8PrivateKey(clientPrivate)
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedPrivate})

	serverConfig := &cryptossh.ServerConfig{PasswordCallback: func(_ cryptossh.ConnMetadata, password []byte) (*cryptossh.Permissions, error) {
		if string(password) == "test-password" {
			return nil, nil
		}
		return nil, cryptossh.ErrNoAuth
	}, PublicKeyCallback: func(_ cryptossh.ConnMetadata, key cryptossh.PublicKey) (*cryptossh.Permissions, error) {
		if reflect.DeepEqual(key.Marshal(), clientSSHKey.Marshal()) {
			return nil, nil
		}
		return nil, cryptossh.ErrNoAuth
	}}
	serverConfig.AddHostKey(hostSigner)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	var lock sync.Mutex
	seen := []string{}
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		_, channels, requests, handshakeErr := cryptossh.NewServerConn(connection, serverConfig)
		if handshakeErr != nil {
			return
		}
		go cryptossh.DiscardRequests(requests)
		for newChannel := range channels {
			if newChannel.ChannelType() != "session" {
				_ = newChannel.Reject(cryptossh.UnknownChannelType, "session only")
				continue
			}
			channel, channelRequests, channelErr := newChannel.Accept()
			if channelErr != nil {
				continue
			}
			go func() {
				defer channel.Close()
				for request := range channelRequests {
					if request.Type != "exec" {
						_ = request.Reply(false, nil)
						continue
					}
					var payload struct{ Command string }
					_ = cryptossh.Unmarshal(request.Payload, &payload)
					lock.Lock()
					seen = append(seen, payload.Command)
					lock.Unlock()
					_ = request.Reply(true, nil)
					switch payload.Command {
					case commandText[CommandDockerVersion]:
						_, _ = channel.Stderr().Write([]byte("warning: diagnostic stderr is not structured output\n"))
						_, _ = channel.Write([]byte("29.1.0\n"))
					case commandText[CommandComposeVersion]:
						_, _ = channel.Write([]byte("v2.40.0\n"))
					}
					_, _ = channel.SendRequest("exit-status", false, cryptossh.Marshal(struct{ Status uint32 }{0}))
					return
				}
			}()
		}
	}()
	return "ssh://" + listener.Addr().String(), Credential{
			Username: "migration", PrivateKey: string(privatePEM), HostKeyFingerprint: cryptossh.FingerprintSHA256(hostSigner.PublicKey()),
		}, func() []string {
			lock.Lock()
			defer lock.Unlock()
			return append([]string(nil), seen...)
		}
}
