package server

import (
	"os"
	"runtime"

	"fmt"
	"strings"

	"github.com/gammazero/workerpool"
	"github.com/Minenetpro/pelican-wings/internal/ufs"
)

// Helper function to replace variables in the file path of the configuration parser
func replaceParserConfigPathVariables(filename string, envvars map[string]interface{}) string {
	// Check if filename contains at least one '{' and one '}'
	// This is here for performance as 99% of the eggs configuration parsers do not have variables in its path
	if !strings.Contains(filename, "{") || !strings.Contains(filename, "}") {
		return filename
	}

	// Replace "{{" with "${" and "}}" with "}"
	filename = strings.ReplaceAll(filename, "{{", "${")
	filename = strings.ReplaceAll(filename, "}}", "}")

	// replaces ${varname} with varval
	for varname, varval := range envvars {
		filename = strings.ReplaceAll(filename, fmt.Sprintf("${%s}", varname), fmt.Sprint(varval))
	}

	return filename
}

// UpdateConfigurationFiles updates all the defined configuration files for
// a server automatically to ensure that they always use the specified values.
func (s *Server) UpdateConfigurationFiles() {
	pool := workerpool.New(runtime.NumCPU())

	s.Log().Debug("acquiring process configuration files...")
	files := s.ProcessConfiguration().ConfigurationFiles
	s.Log().Debug("acquired process configuration files")
	for _, cf := range files {
		f := cf

		pool.Submit(func() {
			filename := replaceParserConfigPathVariables(f.FileName, s.Config().EnvVars)
			file, err := func() (ufs.File, error) {
				if f.AllowCreateFile {
					return s.Filesystem().UnixFS().Touch(filename, ufs.O_RDWR|ufs.O_CREATE, 0o644)
				}
				return s.Filesystem().UnixFS().Open(filename)
			}()
			if err != nil {
				log := s.Log().WithField("file_name", filename)
				if os.IsNotExist(err) && !f.AllowCreateFile {
					log.Debug("file not created")
				} else {
					log.WithField("error", err).Error("failed to open file for configuration")
				}
				return
			}
			defer file.Close()

			if err := f.Parse(file); err != nil {
				s.Log().WithField("error", err).Error("failed to parse and update server configuration file")
			}

			s.Log().WithField("file_name", f.FileName).Debug("finished processing server configuration file")
		})
	}

	pool.StopWait()
}
