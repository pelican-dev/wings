package kubernetes

import (
	"sync"

	"emperror.dev/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/pelican-dev/wings/config"
)

var (
	_konce  sync.Once
	_client kubernetes.Interface
)

// Client returns a shared Kubernetes clientset. The client is created once
// and reused for all subsequent calls. It uses in-cluster config when no
// kubeconfig path is specified, falling back to the provided kubeconfig file.
func Client() (kubernetes.Interface, error) {
	var err error
	_konce.Do(func() {
		var cfg *rest.Config
		kubeconfig := config.Get().Kubernetes.Kubeconfig
		if kubeconfig != "" {
			cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		} else {
			cfg, err = rest.InClusterConfig()
		}
		if err != nil {
			err = errors.Wrap(err, "environment/kubernetes: failed to build config")
			return
		}
		_client, err = kubernetes.NewForConfig(cfg)
		if err != nil {
			err = errors.Wrap(err, "environment/kubernetes: failed to create clientset")
		}
	})
	return _client, err
}
