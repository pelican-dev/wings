package kubernetes

import (
	"context"
	"os"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/pelican-dev/wings/config"
)

// GetNodeIPs returns the IP addresses of the Kubernetes node where Wings is
// running. It queries the node's status.addresses for ExternalIP and
// InternalIP entries. The node name is read from the config or the NODE_NAME
// environment variable (set via the Kubernetes downward API).
func GetNodeIPs(ctx context.Context) ([]string, error) {
	nodeName := config.Get().Kubernetes.NodeName
	if nodeName == "" {
		nodeName = os.Getenv("NODE_NAME")
	}
	if nodeName == "" {
		return nil, nil
	}

	c, err := Client()
	if err != nil {
		return nil, err
	}

	node, err := c.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	var ips []string
	for _, addr := range node.Status.Addresses {
		if addr.Type == corev1.NodeExternalIP || addr.Type == corev1.NodeInternalIP {
			ips = append(ips, addr.Address)
		}
	}
	return ips, nil
}

// GetLoadBalancerIPs returns the external IPs assigned to all game server
// LoadBalancer Services in the configured namespace. This is used by the
// allocation endpoint to show available IPs when in LoadBalancer mode.
func GetLoadBalancerIPs(ctx context.Context) ([]string, error) {
	cfg := config.Get()
	c, err := Client()
	if err != nil {
		return nil, err
	}

	svcs, err := c.CoreV1().Services(cfg.Kubernetes.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/managed-by=pelican-wings,pelican.dev/resource-type=service",
	})
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var ips []string
	for _, svc := range svcs.Items {
		for _, ingress := range svc.Status.LoadBalancer.Ingress {
			addr := ingress.IP
			if addr == "" {
				addr = ingress.Hostname
			}
			if addr != "" && !seen[addr] {
				seen[addr] = true
				ips = append(ips, addr)
			}
		}
	}
	return ips, nil
}
