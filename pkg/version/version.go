package version

import (
	"crypto/md5"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
)

var (
	versionHash string
	versionOnce sync.Once
)

// GetVersionHash returns a hash of the executable
// This hash will change whenever the code is recompiled
func GetVersionHash() string {
	versionOnce.Do(func() {
		versionHash = computeExecutableHash()
	})
	return versionHash
}

// computeExecutableHash computes an MD5 hash of the current executable
// This serves as a version identifier that changes on every rebuild
func computeExecutableHash() string {
	execPath, err := os.Executable()
	if err != nil {
		log.Printf("Warning: failed to get executable path: %v", err)
		return "unknown"
	}

	file, err := os.Open(execPath)
	if err != nil {
		log.Printf("Warning: failed to open executable for hashing: %v", err)
		return "unknown"
	}
	defer func() {
		_ = file.Close()
	}()

	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		log.Printf("Warning: failed to hash executable: %v", err)
		return "unknown"
	}

	return fmt.Sprintf("%x", hash.Sum(nil))
}
