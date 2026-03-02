package quotas

import (
	"fmt"
	"html/template"
	"os"
	"strings"
	"sync"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/parkervcp/fsquota"
	"github.com/pelican-dev/wings/config"
)

var exfs struct {
	projects []exfsProject
	lock     sync.Mutex
}

type exfsProject struct {
	ID       int
	Name     string
	BasePath string
}

const (
	projidTemplate = `{{ range . }}{{ .Name }}:{{ .ID }}
{{ end }}`
	projectsTemplate = `{{ range . }}{{ .ID }}:{{ .BasePath }}/{{ .Name }}
{{ end }}`

	projidFile  = `/etc/projid`
	projectFile = `/etc/projects`
)

// setQuota sets the quota in bytes for the specified server uuid
func (q exfsProject) setQuota(byteLimit uint64) (err error) {
	log.WithFields(log.Fields{"server_path": fmt.Sprintf("%s/%s", q.BasePath, q.Name), "limit_bytes": byteLimit}).Debug("setting quota")
	serverProject, err := fsquota.LookupProject(q.Name)
	if err != nil {
		return
	}

	serverDirPath := fmt.Sprintf("%s/%s", config.Get().System.Data, q.Name)
	limits := fsquota.Limits{}

	limits.Bytes.SetHard(byteLimit)

	if _, err = fsquota.SetProjectQuota(serverDirPath, serverProject, limits); err != nil {
		return
	}

	log.WithField("server_path", fmt.Sprintf("%s/%s", q.BasePath, q.Name)).Debug("quota set")
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
	serverDir, err := os.Open(fmt.Sprintf("%s/%s", q.BasePath, q.Name))
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
	q.BasePath = config.Get().System.Data
	if strings.HasSuffix(q.BasePath, "/") {
		q.BasePath = strings.TrimSuffix(config.Get().System.Data, "/")
	}
	exfs.lock.Lock()
	defer exfs.lock.Unlock()
	exfs.projects = append(exfs.projects, q)

	if err = writeEXFSProjects(); err != nil {
		log.WithError(err).Error("failed to write exfs projects")
		return
	}

	if err = q.enableEXFSQuota(); err != nil {
		log.WithError(err).Error("failed to enable quota")
		return
	}

	return
}

// removeProject drops a specified project from the
func (q exfsProject) removeProject() (err error) {
	exfs.lock.Lock()
	defer exfs.lock.Unlock()
	for pos, project := range exfs.projects {
		if project.Name == q.Name {
			exfs.projects = append(exfs.projects[:pos], exfs.projects[pos+1:]...)
			break
		}
	}

	err = writeEXFSProjects()
	return
}

func getProject(serverUUID string) (serverProject exfsProject, err error) {
	exfs.lock.Lock()
	defer exfs.lock.Unlock()
	for _, project := range exfs.projects {
		if project.Name == serverUUID {
			return project, nil
		}
	}

	return serverProject, errors.New("quota for server doesn't exist")
}

func writeEXFSProjects() (err error) {
	exfs.lock.Lock()
	defer exfs.lock.Unlock()
	// write out projid file
	idtmpl, err := template.New("projid").Parse(projidTemplate)
	if err != nil {
		return err
	}

	if err = writeTemplate(idtmpl, projidFile, exfs.projects); err != nil {
		return
	}

	projtmpl, err := template.New("projects").Parse(projectsTemplate)
	if err != nil {
		return err
	}

	if err = writeTemplate(projtmpl, projectFile, exfs.projects); err != nil {
		return
	}

	return
}

func writeTemplate(t *template.Template, file string, data interface{}) (err error) {
	f, err := os.Create(file)
	if err != nil {
		return err
	}

	defer f.Close()

	err = t.Execute(f, data)
	if err != nil {
		return err
	}

	return nil
}
