package cmd

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pelican-dev/wings/system"
	"github.com/spf13/cobra"
)

var updateArgs struct {
	repoOwner string
	repoName  string
	force     bool
}

func newSelfupdateCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "update",
		Short: "Update wings to the latest version",
		Run:   selfupdateCmdRun,
	}

	command.Flags().StringVar(&updateArgs.repoOwner, "repo-owner", "pelican-dev", "GitHub repository owner")
	command.Flags().StringVar(&updateArgs.repoName, "repo-name", "wings", "GitHub repository name")
	command.Flags().BoolVar(&updateArgs.force, "force", false, "Force update even if on latest version")

	return command
}

func selfupdateCmdRun(_ *cobra.Command, _ []string) {
	currentVersion := system.Version
	if currentVersion == "" {
		fmt.Println("Error: current version is not defined")
		return
	}

	if currentVersion == "develop" && !updateArgs.force {
		fmt.Println("Running in development mode. Use --force to override.")
		return
	}

	fmt.Printf("Current version: %s\n", currentVersion)

	// Fetch the latest release tag from GitHub API
	latestVersionTag, err := fetchLatestGitHubRelease()
	if err != nil {
		fmt.Printf("Failed to fetch latest version: %v\n", err)
		return
	}

	currentVersionTag := "v" + currentVersion
	if currentVersion == "develop" {
		currentVersionTag = currentVersion
	}

	if latestVersionTag == currentVersionTag && !updateArgs.force {
		fmt.Printf("You are running the latest version: %s\n", currentVersion)
		return
	}

	binaryName := determineBinaryName()
	if binaryName == "" {
		fmt.Printf("Error: unsupported architecture: %s\n", runtime.GOARCH)
		return
	}

	fmt.Printf("Updating from %s to %s\n", currentVersionTag, latestVersionTag)

	if err := performUpdate(latestVersionTag, binaryName); err != nil {
		fmt.Printf("Update failed: %v\n", err)
		return
	}

	fmt.Println("\nUpdate successful! Please restart the wings service (e.g., systemctl restart wings)")
}

func performUpdate(version, binaryName string) error {
	downloadURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s",
		updateArgs.repoOwner, updateArgs.repoName, version, binaryName)
	checksumURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/checksums.txt",
		updateArgs.repoOwner, updateArgs.repoName, version)

	tmpDir, err := os.MkdirTemp("", "wings-update-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	checksumPath := filepath.Join(tmpDir, "checksums.txt")
	if err := downloadWithProgress(checksumURL, checksumPath); err != nil {
		return fmt.Errorf("failed to download checksums: %v", err)
	}

	binaryPath := filepath.Join(tmpDir, binaryName)
	if err := downloadWithProgress(downloadURL, binaryPath); err != nil {
		return fmt.Errorf("failed to download binary: %v", err)
	}

	if err := verifyChecksum(binaryPath, checksumPath, binaryName); err != nil {
		return fmt.Errorf("checksum verification failed: %v", err)
	}

	if err := os.Chmod(binaryPath, 0755); err != nil {
		return fmt.Errorf("failed to set executable permissions: %v", err)
	}

	currentExecutable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to locate current executable: %v", err)
	}

	// Try rename first (faster if on same filesystem)
	err = os.Rename(binaryPath, currentExecutable)
	if err != nil {
		// If rename fails (likely due to cross-filesystem), use copy instead
		fmt.Println("Direct replacement failed, using copy method...")
		
		// Open source file
		src, err := os.Open(binaryPath)
		if err != nil {
			return fmt.Errorf("failed to open source file: %v", err)
		}
		defer src.Close()
		
		// Create a temporary file in the same directory as the executable
		execDir := filepath.Dir(currentExecutable)
		tempExec := filepath.Join(execDir, fmt.Sprintf(".%s.new", filepath.Base(currentExecutable)))
		
		// Create the new executable file
		dst, err := os.OpenFile(tempExec, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
		if err != nil {
			return fmt.Errorf("failed to create new executable: %v", err)
		}
		
		// Copy the content
		_, err = io.Copy(dst, src)
		dst.Close()
		if err != nil {
			os.Remove(tempExec) // Clean up on failure
			return fmt.Errorf("failed to copy new binary: %v", err)
		}
		
		// Replace the old executable with the new one
		err = os.Rename(tempExec, currentExecutable)
		if err != nil {
			os.Remove(tempExec) // Clean up on failure
			return fmt.Errorf("failed to replace executable: %v", err)
		}
	}

	return nil
}

func downloadWithProgress(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %s", resp.Status)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	filename := filepath.Base(dest)
	fmt.Printf("Downloading %s (%.2f MB)...\n", filename, float64(resp.ContentLength)/1024/1024)

	pw := &progressWriter{
		Writer:    out,
		Total:     resp.ContentLength,
		StartTime: time.Now(),
	}

	_, err = io.Copy(pw, resp.Body)
	fmt.Println()
	return err
}

func fetchLatestGitHubRelease() (string, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", updateArgs.repoOwner, updateArgs.repoName)

	resp, err := http.Get(apiURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var releaseData struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&releaseData); err != nil {
		return "", err
	}

	return releaseData.TagName, nil
}

func determineBinaryName() string {
	switch runtime.GOARCH {
	case "amd64":
		return "wings_linux_amd64"
	case "arm64":
		return "wings_linux_arm64"
	default:
		return ""
	}
}

func verifyChecksum(binaryPath, checksumPath, binaryName string) error {
	checksumData, err := os.ReadFile(checksumPath)
	if err != nil {
		return err
	}

	var expectedChecksum string
	for _, line := range strings.Split(string(checksumData), "\n") {
		if strings.HasSuffix(line, binaryName) {
			parts := strings.Fields(line)
			if len(parts) > 0 {
				expectedChecksum = parts[0]
			}
			break
		}
	}
	if expectedChecksum == "" {
		return fmt.Errorf("checksum not found for %s", binaryName)
	}

	file, err := os.Open(binaryPath)
	if err != nil {
		return err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return err
	}
	actualChecksum := fmt.Sprintf("%x", hasher.Sum(nil))

	if actualChecksum == expectedChecksum {
		fmt.Printf("Checksum verification successful!\n")
	}

	if actualChecksum != expectedChecksum {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedChecksum, actualChecksum)
	}

	return nil
}

type progressWriter struct {
	io.Writer
	Total     int64
	Written   int64
	StartTime time.Time
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.Writer.Write(p)
	pw.Written += int64(n)

	if pw.Total > 0 {
		percent := float64(pw.Written) / float64(pw.Total) * 100
		fmt.Printf("\rProgress: %.2f%%", percent)
	}

	return n, err
}
