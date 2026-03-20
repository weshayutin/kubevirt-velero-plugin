/*
 * This file is part of the Kubevirt Velero Plugin project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright 2022 Red Hat, Inc.
 *
 */

package plugin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"github.com/vmware-tanzu/velero/pkg/plugin/velero"
	corev1api "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"

	"kubevirt.io/kubevirt-velero-plugin/pkg/util"
)

const operationIDPrefix = "pvc-binding:"

// PVCRestoreItemActionV2 is a V2 restore item action for PVCs that tracks
// PVC binding as an async operation, ensuring Velero waits for PVCs to be
// bound before entering the Finalizing phase.
type PVCRestoreItemActionV2 struct {
	log    logrus.FieldLogger
	client kubernetes.Interface
}

func NewPVCRestoreItemActionV2(log logrus.FieldLogger, client kubernetes.Interface) *PVCRestoreItemActionV2 {
	return &PVCRestoreItemActionV2{log: log, client: client}
}

func (p *PVCRestoreItemActionV2) Name() string {
	return "kubevirt-velero-plugin/restore-pvc-action-v2"
}

func (p *PVCRestoreItemActionV2) AppliesTo() (velero.ResourceSelector, error) {
	return velero.ResourceSelector{
		IncludedResources: []string{"PersistentVolumeClaim"},
	}, nil
}

func (p *PVCRestoreItemActionV2) Execute(input *velero.RestoreItemActionExecuteInput) (*velero.RestoreItemActionExecuteOutput, error) {
	p.log.Info("Executing PVCRestoreItemActionV2")

	if input == nil {
		return nil, fmt.Errorf("input object nil!")
	}

	var pvc corev1api.PersistentVolumeClaim
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(input.Item.UnstructuredContent(), &pvc); err != nil {
		return nil, errors.WithStack(err)
	}

	p.log.Infof("handling PVC %v/%v", pvc.GetNamespace(), pvc.GetName())

	annotations := pvc.GetAnnotations()
	_, inProgress := annotations[AnnInProgress]
	if inProgress {
		return velero.NewRestoreItemActionExecuteOutput(input.Item).WithoutRestore(), nil
	}

	if pvc.Labels != nil {
		if _, exists := pvc.Labels[util.PVCUIDLabel]; exists {
			if pvc.Annotations != nil {
				if originalValue, hasOriginal := pvc.Annotations[util.OriginalPVCUIDAnnotation]; hasOriginal {
					pvc.Labels[util.PVCUIDLabel] = originalValue
					delete(pvc.Annotations, util.OriginalPVCUIDAnnotation)
				} else {
					delete(pvc.Labels, util.PVCUIDLabel)
				}
			} else {
				delete(pvc.Labels, util.PVCUIDLabel)
			}
		}
	}

	item, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&pvc)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	operationID := operationIDPrefix + pvc.GetNamespace() + "/" + pvc.GetName()
	p.log.Infof("Registering async operation %s to track PVC binding", operationID)

	return velero.NewRestoreItemActionExecuteOutput(&unstructured.Unstructured{Object: item}).
		WithOperationID(operationID), nil
}

func (p *PVCRestoreItemActionV2) Progress(operationID string, restore *velerov1api.Restore) (velero.OperationProgress, error) {
	if !strings.HasPrefix(operationID, operationIDPrefix) {
		return velero.OperationProgress{}, fmt.Errorf("invalid operation ID: %s", operationID)
	}

	pvcKey := strings.TrimPrefix(operationID, operationIDPrefix)
	parts := strings.SplitN(pvcKey, "/", 2)
	if len(parts) != 2 {
		return velero.OperationProgress{}, fmt.Errorf("malformed PVC key in operation ID: %s", pvcKey)
	}
	namespace, name := parts[0], parts[1]

	if restore.Spec.NamespaceMapping != nil {
		for orig, mapped := range restore.Spec.NamespaceMapping {
			if orig == namespace {
				namespace = mapped
				break
			}
		}
	}

	pvc, err := p.client.CoreV1().PersistentVolumeClaims(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		p.log.WithError(err).Warnf("PVC %s/%s not found yet, reporting in-progress", namespace, name)
		return velero.OperationProgress{
			Completed:   false,
			Description: fmt.Sprintf("Waiting for PVC %s/%s to exist", namespace, name),
			NCompleted:  0,
			NTotal:      1,
			Updated:     time.Now(),
		}, nil
	}

	if pvc.Status.Phase == corev1api.ClaimBound {
		p.log.Infof("PVC %s/%s is Bound, operation complete", namespace, name)
		return velero.OperationProgress{
			Completed:  true,
			NCompleted: 1,
			NTotal:     1,
			Updated:    time.Now(),
		}, nil
	}

	p.log.Infof("PVC %s/%s phase is %s, still waiting", namespace, name, pvc.Status.Phase)
	return velero.OperationProgress{
		Completed:   false,
		Description: fmt.Sprintf("PVC %s/%s phase: %s", namespace, name, pvc.Status.Phase),
		NCompleted:  0,
		NTotal:      1,
		Updated:     time.Now(),
	}, nil
}

func (p *PVCRestoreItemActionV2) Cancel(operationID string, restore *velerov1api.Restore) error {
	return nil
}

func (p *PVCRestoreItemActionV2) AreAdditionalItemsReady(additionalItems []velero.ResourceIdentifier, restore *velerov1api.Restore) (bool, error) {
	return true, nil
}
