// Package utils contains helpers used by the controller.
package utils

import ctrl "sigs.k8s.io/controller-runtime"

// JoinResults merges multiple controller results into a single result.
func JoinResults(results ...ctrl.Result) ctrl.Result {
	var res ctrl.Result
	for _, r := range results {
		if r.Requeue {
			res.Requeue = true
		}
		if r.RequeueAfter > 0 {
			res.RequeueAfter = r.RequeueAfter
		}
	}
	return res
}
