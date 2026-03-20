package plugin

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"github.com/vmware-tanzu/velero/pkg/plugin/velero"
	corev1api "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"kubevirt.io/kubevirt-velero-plugin/pkg/util"
)

func TestPvcRestoreV2Execute(t *testing.T) {
	testCases := []struct {
		name                string
		input               velero.RestoreItemActionExecuteInput
		expectSkip          bool
		expectOperationID   string
		expectedLabels      map[string]string
	}{
		{
			"Skip the unfinished PVC",
			velero.RestoreItemActionExecuteInput{
				Item: &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "PersistentVolumeClaim",
						"metadata": map[string]interface{}{
							"name": "test-pvc",
							"annotations": map[string]string{
								AnnInProgress: "test-pvc",
							},
							"ownerReferences": []interface{}{
								map[string]interface{}{
									"apiVersion": "cdi.kubevirt.io/v1beta1",
									"kind":       "DataVolume",
									"name":       "test-datavolume",
								},
							},
						},
						"spec": map[string]interface{}{},
					},
				},
			},
			true,
			"",
			nil,
		},
		{
			"Remove resource UID label from PVC and register operation",
			velero.RestoreItemActionExecuteInput{
				Item: &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "PersistentVolumeClaim",
						"metadata": map[string]interface{}{
							"name":      "test-pvc",
							"namespace": "test-namespace",
							"uid":       "633ab84c-8529-487c-8848-99b40fbda9f5",
							"labels": map[string]interface{}{
								util.PVCUIDLabel: "633ab84c-8529-487c-8848-99b40fbda9f5",
								"other-label":    "other-value",
							},
						},
						"spec": map[string]interface{}{},
					},
				},
			},
			false,
			"pvc-binding:test-namespace/test-pvc",
			map[string]string{
				"other-label": "other-value",
			},
		},
		{
			"Restore original UID label value from collision annotation",
			velero.RestoreItemActionExecuteInput{
				Item: &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "PersistentVolumeClaim",
						"metadata": map[string]interface{}{
							"name":      "collision-pvc",
							"namespace": "test-namespace",
							"uid":       "633ab84c-8529-487c-8848-99b40fbda9f5",
							"labels": map[string]interface{}{
								util.PVCUIDLabel: "633ab84c-8529-487c-8848-99b40fbda9f5",
								"other-label":    "other-value",
							},
							"annotations": map[string]interface{}{
								util.OriginalPVCUIDAnnotation: "original-user-uid-value",
							},
						},
						"spec": map[string]interface{}{},
					},
				},
			},
			false,
			"pvc-binding:test-namespace/collision-pvc",
			map[string]string{
				util.PVCUIDLabel: "original-user-uid-value",
				"other-label":    "other-value",
			},
		},
		{
			"Handle PVC without resource UID label",
			velero.RestoreItemActionExecuteInput{
				Item: &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "PersistentVolumeClaim",
						"metadata": map[string]interface{}{
							"name":      "test-pvc",
							"namespace": "test-namespace",
							"uid":       "789def01-2345-6789-abcd-ef0123456789",
							"labels": map[string]interface{}{
								"existing-label": "existing-value",
							},
						},
						"spec": map[string]interface{}{},
					},
				},
			},
			false,
			"pvc-binding:test-namespace/test-pvc",
			map[string]string{
				"existing-label": "existing-value",
			},
		},
		{
			"Handle PVC without any labels",
			velero.RestoreItemActionExecuteInput{
				Item: &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "PersistentVolumeClaim",
						"metadata": map[string]interface{}{
							"name":      "test-pvc",
							"namespace": "test-namespace",
						},
						"spec": map[string]interface{}{},
					},
				},
			},
			false,
			"pvc-binding:test-namespace/test-pvc",
			map[string]string{},
		},
	}

	logrus.SetLevel(logrus.ErrorLevel)
	fakeClient := fake.NewSimpleClientset()
	action := NewPVCRestoreItemActionV2(logrus.StandardLogger(), fakeClient)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := action.Execute(&tc.input)
			if !assert.NoError(t, err) {
				return
			}

			if tc.expectSkip {
				assert.True(t, result.SkipRestore)
				assert.Empty(t, result.OperationID)
				return
			}

			assert.False(t, result.SkipRestore)
			assert.Equal(t, tc.expectOperationID, result.OperationID)

			var resultPVC corev1api.PersistentVolumeClaim
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(result.UpdatedItem.UnstructuredContent(), &resultPVC)
			if !assert.NoError(t, err) {
				return
			}

			if tc.expectedLabels == nil {
				tc.expectedLabels = make(map[string]string)
			}

			if resultPVC.Labels == nil {
				resultPVC.Labels = make(map[string]string)
			}

			assert.Equal(t, len(tc.expectedLabels), len(resultPVC.Labels), "Unexpected number of labels")

			for expectedKey, expectedValue := range tc.expectedLabels {
				actualValue, exists := resultPVC.Labels[expectedKey]
				assert.True(t, exists, "Expected label %s not found", expectedKey)
				assert.Equal(t, expectedValue, actualValue, "Label %s value mismatch", expectedKey)
			}

			if tc.expectedLabels[util.PVCUIDLabel] == "" {
				_, exists := resultPVC.Labels[util.PVCUIDLabel]
				assert.False(t, exists, "Resource UID label should have been removed")
			}

			_, hasCollisionAnnotation := resultPVC.Annotations[util.OriginalPVCUIDAnnotation]
			assert.False(t, hasCollisionAnnotation, "Collision annotation should have been removed")
		})
	}
}

