package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"
)

const (
	privateKeyPrefix = "CALENDARR_ED25519_PRIVATE_KEY_V1="
	publicKeyPrefix  = "CALENDARR_ED25519_PUBLIC_KEY_V1="
)

func main() {
	generate := flag.Bool("generate", false, "generate a new signing key")
	keyPath := flag.String("key", "release_signing_private_key.txt", "private key file")
	inputPath := flag.String("in", "", "file to sign")
	outputPath := flag.String("out", "", "signature output file")
	flag.Parse()

	if *generate {
		if err := generateKey(*keyPath); err != nil {
			fatal(err)
		}
		return
	}

	if *inputPath == "" || *outputPath == "" {
		fatal(fmt.Errorf("-in and -out are required when signing"))
	}
	if err := signFile(*keyPath, *inputPath, *outputPath); err != nil {
		fatal(err)
	}
}

func generateKey(path string) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate signing key: %w", err)
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists; refusing to overwrite it", path)
	}
	privateLine := privateKeyPrefix + base64.StdEncoding.EncodeToString(priv) + "\n"
	if err := os.WriteFile(path, []byte(privateLine), 0600); err != nil {
		return fmt.Errorf("write private key: %w", err)
	}
	fmt.Println(publicKeyPrefix + base64.StdEncoding.EncodeToString(pub))
	fmt.Println("Back up " + path + " outside the repository. Losing it requires a manual trust-rotation release.")
	return nil
}

func signFile(keyPath, inputPath, outputPath string) error {
	priv, err := readPrivateKey(keyPath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("read input: %w", err)
	}
	sig := ed25519.Sign(priv, data)
	out := base64.StdEncoding.EncodeToString(sig) + "\n"
	if err := os.WriteFile(outputPath, []byte(out), 0644); err != nil {
		return fmt.Errorf("write signature: %w", err)
	}
	return nil
}

func readPrivateKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read signing key: %w", err)
	}
	text := strings.TrimSpace(string(raw))
	text = strings.TrimPrefix(text, privateKeyPrefix)
	key, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return nil, fmt.Errorf("decode signing key: %w", err)
	}
	if len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("signing key has %d bytes, want %d", len(key), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(key), nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
