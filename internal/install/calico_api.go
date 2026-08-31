package install

import (
	"context"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
)

const (
	calicoProjectAPIService = "v3.projectcalico.org"
	calicoAPIPollInterval   = 2 * time.Second
)

// isRetryableCalicoAPIError reports whether err is a transient failure while the
// Calico operator / calico-apiserver / APIService are still coming up.
//
// Typical failure from a cold install on Multipass:
//
//	felixconfigurations.projectcalico.org "default" ... currently unable to handle the request
//
// That is ServiceUnavailable from the aggregated API, not a permanent NotFound.
func isRetryableCalicoAPIError(err error) bool {
	if err == nil {
		return false
	}
	switch {
	case apierrors.IsNotFound(err),
		apierrors.IsServiceUnavailable(err),
		apierrors.IsTimeout(err),
		apierrors.IsServerTimeout(err),
		apierrors.IsTooManyRequests(err),
		apierrors.IsConflict(err): // concurrent create/reconcile by the operator
		return true
	}

	msg := strings.ToLower(err.Error())
	// calico-apiserver can report APIService Available before it authenticates
	// the k3s admin kubeconfig (extension-apiserver-authentication). The client
	// is seen as system:anonymous until that wiring finishes — retry, do not fail.
	if apierrors.IsForbidden(err) && strings.Contains(msg, "system:anonymous") {
		return true
	}

	transientSnippets := []string{
		"currently unable to handle the request",
		"no endpoints available for service",
		"service unavailable",
		"connection refused",
		"connection reset",
		"i/o timeout",
		"tls handshake timeout",
		"temporary failure",
		"try again",
		"system:anonymous",
	}
	for _, s := range transientSnippets {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

func (c *CalicoInstaller) dynamicClient() (dynamic.Interface, error) {
	return dynamic.NewForConfig(c.k8sClient.GetConfig())
}

// waitForCalicoAPIService waits until the projectcalico.org aggregated API is Available.
// FelixConfiguration (and other projectcalico.org/v3 resources) are served by calico-apiserver;
// patching them before this returns is a race.
func (c *CalicoInstaller) waitForCalicoAPIService(ctx context.Context, timeout time.Duration) error {
	client, err := c.dynamicClient()
	if err != nil {
		return fmt.Errorf("create dynamic client: %w", err)
	}

	dr := client.Resource(schema.GroupVersionResource{
		Group:    "apiregistration.k8s.io",
		Version:  "v1",
		Resource: "apiservices",
	})

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error
	err = wait.PollUntilContextTimeout(waitCtx, calicoAPIPollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		obj, getErr := dr.Get(ctx, calicoProjectAPIService, metav1.GetOptions{})
		if getErr != nil {
			lastErr = getErr
			if isRetryableCalicoAPIError(getErr) {
				return false, nil
			}
			return false, getErr
		}
		if apiServiceAvailable(obj) {
			lastErr = nil
			return true, nil
		}
		lastErr = fmt.Errorf("APIService %s is not Available yet", calicoProjectAPIService)
		return false, nil
	})
	if err != nil {
		if lastErr != nil {
			return fmt.Errorf("calico aggregated API %s not ready within %s: %w", calicoProjectAPIService, timeout, lastErr)
		}
		return fmt.Errorf("calico aggregated API %s not ready within %s: %w", calicoProjectAPIService, timeout, err)
	}
	c.logger.Infof("✓ Calico aggregated API %s is Available", calicoProjectAPIService)
	return nil
}

func apiServiceAvailable(obj *unstructured.Unstructured) bool {
	if obj == nil {
		return false
	}
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}
	for _, raw := range conditions {
		cond, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if cond["type"] == "Available" && strings.EqualFold(fmt.Sprint(cond["status"]), "True") {
			return true
		}
	}
	return false
}

// waitAndPatchDynamic waits for name to be gettable, then applies a merge patch.
// Both Get and Patch retry on transient aggregation / NotFound / conflict errors for
// the full timeout (aligned with Calico rollout, not a short fixed attempt count).
func (c *CalicoInstaller) waitAndPatchDynamic(
	ctx context.Context,
	gvr schema.GroupVersionResource,
	name string,
	patchBytes []byte,
	timeout time.Duration,
	resourceLabel string,
) error {
	client, err := c.dynamicClient()
	if err != nil {
		return fmt.Errorf("create dynamic client: %w", err)
	}
	dr := client.Resource(gvr)

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error
	err = wait.PollUntilContextTimeout(waitCtx, calicoAPIPollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		if _, getErr := dr.Get(ctx, name, metav1.GetOptions{}); getErr != nil {
			lastErr = getErr
			if isRetryableCalicoAPIError(getErr) {
				c.logger.Debugf("Waiting for %s %q (get: %v)", resourceLabel, name, getErr)
				return false, nil
			}
			return false, fmt.Errorf("get %s %q: %w", resourceLabel, name, getErr)
		}

		if _, patchErr := dr.Patch(ctx, name, types.MergePatchType, patchBytes, metav1.PatchOptions{}); patchErr != nil {
			lastErr = patchErr
			if isRetryableCalicoAPIError(patchErr) {
				c.logger.Debugf("Retrying patch of %s %q (patch: %v)", resourceLabel, name, patchErr)
				return false, nil
			}
			return false, fmt.Errorf("patch %s %q: %w", resourceLabel, name, patchErr)
		}
		lastErr = nil
		return true, nil
	})
	if err != nil {
		if lastErr != nil {
			return fmt.Errorf("%s %q not patchable within %s: %w", resourceLabel, name, timeout, lastErr)
		}
		return fmt.Errorf("%s %q not patchable within %s: %w", resourceLabel, name, timeout, err)
	}
	return nil
}
