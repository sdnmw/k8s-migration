package s3

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Config struct {
	Endpoint  string
	Region    string
	AccessKey string
	SecretKey string
	CABundle  []byte
	TLSVerify bool
}

type Probe struct {
	Bucket  string `json:"bucket"`
	Key     string `json:"key"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	payload []byte
}

type Client struct{}

func NewClient() *Client { return &Client{} }

func (c *Client) WriteProbe(ctx context.Context, config Config, bucket string) (Probe, error) {
	client, err := newClient(config)
	if err != nil {
		return Probe{}, err
	}
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return Probe{}, fmt.Errorf("check S3 bucket: %w", err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: config.Region}); err != nil {
			return Probe{}, fmt.Errorf("create S3 bucket: %w", err)
		}
	}
	payload := make([]byte, 4096)
	if _, err := rand.Read(payload); err != nil {
		return Probe{}, errors.New("generate S3 probe payload")
	}
	hash := sha256.Sum256(payload)
	probe := Probe{Bucket: bucket, Key: "sks-migration-health/" + uuid.NewString(), SHA256: hex.EncodeToString(hash[:]), Size: int64(len(payload)), payload: payload}
	if _, err := client.PutObject(ctx, bucket, probe.Key, bytes.NewReader(payload), probe.Size, minio.PutObjectOptions{ContentType: "application/octet-stream"}); err != nil {
		return Probe{}, fmt.Errorf("write S3 probe object: %w", err)
	}
	return probe, nil
}

func (c *Client) VerifyProbe(ctx context.Context, config Config, probe Probe, remove bool) error {
	client, err := newClient(config)
	if err != nil {
		return err
	}
	object, err := client.GetObject(ctx, probe.Bucket, probe.Key, minio.GetObjectOptions{})
	if err != nil {
		return fmt.Errorf("open S3 probe object: %w", err)
	}
	value, readErr := io.ReadAll(io.LimitReader(object, probe.Size+1))
	closeErr := object.Close()
	if readErr != nil {
		return fmt.Errorf("read S3 probe object: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close S3 probe object: %w", closeErr)
	}
	hash := sha256.Sum256(value)
	if int64(len(value)) != probe.Size || hex.EncodeToString(hash[:]) != probe.SHA256 || !bytes.Equal(value, probe.payload) {
		return errors.New("S3 probe object failed size or checksum validation")
	}
	if remove {
		if err := client.RemoveObject(ctx, probe.Bucket, probe.Key, minio.RemoveObjectOptions{}); err != nil {
			return fmt.Errorf("remove S3 probe object: %w", err)
		}
	}
	return nil
}

func newClient(config Config) (*minio.Client, error) {
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.User != nil || (endpoint.Path != "" && endpoint.Path != "/") || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("S3 endpoint must be an HTTP(S) origin without path, query, fragment, or userinfo")
	}
	if strings.TrimSpace(config.AccessKey) == "" || strings.TrimSpace(config.SecretKey) == "" {
		return nil, errors.New("S3 access key and secret key are required")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if endpoint.Scheme == "https" {
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if len(config.CABundle) > 0 && !roots.AppendCertsFromPEM(config.CABundle) {
			return nil, errors.New("S3 CA bundle contains no certificates")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
		if !config.TLSVerify {
			transport.TLSClientConfig.InsecureSkipVerify = true // #nosec G402 -- accepted only for explicit non-managed test profiles.
		}
	} else if config.TLSVerify {
		return nil, errors.New("S3 TLS verification requires an HTTPS endpoint")
	}
	return minio.New(endpoint.Host, &minio.Options{
		Creds: credentials.NewStaticV4(config.AccessKey, config.SecretKey, ""), Secure: endpoint.Scheme == "https",
		Region: config.Region, Transport: transport,
	})
}
