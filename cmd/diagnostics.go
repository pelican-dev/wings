package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/AlecAivazis/survey/v2/terminal"
	"github.com/apex/log"
	"github.com/docker/docker/api/types"
	dockerSystem "github.com/docker/docker/api/types/system" // Alias the correct system package
	"github.com/docker/docker/pkg/parsers/kernel"
	"github.com/docker/docker/pkg/parsers/operatingsystem"
	"github.com/goccy/go-json"
	"github.com/spf13/cobra"

	"github.com/pelican-dev/wings/config"
	"github.com/pelican-dev/wings/environment"
	"github.com/pelican-dev/wings/loggers/cli"
	"github.com/pelican-dev/wings/system"
)

const (
	DefaultHastebinUrl = "https://logs.pelican.dev"
	DefaultLogLines    = 200
)

var diagnosticsArgs struct {
	IncludeEndpoints   bool
	IncludeLogs        bool
	ReviewBeforeUpload bool
	HastebinURL        string
	LogLines           int
}

func newDiagnosticsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "diagnostics",
		Short: "Collect and report information about this Wings instance to assist in debugging.",
		PreRun: func(cmd *cobra.Command, args []string) {
			initConfig()
			log.SetHandler(cli.Default)
		},
		Run: diagnosticsCmdRun,
	}

	command.Flags().StringVar(&diagnosticsArgs.HastebinURL, "hastebin-url", DefaultHastebinUrl, "the url of the hastebin instance to use")
	command.Flags().IntVar(&diagnosticsArgs.LogLines, "log-lines", DefaultLogLines, "the number of log lines to include in the report")

	return command
}

