// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package check

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/cilium/cilium/cilium-cli/defaults"
	"github.com/cilium/cilium/cilium-cli/k8s"
)

func ciliumAgentPod(name string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "kube-system",
			Labels:    map[string]string{"k8s-app": "cilium"},
		},
		Status: corev1.PodStatus{Phase: phase},
	}
}

func terminatingCiliumAgentPod(name string) *corev1.Pod {
	pod := ciliumAgentPod(name, corev1.PodRunning)
	now := metav1.Now()
	pod.DeletionTimestamp = &now
	return pod
}

func newCiliumPodsTest(t *testing.T, pods ...*corev1.Pod) (*ConnectivityTest, *bytes.Buffer) {
	t.Helper()

	objs := make([]runtime.Object, 0, len(pods))
	for _, pod := range pods {
		objs = append(objs, pod)
	}
	ct, _ := newFakeConnectivityTest(t, objs...)
	out := &bytes.Buffer{}
	ct.params.Writer = out
	ct.params.CiliumNamespace = "kube-system"
	ct.params.AgentPodSelector = defaults.AgentPodSelector
	ct.ciliumPods = make(map[string]Pod)

	return ct, out
}

func TestInitCiliumPodsSkipsPodsThatAreNotRunning(t *testing.T) {
	ct, out := newCiliumPodsTest(t,
		ciliumAgentPod("cilium-running-1", corev1.PodRunning),
		ciliumAgentPod("cilium-running-2", corev1.PodRunning),
		ciliumAgentPod("cilium-pending", corev1.PodPending),
		ciliumAgentPod("cilium-failed", corev1.PodFailed),
		terminatingCiliumAgentPod("cilium-terminating"),
	)

	require.NoError(t, ct.initCiliumPods(context.Background()))
	require.Len(t, ct.ciliumPods, 2)
	assert.Contains(t, ct.ciliumPods, "cilium-running-1")
	assert.Contains(t, ct.ciliumPods, "cilium-running-2")

	assert.Contains(t, out.String(), "Skipping Cilium pod cilium-pending: Pending")
	assert.Contains(t, out.String(), "Skipping Cilium pod cilium-failed: Failed")
	assert.Contains(t, out.String(), "Skipping Cilium pod cilium-terminating: terminating")
}

func TestInitCiliumPodsWithoutRunningPods(t *testing.T) {
	for _, tc := range []struct {
		name string
		pods []*corev1.Pod
	}{
		{
			name: "no pods",
		},
		{
			name: "only pods that are not running",
			pods: []*corev1.Pod{
				ciliumAgentPod("cilium-pending", corev1.PodPending),
				terminatingCiliumAgentPod("cilium-terminating"),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ct, _ := newCiliumPodsTest(t, tc.pods...)

			assert.ErrorIs(t, ct.initCiliumPods(context.Background()), errNoCiliumPods)
			assert.Empty(t, ct.ciliumPods)
		})
	}
}

func TestInitCiliumPodsMultiCluster(t *testing.T) {
	for _, tc := range []struct {
		name     string
		dstPhase corev1.PodPhase
		pods     int
		err      error
	}{
		{
			name:     "running pod in both clusters",
			dstPhase: corev1.PodRunning,
			pods:     2,
		},
		{
			name:     "no running pod in the second cluster",
			dstPhase: corev1.PodPending,
			err:      errNoCiliumPods,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ct, _ := newCiliumPodsTest(t, ciliumAgentPod("cilium-src", corev1.PodRunning))
			ct.clients = &deploymentClients{
				src: ct.clients.src,
				dst: &k8s.Client{Clientset: fake.NewSimpleClientset(ciliumAgentPod("cilium-dst", tc.dstPhase))},
			}

			err := ct.initCiliumPods(context.Background())
			if tc.err != nil {
				assert.ErrorIs(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			assert.Len(t, ct.ciliumPods, tc.pods)
		})
	}
}
