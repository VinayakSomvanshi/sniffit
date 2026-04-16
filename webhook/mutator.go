package webhook

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sTypes "k8s.io/apimachinery/pkg/types"
)

// Mutator handles sidecar injection logic.
type Mutator struct {
	ControlPlaneURL    string
	InsecureSkipVerify bool
	// NamespaceLevelInjection enables Istio-style injection: all pods in namespaces
	// labeled sniffit/inject=true get the sidecar without needing a per-pod label.
	NamespaceLevelInjection bool
}

// NewMutator creates a Mutator. insecureSkipVerify controls TLS verification for
// the probe→control-plane HTTPS call.
func NewMutator(controlPlaneURL string, insecureSkipVerify, namespaceLevelInjection bool) *Mutator {
	return &Mutator{
		ControlPlaneURL:           controlPlaneURL,
		InsecureSkipVerify:        insecureSkipVerify,
		NamespaceLevelInjection:  namespaceLevelInjection,
	}
}

// shouldSkipNamespace returns true for system namespaces where injection must not occur.
func shouldSkipNamespace(ns string) bool {
	switch ns {
	case "kube-system", "kube-public", "kube-node-lease", "sniffit":
		return true
	}
	return false
}

// needsInjection returns true when a pod should receive the sidecar based on the
// injection mode (namespace-level or per-pod label).
func (m *Mutator) needsInjection(pod *corev1.Pod) bool {
	// Respect existing sidecar (idempotent — webhook fires on every CREATE)
	for _, c := range pod.Spec.Containers {
		if c.Name == "sniffit-sidecar" {
			return false // already injected
		}
	}

	// Always-enforced guard: never inject into system namespaces
	if shouldSkipNamespace(pod.Namespace) {
		return false
	}

	// Per-pod label always works
	if _, ok := pod.Labels["sniffit/inject"]; ok {
		return true
	}

	// Namespace-level mode: check the namespace label
	if m.NamespaceLevelInjection {
		// Note: namespace labels are not available in the Pod object directly.
		// The webhook receives the Pod; we need the namespace label.
		// We check pod.Namespace as a fallback label on the pod itself (rarely set),
		// but the proper mechanism relies on namespaceSelector in the webhook config.
		// Here we just return true — the namespaceSelector in the webhook handles it.
		return true
	}

	return false
}

func (m *Mutator) HandleMutate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		fmt.Printf("could not read webhook body: %v", err)
		http.Error(w, "could not read request body", http.StatusBadRequest)
		return
	}

	var admissionReviewReq admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &admissionReviewReq); err != nil {
		fmt.Printf("could not deserialize admission request: %v", err)
		http.Error(w, fmt.Sprintf("could not deserialize request: %v", err), http.StatusBadRequest)
		return
	}

	if admissionReviewReq.Request == nil {
		http.Error(w, "admission request is empty", http.StatusBadRequest)
		return
	}

	var pod corev1.Pod
	if err := json.Unmarshal(admissionReviewReq.Request.Object.Raw, &pod); err != nil {
		fmt.Printf("could not unmarshal pod: %v", err)
		http.Error(w, fmt.Sprintf("could not unmarshal pod: %v", err), http.StatusBadRequest)
		return
	}

	// Decide if this pod needs injection (respects both per-pod and namespace-level modes)
	if !m.needsInjection(&pod) {
		sendResponse(w, admissionReviewReq.Request.UID, true, nil)
		return
	}

	// Properly escape values for the JSON patch
	cpURL, _ := json.Marshal(m.ControlPlaneURL)
	insecureSkip := "false"
	if m.InsecureSkipVerify {
		insecureSkip = "true"
	}

	// Create JSON patches to add the sidecar container
	patch := `[
		{
			"op": "add",
			"path": "/spec/containers/-",
			"value": {
				"name": "sniffit-sidecar",
				"image": "vinzyzk/sniffit-probe:latest",
				"imagePullPolicy": "Always",
				"securityContext": {
					"capabilities": {
						"add": ["NET_RAW", "NET_ADMIN"]
					},
					"runAsUser": 0,
					"runAsNonRoot": false,
					"allowPrivilegeEscalation": true
				},
				"args": ["-iface=eth0,lo"],
				"env": [
					{
						"name": "LOG_LEVEL",
						"value": "info"
					},
					{
						"name": "CONTROL_PLANE_URL",
						"value": ` + string(cpURL) + `
					},
					{
						"name": "TENANT_ID",
						"value": "` + getTenantID(pod) + `"
					},
					{
						"name": "API_KEY",
						"value": "sniffit_379582a513e97184f7711c3b71c96689"
					},
					{
						"name": "INSECURE_SKIP_VERIFY",
						"value": "` + insecureSkip + `"
					},
					{
						"name": "POD_NAME",
						"valueFrom": {
							"fieldRef": {
								"fieldPath": "metadata.name"
							}
						}
					},
					{
						"name": "POD_NAMESPACE",
						"valueFrom": {
							"fieldRef": {
								"fieldPath": "metadata.namespace"
							}
						}
					}
				]
			}
		}
	]`

	pt := admissionv1.PatchTypeJSONPatch
	admissionResponse := &admissionv1.AdmissionResponse{
		UID:     admissionReviewReq.Request.UID,
		Allowed: true,
		Patch:   []byte(patch),
		PatchType: &pt,
	}

	admissionReview := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AdmissionReview",
			APIVersion: "admission.k8s.io/v1",
		},
		Response: admissionResponse,
	}

	resp, err := json.Marshal(admissionReview)
	if err != nil {
		fmt.Printf("could not marshal response: %v", err)
		http.Error(w, "could not marshal response", http.StatusInternalServerError)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}

func getTenantID(pod corev1.Pod) string {
	if tid, ok := pod.Labels["sniffit.tenant_id"]; ok {
		return tid
	}
	return "default"
}

func sendResponse(w http.ResponseWriter, uid k8sTypes.UID, allowed bool, patch []byte) {
	resp := admissionv1.AdmissionResponse{
		UID:     uid,
		Allowed: allowed,
	}
	// For simple response without patch
	admissionReview := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AdmissionReview",
			APIVersion: "admission.k8s.io/v1",
		},
		Response: &resp,
	}
	bytes, _ := json.Marshal(admissionReview)
	w.Header().Set("Content-Type", "application/json")
	w.Write(bytes)
}

// NOTE: Usually read from k8s client, imported here for decoding only
// The proper meta deseralizer requires k8s.io/apimachinery/pkg/runtime/serializer
