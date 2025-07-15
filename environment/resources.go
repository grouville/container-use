package environment

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// Embedded resources for building the container image
var (
	//go:embed resources/Dockerfile.claude
	dockerfileClaude string

	//go:embed resources/inject.js
	injectJS string

	//go:embed resources/globals.js
	globalsJS string
)

// EnsureProxyImage ensures the container image with proxy exists
func EnsureProxyImage(imageName string) error {
	// Check if image already exists
	cmd := exec.Command("docker", "images", "-q", imageName)
	output, _ := cmd.Output()
	if len(output) > 0 {
		return nil
	}

	// Build the image
	return buildProxyImage(imageName)
}

// buildProxyImage builds the container image with embedded resources
func buildProxyImage(imageName string) error {
	// Create temporary build directory
	buildDir, err := os.MkdirTemp("", "cu-build-*")
	if err != nil {
		return fmt.Errorf("failed to create build dir: %w", err)
	}
	defer os.RemoveAll(buildDir)

	// Write Dockerfile
	if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte(dockerfileClaude), 0644); err != nil {
		return fmt.Errorf("failed to write Dockerfile: %w", err)
	}
	
	// Write embedded files to root of build directory (like cosmos)
	files := map[string]string{
		"inject.js":  injectJS,
		"globals.js": globalsJS,
	}

	for name, content := range files {
		path := filepath.Join(buildDir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", name, err)
		}
	}

	// Build the proxy binary for Linux first
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}
	
	// Build proxy for Linux
	proxyBinary := filepath.Join(buildDir, "container-use-proxy-linux")
	buildCmd := exec.Command("go", "build", "-o", proxyBinary, "./cmd/container-use-proxy")
	buildCmd.Dir = wd
	buildCmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	if output, err := buildCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to build proxy: %w\nOutput: %s", err, output)
	}

	// Build the Docker image
	cmd := exec.Command("docker", "build",
		"-t", imageName,
		"-f", filepath.Join(buildDir, "Dockerfile"),
		buildDir,
	)

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to build Docker image: %w\nOutput: %s", err, output)
	}

	return nil
}

// copyFile copies a single file
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	
	_, err = io.Copy(out, in)
	return err
}

// copyDir recursively copies a directory
func copyDir(src, dst string) error {
	// Create destination directory
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		
		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	
	return nil
}