// diagnosticsCmdRun collects diagnostics about wings, its configuration and the node.
// We collect:
// - wings and docker versions
// - relevant parts of daemon configuration
// - the docker debug output
// - running docker containers
// - logs
func diagnosticsCmdRun(*cobra.Command, []string) {
	questions := []*survey.Question{
		{
			Name:   "IncludeEndpoints",
			Prompt: &survey.Confirm{Message: "Do you want to include endpoints (i.e. the FQDN/IP of your panel)?", Default: false},
		},
		{
			Name:   "IncludeLogs",
			Prompt: &survey.Confirm{Message: "Do you want to include the latest logs?", Default: true},
		},
		{
			Name: "ReviewBeforeUpload",
			Prompt: &survey.Confirm{
				Message: "Do you want to review the collected data before uploading to " + diagnosticsArgs.HastebinURL + "?",
				Help:    "The data, especially the logs, might contain sensitive information, so you should review it. You will be asked again if you want to upload.",
				Default: true,
			},
		},
	}
	if err := survey.Ask(questions, &diagnosticsArgs); err != nil {
		if err == terminal.InterruptErr {
			return
		}
		panic(err)
	}

	dockerVersion, dockerInfo, dockerErr := getDockerInfo()

	output := &strings.Builder{}
	type diagnosticField struct {
		name   string
		format string
		args   []any
	}
	diagnosticFields := []diagnosticField{
		{
			name:   "title",
			format: "Pelican Wings - Diagnostics Report",
		},
		{
			name:   "versions header",
			format: printHeader("Versions"),
		},
		{
			name:   "wings version",
			format: "               Wings: %v",
			args:   []any{system.Version},
		},
		{
			name:   "docker version",
			format: "              Docker: %v",
			args: []any{
				func() string {
					version := "unknown"
					if dockerErr != nil {
						log.WithError(dockerErr).Warn("failed to get docker version")
					} else {
						version = dockerVersion.Version
					}
					return version
				}(),
			},
		},
		{
			name:   "kernel version",
			format: "              Kernel: %v",
			args: []any{
				func() string {
					version := "unknown"
					kernelver, err := kernel.GetKernelVersion()
					if err != nil {
						log.WithError(err).Warn("failed to get kernel version")
					} else {
						version = fmt.Sprint(kernelver)
					}
					return version
				}(),
			},
		},
		{
			name:   "operating system",
			format: "                  OS: %v",
			args: []any{
				func() string {
					os, err := operatingsystem.GetOperatingSystem()
					if err != nil {
						log.WithError(err).Warn("failed to get operating system")
						os = "unknown"
					}
					return os
				}(),
			},
		},
	}
	diagnosticFields = append(diagnosticFields, diagnosticField{
		name:   "wings configuration header",
		format: printHeader("Wings Configuration"),
	})
	err := config.FromFile(config.DefaultLocation)
	if err != nil {
		log.WithError(err).Warn("failed to load configuration so configuration information will not be included in the report")
		diagnosticFields = append(diagnosticFields, diagnosticField{
			name:   "wings configuration",
			format: "Failed to load configuration",
		},
		)
	}
	cfg := config.Get()
	if err == nil {
		diagnosticFields = append(diagnosticFields, []diagnosticField{
			{
				name:   "wings configuration header",
				format: printHeader("Wings Configuration"),
			},
			{
				name:   "panel location",
				format: "      Panel Location: %v\n",
				args: []any{
					redact(cfg.PanelLocation),
				},
			},
			{
				name:   "internal webserver",
				format: "  Internal Webserver: %v : %v",
				args: []any{
					redact(cfg.Api.Host),
					cfg.Api.Port,
				},
			},
			{
				name:   "ssl enabled",
				format: "         SSL Enabled: %v",
				args: []any{
					cfg.Api.Ssl.Enabled,
				},
			},
			{
				name:   "ssl certificate",
				format: "     SSL Certificate: %v",
				args: []any{
					redact(cfg.Api.Ssl.CertificateFile),
				},
			},
			{
				name:   "ssl key",
				format: "             SSL Key: %v\n",
				args: []any{
					redact(cfg.Api.Ssl.KeyFile),
				},
			},
			{
				name:   "sftp server",
				format: "         SFTP Server: %v : %v",
				args: []any{
					redact(cfg.System.Sftp.Address),
					cfg.System.Sftp.Port,
				},
			},
			{
				name:   "sftp read-only",
				format: "      SFTP Read-Only: %v\n",
				args: []any{
					cfg.System.Sftp.ReadOnly,
				},
			},
			{
				name:   "root directory",
				format: "      Root Directory: %v",
				args: []any{
					cfg.System.RootDirectory,
				},
			},
			{
				name:   "logs directory",
				format: "      Logs Directory: %v",
				args: []any{
					cfg.System.LogDirectory,
				},
			},
			{
				name:   "data directory",
				format: "      Data Directory: %v",
				args: []any{
					cfg.System.Data,
				},
			},
			{
				name:   "archive directory",
				format: "   Archive Directory: %v",
				args: []any{
					cfg.System.ArchiveDirectory,
				},
			},
			{
				name:   "backup directory",
				format: "    Backup Directory: %v\n",
				args: []any{
					cfg.System.BackupDirectory,
				},
			},
			{
				name:   "username",
				format: "            Username: %v",
				args: []any{
					cfg.System.Username,
				},
			},
			{
				name:   "debug mode",
				format: "          Debug Mode: %v",
				args: []any{
					cfg.Debug,
				},
			},
		}...)
	}
	diagnosticFields = append(diagnosticFields, []diagnosticField{
		{
			name:   "server time",
			format: "         Server Time: %v",
			args: []any{
				time.Now().Format(time.RFC1123Z),
			},
		},
		{
			name:   "docker info header",
			format: printHeader("Docker: Info"),
		},
	}...)

	if dockerErr != nil {
		log.WithError(dockerErr).Warn("failed to get docker info, so docker information will not be included in the report")
		diagnosticFields = append(diagnosticFields, diagnosticField{
			name:   "docker info",
			format: "Failed to get docker info due to error %v",
			args: []any{
				dockerErr,
			},
		})
	} else {
		diagnosticFields = append(diagnosticFields, []diagnosticField{
			{
				name:   "docker server version",
				format: "Server Version: %v",
				args: []any{
					dockerInfo.ServerVersion,
				},
			},
			{
				name:   "docker storage driver",
				format: "Storage Driver: %v",
				args: []any{
					dockerInfo.Driver,
				},
			},
		}...)
		if dockerInfo.DriverStatus != nil {
			for _, pair := range dockerInfo.DriverStatus {
				diagnosticFields = append(diagnosticFields, diagnosticField{
					name:   "docker driver status",
					format: "  %v: %v",
					args: []any{
						pair[0],
						pair[1],
					},
				})
			}
		}
		if dockerInfo.SystemStatus != nil {
			for _, pair := range dockerInfo.SystemStatus {
				diagnosticFields = append(diagnosticFields, diagnosticField{
					name:   "docker driver status",
					format: "  %v: %v",
					args: []any{
						pair[0],
						pair[1],
					},
				})
			}
		}
		diagnosticFields = append(diagnosticFields, []diagnosticField{
			{
				name:   "docker LoggingDriver",
				format: "LoggingDriver: %v",
				args: []any{
					dockerInfo.LoggingDriver,
				},
			},
			{
				name:   "docker CgroupDriver",
				format: " CgroupDriver: %v",
				args: []any{
					dockerInfo.CgroupDriver,
				},
			},
		}...)
		if len(dockerInfo.Warnings) > 0 {
			for _, w := range dockerInfo.Warnings {
				diagnosticFields = append(diagnosticFields, diagnosticField{
					name:   "docker warning",
					format: "%v",
					args: []any{
						w,
					},
				})
			}
		}
	}

	diagnosticFields = append(diagnosticFields, diagnosticField{
		name:   "docker running containers header",
		format: printHeader("Docker: Running Containers"),
	})
	c := exec.Command("docker", "ps")
	if co, err := c.Output(); err == nil {
		diagnosticFields = append(diagnosticFields, diagnosticField{
			name:   "docker running containers",
			format: "%v",
			args: []any{
				string(co),
			},
		})
	} else {
		diagnosticFields = append(diagnosticFields, diagnosticField{
			name:   "docker running containers",
			format: "Couldn't list containers: %v",
			args: []any{
				err,
			},
		})
	}

	diagnosticFields = append(diagnosticFields, diagnosticField{
		name:   "latest wings logs header",
		format: printHeader("Latest Wings Logs"),
	})
	if diagnosticsArgs.IncludeLogs {
		p := "/var/log/pelican/wings.log"
		if cfg != nil {
			p = path.Join(cfg.System.LogDirectory, "wings.log")
		}
		if c, err := exec.Command("tail", "-n", strconv.Itoa(diagnosticsArgs.LogLines), p).Output(); err != nil {
			diagnosticFields = append(diagnosticFields, diagnosticField{
				name:   "no logs",
				format: "No logs found or an error occurred.",
			})
		} else {
			diagnosticFields = append(diagnosticFields, diagnosticField{
				name:   "logs",
				format: "%v",
				args: []any{
					string(c),
				},
			})
		}
	} else {
		diagnosticFields = append(diagnosticFields, diagnosticField{
			name:   "logs redacted",
			format: "Logs redacted.",
		})
	}

	for _, f := range diagnosticFields {
		_, err := fmt.Fprintf(output, f.format+"\n", f.args...)
		if err != nil {
			log.WithError(err).Warnf("failed to write diagnostic field '%v'", f.name)
		}
	}

	if !diagnosticsArgs.IncludeEndpoints {
		s := output.String()
		output.Reset()
		s = strings.ReplaceAll(s, cfg.PanelLocation, "{redacted}")
		s = strings.ReplaceAll(s, cfg.Api.Host, "{redacted}")
		s = strings.ReplaceAll(s, cfg.Api.Ssl.CertificateFile, "{redacted}")
		s = strings.ReplaceAll(s, cfg.Api.Ssl.KeyFile, "{redacted}")
		s = strings.ReplaceAll(s, cfg.System.Sftp.Address, "{redacted}")
		output.WriteString(s)
	}

	fmt.Println("\n---------------  generated report  ---------------")
	fmt.Println(output.String())
	fmt.Print("---------------   end of report    ---------------\n\n")

	upload := !diagnosticsArgs.ReviewBeforeUpload
	if !upload {
		survey.AskOne(&survey.Confirm{Message: "Upload to " + diagnosticsArgs.HastebinURL + "?", Default: false}, &upload)
	}
	if upload {
		u, err := uploadToHastebin(diagnosticsArgs.HastebinURL, output.String())
		if err == nil {
			fmt.Println("Your report is available here: ", u)
		}
	}
}

