package kubernetes

import (
	"context"

	"emperror.dev/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/pelican-dev/wings/config"
)

const (
	quotaName      = "pelican-wings"
	limitRangeName = "pelican-wings"
)

// EnsureResourceQuota creates or updates the ResourceQuota in the game server
// namespace. If resource_quota is not enabled in config, this is a no-op.
func (e *Environment) EnsureResourceQuota(ctx context.Context) error {
	cfg := config.Get()
	if !cfg.Kubernetes.ResourceQuota.Enabled {
		return nil
	}

	ns := e.namespace()
	quota := buildResourceQuota(ns, &cfg.Kubernetes.ResourceQuota)

	existing, err := e.client.CoreV1().ResourceQuotas(ns).Get(ctx, quotaName, metav1.GetOptions{})
	if err == nil {
		// Update existing.
		existing.Spec = quota.Spec
		_, err = e.client.CoreV1().ResourceQuotas(ns).Update(ctx, existing, metav1.UpdateOptions{})
		if err != nil {
			return errors.Wrap(err, "environment/kubernetes: failed to update ResourceQuota")
		}
		return nil
	}

	_, err = e.client.CoreV1().ResourceQuotas(ns).Create(ctx, quota, metav1.CreateOptions{})
	if err != nil {
		return errors.Wrap(err, "environment/kubernetes: failed to create ResourceQuota")
	}

	e.log().Info("created ResourceQuota for namespace")
	return nil
}

// EnsureLimitRange creates or updates the LimitRange in the game server
// namespace. If limit_range is not enabled in config, this is a no-op.
func (e *Environment) EnsureLimitRange(ctx context.Context) error {
	cfg := config.Get()
	if !cfg.Kubernetes.LimitRange.Enabled {
		return nil
	}

	ns := e.namespace()
	lr := buildLimitRange(ns, &cfg.Kubernetes.LimitRange)

	existing, err := e.client.CoreV1().LimitRanges(ns).Get(ctx, limitRangeName, metav1.GetOptions{})
	if err == nil {
		// Update existing.
		existing.Spec = lr.Spec
		_, err = e.client.CoreV1().LimitRanges(ns).Update(ctx, existing, metav1.UpdateOptions{})
		if err != nil {
			return errors.Wrap(err, "environment/kubernetes: failed to update LimitRange")
		}
		return nil
	}

	_, err = e.client.CoreV1().LimitRanges(ns).Create(ctx, lr, metav1.CreateOptions{})
	if err != nil {
		return errors.Wrap(err, "environment/kubernetes: failed to create LimitRange")
	}

	e.log().Info("created LimitRange for namespace")
	return nil
}

// buildResourceQuota constructs the ResourceQuota spec from config values.
func buildResourceQuota(namespace string, cfg *config.KubeResourceQuota) *corev1.ResourceQuota {
	hard := corev1.ResourceList{}

	if cfg.CPULimit != "" {
		hard[corev1.ResourceLimitsCPU] = resource.MustParse(cfg.CPULimit)
	}
	if cfg.MemoryLimit != "" {
		hard[corev1.ResourceLimitsMemory] = resource.MustParse(cfg.MemoryLimit)
	}
	if cfg.CPURequest != "" {
		hard[corev1.ResourceRequestsCPU] = resource.MustParse(cfg.CPURequest)
	}
	if cfg.MemoryRequest != "" {
		hard[corev1.ResourceRequestsMemory] = resource.MustParse(cfg.MemoryRequest)
	}
	if cfg.MaxPods > 0 {
		hard[corev1.ResourcePods] = *resource.NewQuantity(cfg.MaxPods, resource.DecimalSI)
	}
	if cfg.MaxPVCs > 0 {
		hard[corev1.ResourcePersistentVolumeClaims] = *resource.NewQuantity(cfg.MaxPVCs, resource.DecimalSI)
	}
	if cfg.MaxStorage != "" {
		hard[corev1.ResourceRequestsStorage] = resource.MustParse(cfg.MaxStorage)
	}

	return &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:      quotaName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "pelican-wings",
			},
		},
		Spec: corev1.ResourceQuotaSpec{
			Hard: hard,
		},
	}
}

// buildLimitRange constructs the LimitRange spec from config values.
func buildLimitRange(namespace string, cfg *config.KubeLimitRange) *corev1.LimitRange {
	containerLimit := corev1.LimitRangeItem{
		Type:           corev1.LimitTypeContainer,
		Default:        corev1.ResourceList{},
		DefaultRequest: corev1.ResourceList{},
		Max:            corev1.ResourceList{},
	}

	if cfg.DefaultCPULimit != "" {
		containerLimit.Default[corev1.ResourceCPU] = resource.MustParse(cfg.DefaultCPULimit)
	}
	if cfg.DefaultMemoryLimit != "" {
		containerLimit.Default[corev1.ResourceMemory] = resource.MustParse(cfg.DefaultMemoryLimit)
	}
	if cfg.DefaultCPURequest != "" {
		containerLimit.DefaultRequest[corev1.ResourceCPU] = resource.MustParse(cfg.DefaultCPURequest)
	}
	if cfg.DefaultMemoryRequest != "" {
		containerLimit.DefaultRequest[corev1.ResourceMemory] = resource.MustParse(cfg.DefaultMemoryRequest)
	}
	if cfg.MaxCPU != "" {
		containerLimit.Max[corev1.ResourceCPU] = resource.MustParse(cfg.MaxCPU)
	}
	if cfg.MaxMemory != "" {
		containerLimit.Max[corev1.ResourceMemory] = resource.MustParse(cfg.MaxMemory)
	}

	return &corev1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{
			Name:      limitRangeName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "pelican-wings",
			},
		},
		Spec: corev1.LimitRangeSpec{
			Limits: []corev1.LimitRangeItem{containerLimit},
		},
	}
}
