package main

import (
	"fmt"
	"os"

	"telecli/internal/config"
)

func main() {
	creds, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	source := determineSource()

	fmt.Printf("API ID: %d\n", creds.APIID)
	fmt.Printf("API Hash: %s (len=%d)\n", maskHash(creds.APIHash), len(creds.APIHash))
	fmt.Printf("Source: %s\n", source)
}

func determineSource() string {
	if os.Getenv("TELECLI_API_ID") != "" && os.Getenv("TELECLI_API_HASH") != "" {
		return "environment variables"
	}

	_, err := config.LoadFromKeyringForTest()
	if err == nil {
		return "system keyring"
	}

	path := config.FallbackPathForTest()
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			return "fallback file (test override)"
		}
	}

	if path, err := config.FallbackPath(); err == nil {
		if _, err := os.Stat(path); err == nil {
			return "fallback file"
		}
	}

	return "unknown"
}

func maskHash(hash string) string {
	if len(hash) <= 4 {
		return "****"
	}
	return hash[:2] + "****" + hash[len(hash)-2:]
}
