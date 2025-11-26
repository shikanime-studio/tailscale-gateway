// Package utils contains helpers used by the controller.
package utils

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// SetGatewayAcceptedCondition sets the Gateway Accepted condition.
func SetGatewayAcceptedCondition(
	gw *gatewayv1.Gateway,
	status metav1.ConditionStatus,
	reason gatewayv1.GatewayConditionReason,
	message string,
) bool {
	accepted := metav1.Condition{
		Type:               string(gatewayv1.GatewayConditionAccepted),
		Status:             status,
		ObservedGeneration: gw.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             string(reason),
		Message:            message,
	}
	return meta.SetStatusCondition(&gw.Status.Conditions, accepted)
}

// SetGatewayProgrammedCondition sets the Gateway Programmed condition.
func SetGatewayProgrammedCondition(
	gw *gatewayv1.Gateway,
	status metav1.ConditionStatus,
	reason gatewayv1.GatewayConditionReason,
	message string,
) bool {
	programmed := metav1.Condition{
		Type:               string(gatewayv1.GatewayConditionProgrammed),
		Status:             status,
		ObservedGeneration: gw.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             string(reason),
		Message:            message,
	}
	return meta.SetStatusCondition(&gw.Status.Conditions, programmed)
}

// SetListenerAcceptedCondition sets the Listener Accepted condition.
func SetListenerAcceptedCondition(
	ls *gatewayv1.ListenerStatus,
	gw *gatewayv1.Gateway,
	status metav1.ConditionStatus,
	reason gatewayv1.ListenerConditionReason,
	message string,
) bool {
	accepted := metav1.Condition{
		Type:               string(gatewayv1.ListenerConditionAccepted),
		Status:             status,
		ObservedGeneration: gw.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             string(reason),
		Message:            message,
	}
	return meta.SetStatusCondition(&ls.Conditions, accepted)
}

// SetListenerProgrammedCondition sets the Listener Programmed condition.
func SetListenerProgrammedCondition(
	ls *gatewayv1.ListenerStatus,
	gw *gatewayv1.Gateway,
	status metav1.ConditionStatus,
	reason gatewayv1.ListenerConditionReason,
	message string,
) bool {
	programmed := metav1.Condition{
		Type:               string(gatewayv1.ListenerConditionProgrammed),
		Status:             status,
		ObservedGeneration: gw.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             string(reason),
		Message:            message,
	}
	return meta.SetStatusCondition(&ls.Conditions, programmed)
}
