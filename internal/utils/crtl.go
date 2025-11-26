package utils

import ctrl "sigs.k8s.io/controller-runtime"

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
