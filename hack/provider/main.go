package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

var checksumMap = map[string]string{
	"./release/devpod-provider-aws-linux-amd64": "##CHECKSUM_LINUX_AMD64##",
	// TODO Enable these once we have a proper release
	//"./release/devpod-provider-aws-linux-arm64": "##CHECKSUM_LINUX_ARM64##",
	//"./release/devpod-provider-aws-darwin-amd64":      "##CHECKSUM_DARWIN_AMD64##",
	//"./release/devpod-provider-aws-darwin-arm64":      "##CHECKSUM_DARWIN_ARM64##",
	//"./release/devpod-provider-aws-windows-amd64.exe": "##CHECKSUM_WINDOWS_AMD64##",
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "Expected version as argument")
		os.Exit(1)

		return
	}

	content, err := os.ReadFile("./hack/provider/provider.yaml")
	if err != nil {
		panic(err)
	}

	replaced := strings.Replace(string(content), "##VERSION##", os.Args[1], -1)

	for k, v := range checksumMap {
		checksum, err := File(k)
		if err != nil {
			panic(fmt.Errorf("generate checksum for %s: %v", k, err))
		}

		replaced = strings.Replace(replaced, v, checksum, -1)
	}

	fmt.Print(replaced)
}

// File hashes a given file to a sha256 string
func File(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()

	_, err = io.Copy(hash, file)
	if err != nil {
		return "", err
	}

	return strings.ToLower(hex.EncodeToString(hash.Sum(nil))), nil
}
