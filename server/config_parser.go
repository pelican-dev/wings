package server

import (
	"os"
	"runtime"

	"github.com/gammazero/workerpool"

	"github.com/pelican-dev/wings/internal/ufs"
)

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
			var file ufs.File
			var err error
			if f.AllowCreateFile {
				file, err = s.Filesystem().UnixFS().Touch(f.FileName, ufs.O_RDWR|ufs.O_CREATE, 0o644)
			} else {
				file, err = s.Filesystem().UnixFS().Open(f.FileName)
			}
			if err != nil {
				if !os.IsNotExist(err) || f.AllowCreateFile {
					s.Log().WithField("file_name", f.FileName).WithField("error", err).Error("failed to open file for configuration")
				} else {
					s.Log().WithField("file_name", f.FileName).Debug("file not created")
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
