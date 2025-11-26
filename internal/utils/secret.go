// Package utils contains helpers used by the controller.
package utils

import corev1 "k8s.io/api/core/v1"

// IsAuthKeyGenerationNeeded returns true if the Secret lacks a non-empty authkey.
func IsAuthKeyGenerationNeeded(existing *corev1.Secret) bool {
	if existing != nil && existing.Data != nil {
		if v, ok := existing.Data["authkey"]; ok && len(v) > 0 {
			return false
		}
	}
	return true
}
