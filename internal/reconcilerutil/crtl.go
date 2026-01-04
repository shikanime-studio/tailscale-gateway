// Package reconcilerutil contains helpers used by the reconciler.
package reconcilerutil

import ctrl "sigs.k8s.io/controller-runtime"

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
