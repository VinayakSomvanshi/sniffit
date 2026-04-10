package webhook

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sTypes "k8s.io/apimachinery/pkg/types"
)

type Mutator struct {
	ControlPlaneURL string
}

func NewMutator(controlPlaneURL string) *Mutator {
	return &Mutator{
		ControlPlaneURL: controlPlaneURL,
	}
}

func (m *Mutator) HandleMutate(w http.ResponseWriter, r *http.Request) {
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Printf("could not read webhook body: %v", err)
		http.Error(w, "could not read request body", http.StatusBadRequest)
		return
	}

	var admissionReviewReq admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &admissionReviewReq); err != nil {
		log.Printf("could not deserialize admission request: %v", err)
		http.Error(w, fmt.Sprintf("could not deserialize request: %v", err), http.StatusBadRequest)
		return
	}

	if admissionReviewReq.Request == nil {
		http.Error(w, "admission request is empty", http.StatusBadRequest)
		return
	}

	var pod corev1.Pod
	if err := json.Unmarshal(admissionReviewReq.Request.Object.Raw, &pod); err != nil {
		log.Printf("could not unmarshal pod: %v", err)
		http.Error(w, fmt.Sprintf("could not unmarshal pod: %v", err), http.StatusBadRequest)
		return
	}

	// Check if we actually need to inject
	if _, ok := pod.Labels["sniffit/inject"]; !ok {
		sendResponse(w, admissionReviewReq.Request.UID, true, nil)
		return
	}

	// Check if already injected
	for _, c := range pod.Spec.Containers {
		if c.Name == "sniffit-sidecar" {
			sendResponse(w, admissionReviewReq.Request.UID, true, nil)
			return
		}
	}

	// Create JSON patches to add the sidecar container
	patch := `[
		{
			"op": "add",
			"path": "/spec/containers/-",
			"value": {
				"name": "sniffit-sidecar",
				"image": "vinayak/sniffit:latest",
				"securityContext": {
					"capabilities": {
						"add": ["NET_RAW", "NET_ADMIN"]
					}
				},
				"env": [
					{
						"name": "CONTROL_PLANE_URL",
						"value": "` + m.ControlPlaneURL + `"
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
		log.Printf("could not marshal response: %v", err)
		http.Error(w, "could not marshal response", http.StatusInternalServerError)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
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
