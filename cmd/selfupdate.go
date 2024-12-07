package cmd

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/pelican-dev/wings/system"
	"github.com/spf13/cobra"
)

var updateArgs struct {
	repoOwner string
	repoName  string
}

func newSelfupdateCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "update",
		Short: "Update the wings to the latest version",
		Run:   selfupdateCmdRun,
	}

	command.Flags().StringVar(&updateArgs.repoOwner, "repo-owner", "pelican-dev", "GitHub username or organization that owns the repository containing the updates")
	command.Flags().StringVar(&updateArgs.repoName, "repo-name", "wings", "The name of the GitHub repository to fetch updates from")

	return command
}

func selfupdateCmdRun(*cobra.Command, []string) {
	currentVersion := system.Version
	if currentVersion == "" {
		fmt.Println("Error: Current version is not defined")
		return
	}

	if currentVersion == "develop" {
		fmt.Println("Running in development mode. Skipping update.")
		return
	}

	fmt.Println("Current version:", currentVersion)

	// Fetch the latest release tag from GitHub API
	latestVersionTag, err := fetchLatestGitHubRelease()
	if err != nil {
		fmt.Println("Failed to fetch the latest version:", err)
		return
	}

	currentVersionTag := "v" + currentVersion
	if latestVersionTag == currentVersionTag {
		fmt.Println("You are running the latest version:", currentVersion)
		return
	}

	fmt.Printf("A new version is available: %s (current: %s)\n", latestVersionTag, currentVersionTag)

	binaryName := determineBinaryName()
	if binaryName == "" {
		fmt.Println("Unsupported architecture")
		return
	}

	downloadURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s", updateArgs.repoOwner, updateArgs.repoName, latestVersionTag, binaryName)
	checksumURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/checksums.txt", updateArgs.repoOwner, updateArgs.repoName, latestVersionTag)

	fmt.Println("Downloading checksums.txt...")
	checksumFile, err := downloadFile(checksumURL, "checksums.txt")
	if err != nil {
		fmt.Println("Failed to download checksum file:", err)
		return
	}
	defer os.Remove(checksumFile)

	fmt.Println("Downloading", binaryName, "...")
	binaryFile, err := downloadFile(downloadURL, binaryName)
	if err != nil {
		fmt.Println("Failed to download binary file:", err)
		return
	}
	defer os.Remove(binaryFile)

	if err := verifyChecksum(binaryFile, checksumFile, binaryName); err != nil {
		fmt.Println("Checksum verification failed:", err)
		return
	}
	fmt.Println("\nChecksum verification successful.")

	currentExecutable, err := os.Executable()
	if err != nil {
		fmt.Println("Failed to locate current executable:", err)
		return
	}

	if err := os.Chmod(binaryFile, 0755); err != nil {
		fmt.Println("Failed to set executable permissions on the new binary:", err)
		return
	}

	if err := replaceBinary(currentExecutable, binaryFile); err != nil {
		fmt.Println("Failed to replace executable:", err)
		return
	}

	fmt.Println("Now restart the wings service. Example: systemctl restart wings")

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

func downloadFile(url, fileName string) (string, error) {
	tmpFile, err := os.CreateTemp("", fileName)
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status: %s", resp.Status)
	}

	fmt.Printf("Downloading %s (%.2f MB)...\n", fileName, float64(resp.ContentLength)/1024/1024)
	progressWriter := &progressWriter{Writer: tmpFile, Total: resp.ContentLength}
	if _, err := io.Copy(progressWriter, resp.Body); err != nil {
		return "", err
	}

	fmt.Println() // Ensure a newline after download progress
	return tmpFile.Name(), nil
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

	if actualChecksum != expectedChecksum {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedChecksum, actualChecksum)
	}

	return nil
}

func replaceBinary(currentPath, newPath string) error {
	return os.Rename(newPath, currentPath)
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
