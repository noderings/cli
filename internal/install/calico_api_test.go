package install

import (
	"errors"
	"fmt"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestIsRetryableCalicoAPIError(t *testing.T) {
	t.Parallel()

	notFound := apierrors.NewNotFound(schema.GroupResource{Group: "projectcalico.org", Resource: "felixconfigurations"}, "default")
	unavailable := apierrors.NewServiceUnavailable("the server is currently unable to handle the request")
	conflict := apierrors.NewConflict(schema.GroupResource{Group: "projectcalico.org", Resource: "felixconfigurations"}, "default", errors.New("conflict"))
	forbidden := apierrors.NewForbidden(schema.GroupResource{Group: "projectcalico.org", Resource: "felixconfigurations"}, "default", errors.New("denied"))
	forbiddenAnonymous := apierrors.NewForbidden(
		schema.GroupResource{Group: "projectcalico.org", Resource: "felixconfigurations"},
		"default",
		errors.New(`User "system:anonymous" cannot get resource "felixconfigurations"`),
	)

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "not found", err: notFound, want: true},
		{name: "service unavailable typed", err: unavailable, want: true},
		{name: "conflict", err: conflict, want: true},
		{name: "forbidden", err: forbidden, want: false},
		{name: "anonymous forbidden", err: forbiddenAnonymous, want: true},
		{
			name: "aggregation string",
			err:  fmt.Errorf(`felixconfigurations.projectcalico.org "default" is invalid: the server is currently unable to handle the request`),
			want: true,
		},
		{
			name: "no endpoints",
			err:  errors.New("no endpoints available for service \"calico-api\""),
			want: true,
		},
		{
			name: "connection refused",
			err:  errors.New("Get \"https://127.0.0.1:6443/...\": dial tcp 127.0.0.1:6443: connection refused"),
			want: true,
		},
		{
			name: "permanent validation",
			err:  errors.New(`FelixConfiguration.projectcalico.org "default" is invalid: spec.vxlanPort: Invalid value`),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isRetryableCalicoAPIError(tc.err); got != tc.want {
				t.Fatalf("isRetryableCalicoAPIError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestAPIServiceAvailable(t *testing.T) {
	t.Parallel()

	if apiServiceAvailable(nil) {
		t.Fatal("nil should not be available")
	}

	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"status": map[string]interface{}{
			"conditions": []interface{}{
				map[string]interface{}{"type": "Available", "status": "False"},
			},
		},
	}}
	if apiServiceAvailable(obj) {
		t.Fatal("Available=False should not be available")
	}

	obj.Object["status"] = map[string]interface{}{
		"conditions": []interface{}{
			map[string]interface{}{"type": "Available", "status": string(metav1.ConditionTrue)},
		},
	}
	if !apiServiceAvailable(obj) {
		t.Fatal("Available=True should be available")
	}
}

func TestRewriteUpstreamCalicoCIDR(t *testing.T) {
	t.Parallel()

	in := []byte("cidr: 192.168.0.0/16\nencapsulation: VXLANCrossSubnet\n")
	out := string(rewriteUpstreamCalicoCIDR(in, "10.200.0.0/16"))
	if out != "cidr: 10.200.0.0/16\nencapsulation: IPIPCrossSubnet\n" {
		t.Fatalf("got %q", out)
	}
}