func getDockerInfo() (types.Version, dockerSystem.Info, error) {
	client, err := environment.Docker()
	if err != nil {
		return types.Version{}, dockerSystem.Info{}, err
	}
	dockerVersion, err := client.ServerVersion(context.Background())
	if err != nil {
		return types.Version{}, dockerSystem.Info{}, err
	}
	dockerInfo, err := client.Info(context.Background())
	if err != nil {
		return types.Version{}, dockerSystem.Info{}, err
	}
	return dockerVersion, dockerInfo, nil
}

func uploadToHastebin(hbUrl, content string) (string, error) {
	u, err := url.Parse(hbUrl)
	if err != nil {
		return "", err
	}

	formData := new(bytes.Buffer)
	formWriter := multipart.NewWriter(formData)
	formWriter.WriteField("c", content)
	formWriter.WriteField("e", "14d")
	formWriter.Close()

	res, err := http.Post(u.String(), formWriter.FormDataContentType(), formData)
	if err != nil || res.StatusCode != 200 {
		fmt.Println("Failed to upload report to ", u.String(), err, res.StatusCode)
		myb, _ := io.ReadAll(res.Body)
		fmt.Println(string(myb))
		return "", err
	}
	pres := make(map[string]interface{})
	body, err := io.ReadAll(res.Body)
	if err != nil {
		fmt.Println("Failed to parse response.", err)
		return "", err
	}
	json.Unmarshal(body, &pres)
	if pasteUrl, ok := pres["url"].(string); ok {
		return pasteUrl, nil
	}
	return "", errors.New("failed to find key in response")
}

func redact(s string) string {
	if !diagnosticsArgs.IncludeEndpoints {
		return "{redacted}"
	}
	return s
}

func printHeader(title string) string {
	output := ""
	output += fmt.Sprintln("\n|\n|", title)
	output += fmt.Sprint("| ------------------------------")
	return output
}
