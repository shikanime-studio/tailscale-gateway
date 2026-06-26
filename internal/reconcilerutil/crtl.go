// Package reconcilerutil contains helpers used by the reconciler.
package reconcilerutil

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/shikanime-studio/tailscale-gateway/internal/tailscale"
)

// CleanupDevice deletes the Tailscale device associated with a Secret, if present.
func CleanupDevice(
	ctx context.Context,
	kube kubernetes.Interface,
	ts tailscale.Interface,
	name,
	namespace string,
) error {
	sec, err := kube.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get secret: %w", err)
	}
	if sec.Data == nil {
		return nil
	}
	b, ok := sec.Data["device_id"]
	if !ok {
		return nil
	}
	devID := string(b)
	if devID == "" {
		return nil
	}
	if err = ts.DeleteDevice(ctx, devID); err != nil {
		return fmt.Errorf("failed to delete device: %w", err)
	}
	return nil
}

// JoinResults merges multiple controller results into a single result.
func JoinResults(results ...ctrl.Result) ctrl.Result {
	var res ctrl.Result
	for _, r := range results {
		if r.RequeueAfter > 0 {
			res.RequeueAfter = r.RequeueAfter
		}
	}
	return res
}
