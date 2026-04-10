package webhook

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestMutator_HandleMutate_Injected(t *testing.T) {
	mutator := NewMutator("http://control-plane:8080")

	// 1. Create a pod that needs injection
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-pod",
			Labels: map[string]string{"sniffit/inject": "true"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Image: "app:latest"}},
		},
	}
	podRaw, _ := json.Marshal(pod)

	// 2. Create AdmissionReview request
	ar := admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			UID: "test-uid",
			Object: runtime.RawExtension{
				Raw: podRaw,
			},
		},
	}
	arBody, _ := json.Marshal(ar)

	// 3. Test HTTP handler
	req := httptest.NewRequest("POST", "/mutate", bytes.NewReader(arBody))
	rr := httptest.NewRecorder()
	
	mutator.HandleMutate(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", rr.Code)
	}

	var respAr admissionv1.AdmissionReview
	json.Unmarshal(rr.Body.Bytes(), &respAr)

	if !respAr.Response.Allowed {
		t.Error("Expected Allowed: true")
	}
	if len(respAr.Response.Patch) == 0 {
		t.Error("Expected a patch for pod with inject label")
	}
	if !strings.Contains(string(respAr.Response.Patch), "sniffit-sidecar") {
		t.Error("Patch should contain sniffit-sidecar")
	}
}

func TestMutator_HandleMutate_Skip(t *testing.T) {
	mutator := NewMutator("http://control-plane:8080")

	// Pod WITHOUT label
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "safe-pod"},
	}
	podRaw, _ := json.Marshal(pod)

	ar := admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			UID: "skip-uid",
			Object: runtime.RawExtension{Raw: podRaw},
		},
	}
	arBody, _ := json.Marshal(ar)

	req := httptest.NewRequest("POST", "/mutate", bytes.NewReader(arBody))
	rr := httptest.NewRecorder()
	
	mutator.HandleMutate(rr, req)

	var respAr admissionv1.AdmissionReview
	json.Unmarshal(rr.Body.Bytes(), &respAr)

	if len(respAr.Response.Patch) != 0 {
		t.Error("Expected NO patch for pod without inject label")
	}
}
