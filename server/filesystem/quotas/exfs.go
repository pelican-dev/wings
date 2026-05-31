//go:build linux

package quotas

import (
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/parkervcp/fsquota"
	"github.com/pelican-dev/wings/config"
)

var exfs struct {
	projects []exfsProject
	sync.Mutex
}

type exfsProject struct {
	ID       int
	Name     string
	BasePath string
}

const (
	projidTemplateSrc = `{{ range . }}{{ .Name }}:{{ .ID }}
{{ end }}`
	projectsTemplateSrc = `{{ range . }}{{ .ID }}:{{ .BasePath }}/{{ .Name }}
{{ end }}`

	projidFile  = `/etc/projid`
	projectFile = `/etc/projects`
)

var (
	projidTemplate   = template.Must(template.New("projid").Parse(projidTemplateSrc))
	projectsTemplate = template.Must(template.New("projects").Parse(projectsTemplateSrc))
)

// setQuota sets the quota in bytes for the specified server uuid
// A limit of 0 is treated as unlimited by xfs and ext4
func (q exfsProject) setQuota(byteLimit uint64) (err error) {
	serverDirPath := filepath.Join(q.BasePath, q.Name)
	log.WithFields(log.Fields{"server_path": serverDirPath, "limit_bytes": byteLimit}).Debug("setting quota")
	serverProject, err := fsquota.LookupProject(q.Name)
	if err != nil {
		return
	}

	limits := fsquota.Limits{}

	limits.Bytes.SetHard(byteLimit)

	if _, err = fsquota.SetProjectQuota(serverDirPath, serverProject, limits); err != nil {
		return
	}

	log.WithField("server_path", serverDirPath).Debug("quota set")
	return
}

// getQuota gets the specified quotas and usage of a specified server uuid
func (q exfsProject) getQuota() (bytesUsed int64, err error) {
	serverProject, err := fsquota.LookupProject(q.Name)
	if err != nil {
		return -1, err
	}

	projInfo, err := fsquota.GetProjectInfo(q.BasePath, serverProject)
	if err != nil {
		return -1, err
	}

	// converts the uint64 to int64.
	// This should only be an issue in the terms of exabytes...
	return int64(projInfo.BytesUsed), nil
}

// enableEXFSQuota enables quotas on a specified directory
func (q exfsProject) enableEXFSQuota() (err error) {
	serverDir, err := os.Open(filepath.Join(q.BasePath, q.Name))
	if err != nil {
		return err
	}

	defer serverDir.Close()

	// enable project quota inheritance and set project id
	if err = setXAttr(serverDir, q.ID, FS_XFLAG_PROJINHERIT); err != nil {
		log.WithFields(log.Fields{"server_uuid": q.Name, "server_path": serverDir.Name()}).Error("failed to update XATTRs for server")
		return
	}

	return
}

func (q exfsProject) addProject() (err error) {
	q.BasePath = strings.TrimSuffix(config.Get().System.Data, "/")
	exfs.Lock()
	defer exfs.Unlock()

	projects := make([]exfsProject, len(exfs.projects), len(exfs.projects)+1)
	copy(projects, exfs.projects)
	projects = append(projects, q)

	if err = writeEXFSProjects(projects); err != nil {
		log.WithError(err).Error("failed to write exfs projects")
		return
	}

	if err = q.enableEXFSQuota(); err != nil {
		log.WithError(err).Error("failed to enable quota")
		// Roll back the disk write so /etc/projects and exfs.projects stay in sync.
		if err := writeEXFSProjects(exfs.projects); err != nil {
			log.WithError(err).Error("failed to roll back exfs projects after enableEXFSQuota failure")
		}
		return
	}

	exfs.projects = projects
	return
}

// removeProject drops a specified project from the
// if the project is already missing will return with no error
func (q exfsProject) removeProject() (err error) {
	exfs.Lock()
	defer exfs.Unlock()

	projects := make([]exfsProject, 0, len(exfs.projects))
	for _, project := range exfs.projects {
		if project.Name != q.Name {
			projects = append(projects, project)
		}
	}

	if err = writeEXFSProjects(projects); err != nil {
		return err
	}

	exfs.projects = projects

	return
}

func getProject(serverUUID string) (serverProject exfsProject, err error) {
	exfs.Lock()
	defer exfs.Unlock()
	for _, project := range exfs.projects {
		if project.Name == serverUUID {
			return project, nil
		}
	}

	return serverProject, errors.New("quota for server doesn't exist")
}

// writeEXFSProjects
func writeEXFSProjects(projects []exfsProject) (err error) {
	if err = writeTemplate(projidTemplate, projidFile, projects); err != nil {
		return err
	}
	if err = writeTemplate(projectsTemplate, projectFile, projects); err != nil {
		return err
	}
	return
}

// writeTemplate renders t into file atomically: it writes to a temp file in
// the same directory, fsyncs, and renames over the target. On any failure the
// temp file is removed and the original file is left untouched.
func writeTemplate(t *template.Template, file string, data interface{}) (err error) {
	dir := filepath.Dir(file)
	tmp, err := os.CreateTemp(dir, filepath.Base(file)+".tmp.*")
	if err != nil {
		return err
	}

	tmpName := tmp.Name()

	defer func() {
		if err != nil {
			os.Remove(tmpName)
		}
	}()

	if err = t.Execute(tmp, data); err != nil {
		tmp.Close()
		return err
	}

	if err = tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}

	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}

	if err = tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmpName, file)
}