func TestPvcRestoreV2Progress(t *testing.T) {
	restore := &velerov1api.Restore{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-restore",
			Namespace: "velero",
		},
	}

	t.Run("PVC not found reports in-progress", func(t *testing.T) {
		fakeClient := fake.NewSimpleClientset()
		action := NewPVCRestoreItemActionV2(logrus.StandardLogger(), fakeClient)

		progress, err := action.Progress("pvc-binding:test-ns/test-pvc", restore)
		assert.NoError(t, err)
		assert.False(t, progress.Completed)
	})

	t.Run("PVC Pending reports in-progress", func(t *testing.T) {
		pvc := &corev1api.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pvc",
				Namespace: "test-ns",
			},
			Status: corev1api.PersistentVolumeClaimStatus{
				Phase: corev1api.ClaimPending,
			},
		}
		fakeClient := fake.NewSimpleClientset(pvc)
		action := NewPVCRestoreItemActionV2(logrus.StandardLogger(), fakeClient)

		progress, err := action.Progress("pvc-binding:test-ns/test-pvc", restore)
		assert.NoError(t, err)
		assert.False(t, progress.Completed)
	})

	t.Run("PVC Bound reports completed", func(t *testing.T) {
		pvc := &corev1api.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pvc",
				Namespace: "test-ns",
			},
			Status: corev1api.PersistentVolumeClaimStatus{
				Phase: corev1api.ClaimBound,
			},
		}
		fakeClient := fake.NewSimpleClientset(pvc)
		action := NewPVCRestoreItemActionV2(logrus.StandardLogger(), fakeClient)

		progress, err := action.Progress("pvc-binding:test-ns/test-pvc", restore)
		assert.NoError(t, err)
		assert.True(t, progress.Completed)
	})

	t.Run("Invalid operation ID returns error", func(t *testing.T) {
		fakeClient := fake.NewSimpleClientset()
		action := NewPVCRestoreItemActionV2(logrus.StandardLogger(), fakeClient)

		_, err := action.Progress("invalid-prefix:test-ns/test-pvc", restore)
		assert.Error(t, err)
	})

	t.Run("Malformed PVC key returns error", func(t *testing.T) {
		fakeClient := fake.NewSimpleClientset()
		action := NewPVCRestoreItemActionV2(logrus.StandardLogger(), fakeClient)

		_, err := action.Progress("pvc-binding:no-slash", restore)
		assert.Error(t, err)
	})

	t.Run("Namespace remapping is applied", func(t *testing.T) {
		pvc := &corev1api.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pvc",
				Namespace: "remapped-ns",
			},
			Status: corev1api.PersistentVolumeClaimStatus{
				Phase: corev1api.ClaimBound,
			},
		}
		fakeClient := fake.NewSimpleClientset(pvc)
		action := NewPVCRestoreItemActionV2(logrus.StandardLogger(), fakeClient)

		restoreWithMapping := &velerov1api.Restore{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-restore",
				Namespace: "velero",
			},
			Spec: velerov1api.RestoreSpec{
				NamespaceMapping: map[string]string{
					"original-ns": "remapped-ns",
				},
			},
		}

		progress, err := action.Progress("pvc-binding:original-ns/test-pvc", restoreWithMapping)
		assert.NoError(t, err)
		assert.True(t, progress.Completed)
	})
}
