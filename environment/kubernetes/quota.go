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
	quota, err := buildResourceQuota(ns, &cfg.Kubernetes.ResourceQuota)
	if err != nil {
		return err
	}

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
	lr, err := buildLimitRange(ns, &cfg.Kubernetes.LimitRange)
	if err != nil {
		return err
	}

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

// setQuantity parses a quantity string into the resource list under key when
// the value is non-empty. Unlike resource.MustParse it returns an error for
// invalid (user-configurable) values instead of panicking the process.
func setQuantity(list corev1.ResourceList, key corev1.ResourceName, field, value string) error {
	if value == "" {
		return nil
	}
	q, err := resource.ParseQuantity(value)
	if err != nil {
		return errors.Wrapf(err, "environment/kubernetes: invalid quantity %q for %s", value, field)
	}
	list[key] = q
	return nil
}

// buildResourceQuota constructs the ResourceQuota spec from config values.
func buildResourceQuota(namespace string, cfg *config.KubeResourceQuota) (*corev1.ResourceQuota, error) {
	hard := corev1.ResourceList{}

	for _, q := range []struct {
		key   corev1.ResourceName
		field string
		value string
	}{
		{corev1.ResourceLimitsCPU, "cpu_limit", cfg.CPULimit},
		{corev1.ResourceLimitsMemory, "memory_limit", cfg.MemoryLimit},
		{corev1.ResourceRequestsCPU, "cpu_request", cfg.CPURequest},
		{corev1.ResourceRequestsMemory, "memory_request", cfg.MemoryRequest},
		{corev1.ResourceRequestsStorage, "max_storage", cfg.MaxStorage},
	} {
		if err := setQuantity(hard, q.key, q.field, q.value); err != nil {
			return nil, err
		}
	}
	if cfg.MaxPods > 0 {
		hard[corev1.ResourcePods] = *resource.NewQuantity(cfg.MaxPods, resource.DecimalSI)
	}
	if cfg.MaxPVCs > 0 {
		hard[corev1.ResourcePersistentVolumeClaims] = *resource.NewQuantity(cfg.MaxPVCs, resource.DecimalSI)
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
	}, nil
}

// buildLimitRange constructs the LimitRange spec from config values.
func buildLimitRange(namespace string, cfg *config.KubeLimitRange) (*corev1.LimitRange, error) {
	containerLimit := corev1.LimitRangeItem{
		Type:           corev1.LimitTypeContainer,
		Default:        corev1.ResourceList{},
		DefaultRequest: corev1.ResourceList{},
		Max:            corev1.ResourceList{},
	}

	for _, q := range []struct {
		list  corev1.ResourceList
		key   corev1.ResourceName
		field string
		value string
	}{
		{containerLimit.Default, corev1.ResourceCPU, "default_cpu_limit", cfg.DefaultCPULimit},
		{containerLimit.Default, corev1.ResourceMemory, "default_memory_limit", cfg.DefaultMemoryLimit},
		{containerLimit.DefaultRequest, corev1.ResourceCPU, "default_cpu_request", cfg.DefaultCPURequest},
		{containerLimit.DefaultRequest, corev1.ResourceMemory, "default_memory_request", cfg.DefaultMemoryRequest},
		{containerLimit.Max, corev1.ResourceCPU, "max_cpu", cfg.MaxCPU},
		{containerLimit.Max, corev1.ResourceMemory, "max_memory", cfg.MaxMemory},
	} {
		if err := setQuantity(q.list, q.key, q.field, q.value); err != nil {
			return nil, err
		}
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
	}, nil
}
