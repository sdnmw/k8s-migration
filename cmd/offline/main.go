package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/smartx/sks-migration-center/internal/offline"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: offline <pack|verify|import|deploy|sign|verify-signature> [options]")
	}
	switch args[0] {
	case "pack":
		flags := flag.NewFlagSet("pack", flag.ContinueOnError)
		source := flags.String("source", "", "bundle source directory")
		destination := flags.String("output", "", "new .tar.gz output path")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *source == "" || *destination == "" {
			return errors.New("pack requires --source and --output")
		}
		file, err := os.OpenFile(*destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("create bundle without overwriting existing output: %w", err)
		}
		if err := offline.PackDirectory(*source, file); err != nil {
			_ = file.Close()
			_ = os.Remove(*destination)
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		_, err = fmt.Fprintf(output, "offline bundle created: %s\n", *destination)
		return err
	case "verify":
		flags := flag.NewFlagSet("verify", flag.ContinueOnError)
		directory := flags.String("directory", "", "extracted bundle directory")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *directory == "" {
			return errors.New("verify requires --directory")
		}
		manifest, err := offline.VerifyDirectory(*directory)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(output, "bundle %s verified: %d files\n", manifest.BundleVersion, len(manifest.Files))
		return err
	case "import":
		return importImages(args[1:], output)
	case "deploy":
		return deployBundle(args[1:], output)
	case "sign":
		return signArchive(args[1:], output)
	case "verify-signature":
		return verifyArchiveSignature(args[1:], output)
	default:
		return fmt.Errorf("unknown offline command %q", args[0])
	}
}

func signArchive(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("sign", flag.ContinueOnError)
	archive := flags.String("archive", "", "offline .tar.gz archive")
	privateKeyPath := flags.String("private-key-file", "", "Ed25519 PKCS#8 PEM private key")
	destination := flags.String("output", "", "new signature bundle path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *archive == "" || *privateKeyPath == "" || *destination == "" {
		return errors.New("sign requires --archive, --private-key-file and --output")
	}
	key, err := os.Open(*privateKeyPath)
	if err != nil {
		return fmt.Errorf("open signing key: %w", err)
	}
	defer key.Close()
	signature, err := offline.SignArchive(*archive, key)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(*destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create signature without overwriting existing output: %w", err)
	}
	if err := offline.EncodeArchiveSignature(file, signature); err != nil {
		_ = file.Close()
		_ = os.Remove(*destination)
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "offline bundle signature created: %s key=%s\n", *destination, signature.KeyID)
	return err
}

func verifyArchiveSignature(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("verify-signature", flag.ContinueOnError)
	archive := flags.String("archive", "", "offline .tar.gz archive")
	signaturePath := flags.String("signature", "", "signature bundle path")
	publicKeyPath := flags.String("public-key-file", "", "trusted Ed25519 PKIX PEM public key")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *archive == "" || *signaturePath == "" || *publicKeyPath == "" {
		return errors.New("verify-signature requires --archive, --signature and --public-key-file")
	}
	signature, err := os.Open(*signaturePath)
	if err != nil {
		return fmt.Errorf("open signature bundle: %w", err)
	}
	defer signature.Close()
	publicKey, err := os.Open(*publicKeyPath)
	if err != nil {
		return fmt.Errorf("open trusted public key: %w", err)
	}
	defer publicKey.Close()
	if err := offline.VerifyArchiveSignature(*archive, signature, publicKey); err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, "offline bundle signature verified")
	return err
}

func importImages(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("import", flag.ContinueOnError)
	directory := flags.String("directory", "", "verified extracted bundle directory")
	endpoint := flags.String("endpoint", "", "Harbor/OCI registry endpoint")
	usernameFile := flags.String("username-file", "", "path to a file containing the registry username")
	passwordFile := flags.String("password-file", "", "path to a file containing the registry password")
	selected := flags.String("image", "", "optional locked image name to import")
	targetProject := flags.String("target-project", "", "optional Harbor project override for all locked targets")
	insecure := flags.Bool("insecure", false, "allow HTTP and skip TLS verification for an explicitly trusted lab registry")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *directory == "" || *endpoint == "" || *usernameFile == "" || *passwordFile == "" {
		return errors.New("import requires --directory, --endpoint, --username-file and --password-file")
	}
	if _, err := offline.VerifyDirectory(*directory); err != nil {
		return fmt.Errorf("refuse registry import from unverified bundle: %w", err)
	}
	username, err := readSecret(*usernameFile)
	if err != nil {
		return err
	}
	password, err := readSecret(*passwordFile)
	if err != nil {
		return err
	}
	lockFile, err := os.Open(*directory + string(os.PathSeparator) + offline.ImageLockName)
	if err != nil {
		return err
	}
	lock, err := offline.LoadImageLock(lockFile)
	_ = lockFile.Close()
	if err != nil {
		return err
	}
	importer, err := offline.NewRegistryImporter(offline.RegistryConfig{Endpoint: *endpoint, Username: username, Password: password, Insecure: *insecure})
	username, password = "", ""
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	encoder := json.NewEncoder(output)
	imported := 0
	for _, image := range lock.Images {
		if *selected != "" && image.Name != *selected {
			continue
		}
		if *targetProject != "" {
			image, err = offline.RetargetImage(image, *targetProject)
			if err != nil {
				return err
			}
		}
		result, err := importer.Import(ctx, *directory, image)
		if err != nil {
			return err
		}
		if err := encoder.Encode(result); err != nil {
			return err
		}
		imported++
	}
	if imported == 0 {
		return fmt.Errorf("locked image %q was not found", *selected)
	}
	return nil
}

func readSecret(path string) (string, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read registry credential file: %w", err)
	}
	secret := strings.TrimSpace(string(value))
	if secret == "" {
		return "", errors.New("registry credential file is empty")
	}
	return secret, nil
}
